package infrastructure

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

// maxCursor is the "start from the newest row" observation cursor.
var maxCursor = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))

var fixedNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

func newTestUseCase(t *testing.T) (*InfrastructureUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	return NewInfrastructureUseCase(Dependencies{
		Observations: eventLabObservationRepo.New(q),
		Stands:       platformStandRepo.New(q),
		Now:          func() time.Time { return fixedNow },
	}), q
}

func TestCurrentLabMonitoringReadsActiveEventsByDefaultAndNeverLeaksSecrets(t *testing.T) {
	uc, q := newTestUseCase(t)
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListPlatformLabMonitoringCurrent(gomock.Any(), postgres.ListPlatformLabMonitoringCurrentParams{
		IncludeRecent: false, RecentSince: fixedNow.Add(-labMonitoringModel.RecentWindow), Now: fixedNow,
	}).Return([]postgres.ListPlatformLabMonitoringCurrentRow{{
		EventID: eventID, EventName: "CTF", EventTeamID: teamID, TeamName: "Red", LabGroupName: "g", AgentID: "a", Sequence: 4,
		Payload: []byte(`{"labs":[{"name":"web","specJson":"c2VjcmV0"}]}`),
	}}, nil)

	got, err := uc.CurrentLabMonitoring(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EventName != "CTF" || got[0].TeamName != "Red" || got[0].Sequence != 4 {
		t.Fatalf("unexpected current state: %+v", got)
	}
	if strings.Contains(string(got[0].Payload), "specJson") {
		t.Fatalf("payload leaks the lab spec: %s", got[0].Payload)
	}
}

func TestCurrentLabMonitoringPassesIncludeRecent(t *testing.T) {
	uc, q := newTestUseCase(t)
	q.EXPECT().ListPlatformLabMonitoringCurrent(gomock.Any(), gomock.Cond(func(x any) bool {
		return x.(postgres.ListPlatformLabMonitoringCurrentParams).IncludeRecent
	})).Return(nil, nil)
	if _, err := uc.CurrentLabMonitoring(context.Background(), true); err != nil {
		t.Fatal(err)
	}
}

func TestListLabMonitoringReportsNextCursorOnlyWhenThereIsMore(t *testing.T) {
	uc, q := newTestUseCase(t)
	ids := []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}
	rows := make([]postgres.ListPlatformLabObservationsRow, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, postgres.ListPlatformLabObservationsRow{ID: id, EventName: "CTF", TeamName: "Red", Payload: []byte(`{}`)})
	}
	// pageSize 2 must ask for 3 rows.
	q.EXPECT().ListPlatformLabObservations(gomock.Any(), gomock.Cond(func(x any) bool {
		return x.(postgres.ListPlatformLabObservationsParams).LimitVal == 3
	})).Return(rows, nil)

	page, err := uc.ListLabMonitoring(context.Background(), uuid.NullUUID{}, uuid.NullUUID{}, fixedNow.Add(-time.Hour), fixedNow, fixedNow, maxCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || !page.HasMore || page.NextCursor == nil || *page.NextCursor != ids[1] {
		t.Fatalf("page = %+v, want 2 items, hasMore and next cursor = second id", page)
	}
	if page.Items[0].EventName != "CTF" || page.Items[0].TeamName != "Red" {
		t.Fatalf("names missing from the view: %+v", page.Items[0])
	}

	q.EXPECT().ListPlatformLabObservations(gomock.Any(), gomock.Any()).Return(rows[:2], nil)
	page, err = uc.ListLabMonitoring(context.Background(), uuid.NullUUID{}, uuid.NullUUID{}, fixedNow.Add(-time.Hour), fixedNow, fixedNow, maxCursor, 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 || page.HasMore || page.NextCursor != nil {
		t.Fatalf("last page = %+v, want no cursor", page)
	}
}

func TestListCapacityMonitoringPaginates(t *testing.T) {
	uc, q := newTestUseCase(t)
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListPlatformLabCapacityObservations(gomock.Any(), gomock.Any()).Return([]postgres.PlatformLabCapacityObservation{{ID: first}, {ID: second}}, nil)
	page, err := uc.ListCapacityMonitoring(context.Background(), fixedNow.Add(-time.Hour), fixedNow, fixedNow, maxCursor, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || !page.HasMore || page.NextCursor == nil || *page.NextCursor != first {
		t.Fatalf("page = %+v", page)
	}
}

func TestInfrastructureSummaryCountsStandsAndSumsClusterCapacity(t *testing.T) {
	uc, q := newTestUseCase(t)
	q.EXPECT().CountPlatformStandsByKindStatus(gomock.Any()).Return([]postgres.CountPlatformStandsByKindStatusRow{
		{Status: int16(eventStandModel.StatusCreating), Total: 1}, {Status: int16(eventStandModel.StatusReady), Total: 4},
		{Status: int16(eventStandModel.StatusFailed), Total: 3}, {Status: int16(eventStandModel.StatusRemoved), Total: 7},
		{Moderators: true, Status: int16(eventStandModel.StatusCreating), Total: 1}, {Moderators: true, Status: int16(eventStandModel.StatusReady), Total: 1},
	}, nil)
	q.EXPECT().CountPlatformTestLabs(gomock.Any(), fixedNow).Return(postgres.CountPlatformTestLabsRow{Active: 3, Expired: 1}, nil)
	q.EXPECT().ListLatestPlatformLabCapacityObservations(gomock.Any()).Return([]postgres.PlatformLabCapacityObservation{
		{Payload: []byte(`{"allocatableCpuMillicores":"4000","requestedCpuMillicores":"1000","allocatableMemoryBytes":"1000","requestedMemoryBytes":"250"}`)},
		{Payload: []byte(`{"allocatableCpuMillicores":4000,"requestedCpuMillicores":3000,"allocatableMemoryBytes":"1000","requestedMemoryBytes":"750"}`)},
		{Payload: []byte(`not json`)},
	}, nil)

	got, err := uc.InfrastructureSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Stands != (StandCountsView{Total: 17, Creating: 2, Ready: 5, Failed: 3, Removed: 7, Active: 7}) {
		t.Fatalf("stand counts = %+v", got.Stands)
	}
	if got.Moderators != (StandCountsView{Total: 2, Creating: 1, Ready: 1, Active: 2}) {
		t.Fatalf("moderators counts = %+v", got.Moderators)
	}
	if got.TestLabs != (TestLabCountsView{Total: 4, Active: 3, Expired: 1}) {
		t.Fatalf("test lab counts = %+v", got.TestLabs)
	}
	if !got.Capacity.Available || got.Capacity.CPUPercent == nil || *got.Capacity.CPUPercent != 50 || got.Capacity.MemoryPercent == nil || *got.Capacity.MemoryPercent != 50 {
		t.Fatalf("capacity = %+v, want 50%% cpu and memory", got.Capacity)
	}
}

func TestInfrastructureSummaryWithoutCapacityHasNoPercentages(t *testing.T) {
	uc, q := newTestUseCase(t)
	q.EXPECT().CountPlatformStandsByKindStatus(gomock.Any()).Return(nil, nil)
	q.EXPECT().CountPlatformTestLabs(gomock.Any(), fixedNow).Return(postgres.CountPlatformTestLabsRow{}, nil)
	q.EXPECT().ListLatestPlatformLabCapacityObservations(gomock.Any()).Return(nil, nil)
	got, err := uc.InfrastructureSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.Capacity.Available || got.Capacity.CPUPercent != nil || got.Capacity.MemoryPercent != nil {
		t.Fatalf("capacity = %+v, want unavailable", got.Capacity)
	}
}

func TestListStandsHidesTheTechnicalModeratorsTeamNameAndPages(t *testing.T) {
	uc, q := newTestUseCase(t)
	standEvent, standTeam := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListPlatformStands(gomock.Any(), gomock.Cond(func(x any) bool {
		p := x.(postgres.ListPlatformStandsParams)
		return p.LimitVal == 25 && p.OffsetVal == 50 && p.Search == "red" && len(p.Statuses) == 2
	})).Return([]postgres.ListPlatformStandsRow{
		{EventID: standEvent, EventTeamID: standTeam, EventName: "CTF", EventTag: "ctf", TeamName: "moderators:x", Moderators: true, Status: int16(eventStandModel.StatusReady), Total: 60},
		{EventName: "CTF", EventTag: "ctf", TeamName: "Red", Status: int16(eventStandModel.StatusFailed), Reason: "ErrImagePull", Generation: 2, Total: 60},
	}, nil)

	q.EXPECT().ListPlatformLabMonitoringCurrent(gomock.Any(), gomock.Any()).Return([]postgres.ListPlatformLabMonitoringCurrentRow{
		{EventID: standEvent, EventTeamID: standTeam, LabGroupName: "g1", Payload: []byte(`{"labs":[{"status":{"devices":[
			{"usageAvailable":true,"cpuMillicores":"120","memoryBytes":"1000","cpuRequestMillicores":"500","memoryRequestBytes":"2000"},
			{"usageAvailable":false,"cpuRequestMillicores":500,"memoryRequestBytes":"2000"}]}}]}`)},
		{EventID: standEvent, EventTeamID: standTeam, LabGroupName: "g2", Payload: []byte(`{"labs":[{"status":{"devices":[{"usageAvailable":true,"cpuMillicores":30,"memoryBytes":"24"}]}}]}`)},
	}, nil)

	page, err := uc.ListStands(context.Background(), StandsFilter{Statuses: []eventStandModel.Status{eventStandModel.StatusCreating, eventStandModel.StatusReady}, Search: "red", Page: 3, PageSize: 25})
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 60 || page.Page != 3 || len(page.Items) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Items[0].TeamName != "" || !page.Items[0].Moderators {
		t.Fatalf("moderators team name must be blank: %+v", page.Items[0])
	}
	// Usage is summed over the devices that report it; the requests over all devices.
	if got := page.Items[0].Resources; got != (ResourcesView{Known: true, Available: true, CPUMillicores: 150, MemoryBytes: 1024, RequestedCPU: 1000, RequestedMemory: 4000}) {
		t.Fatalf("resources = %+v", got)
	}
	if page.Items[1].Resources.Known {
		t.Fatalf("a stand without observations has unknown resources: %+v", page.Items[1].Resources)
	}
	if page.Items[1].TeamName != "Red" || page.Items[1].Status != eventStandModel.StatusFailed || page.Items[1].Reason != "ErrImagePull" || page.Items[1].Generation != 2 {
		t.Fatalf("stand mapped wrongly: %+v", page.Items[1])
	}
}

func TestListStandsReportsTheLaunchQueueOfAStand(t *testing.T) {
	uc, q := newTestUseCase(t)
	standEvent, standTeam := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListPlatformStands(gomock.Any(), gomock.Any()).Return([]postgres.ListPlatformStandsRow{
		{EventID: standEvent, EventTeamID: standTeam, EventName: "CTF", Status: int16(eventStandModel.StatusCreating), Total: 2},
		{EventName: "CTF", Status: int16(eventStandModel.StatusReady), Total: 2},
	}, nil)
	q.EXPECT().ListPlatformLabMonitoringCurrent(gomock.Any(), gomock.Any()).Return([]postgres.ListPlatformLabMonitoringCurrentRow{
		{EventID: standEvent, EventTeamID: standTeam, LabGroupName: "g1", Payload: []byte(`{"groups":[{"status":{"imageWarning":"vpn:1"}}],"labs":[
			{"status":{"phase":"Queued","scheduling":{"position":6,"length":"12","reason":"InFlightLimit"}}},
			{"status":{"phase":"Queued","scheduling":{"position":3,"length":"12","reason":"PreparingImages"}}}]}`)},
	}, nil)
	page, err := uc.ListStands(context.Background(), StandsFilter{Page: 1, PageSize: 25})
	if err != nil {
		t.Fatal(err)
	}
	if got := page.Items[0].Launch; got.QueuedLabs != 2 || got.Position != 3 || got.Length != 12 || got.Reason != "PreparingImages" || !got.ImageWarning {
		t.Fatalf("launch = %+v", got)
	}
	if got := page.Items[1].Launch; got.QueuedLabs != 0 || got.ImageWarning {
		t.Fatalf("an unobserved stand has no queue: %+v", got)
	}
}

func TestInfrastructureSummaryReadsTheTenantCapacityView(t *testing.T) {
	uc, q := newTestUseCase(t)
	q.EXPECT().CountPlatformStandsByKindStatus(gomock.Any()).Return(nil, nil)
	q.EXPECT().CountPlatformTestLabs(gomock.Any(), fixedNow).Return(postgres.CountPlatformTestLabsRow{}, nil)
	q.EXPECT().ListLatestPlatformLabCapacityObservations(gomock.Any()).Return([]postgres.PlatformLabCapacityObservation{
		{Payload: []byte(`{"tenant":"platform","hasCpuQuota":true,"cpuQuotaMillicores":"8000","hasMemoryQuota":true,"memoryQuotaBytes":"2000","cpuReservedMillicores":"2000","memoryReservedBytes":"500","cpuFreeMillicores":"6000"}`)},
		{Payload: []byte(`{"tenant":"platform"}`)},
	}, nil)
	got, err := uc.InfrastructureSummary(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !got.Capacity.Available || got.Capacity.CPUPercent == nil || *got.Capacity.CPUPercent != 25 || got.Capacity.MemoryPercent == nil || *got.Capacity.MemoryPercent != 25 {
		t.Fatalf("capacity = %+v, want 25%% cpu and memory of the quota", got.Capacity)
	}
}
