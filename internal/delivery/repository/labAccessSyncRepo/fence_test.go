package labAccessSyncRepo_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/labAccessSyncRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"
	"testing"
	"time"
)

func TestObserveAccessFenceRejectsUnknownStaleBootGenerationAndErrors(t *testing.T) {
	at := time.Now()
	operation := uuid.Must(uuid.NewV7())
	team := uuid.Must(uuid.NewV7())
	target := eventLabModel.AccessTarget{Group: "group", ExpectedGroupUID: "group-uid", OperationID: operation, Revision: 7}
	for name, change := range map[string]func(*labpb.MonitoringUpdate, *time.Time){
		"exact":                    func(*labpb.MonitoringUpdate, *time.Time) {},
		"unavailable current boot": func(m *labpb.MonitoringUpdate, _ *time.Time) { m.Groups[0].Status.CurrentVpnBootAvailable = false },
		"different boot":           func(m *labpb.MonitoringUpdate, _ *time.Time) { m.Groups[0].Status.CurrentVpnBootId = "boot-2" },
		"stale startup witness": func(m *labpb.MonitoringUpdate, _ *time.Time) {
			m.Groups[0].Status.CurrentVpnBootObservedUnixMs = at.Add(-time.Minute).UnixMilli()
		},
		"missing startup time":    func(m *labpb.MonitoringUpdate, _ *time.Time) { m.Groups[0].Status.CurrentVpnBootObservedUnixMs = 0 },
		"stale monitoring":        func(_ *labpb.MonitoringUpdate, t *time.Time) { *t = at.Add(-time.Minute) },
		"future monitoring":       func(_ *labpb.MonitoringUpdate, t *time.Time) { *t = at.Add(time.Minute) },
		"wrong group incarnation": func(m *labpb.MonitoringUpdate, _ *time.Time) { m.Groups[0].Uid = "old-group" },
		"stale policy generation": func(m *labpb.MonitoringUpdate, _ *time.Time) { m.Policies[0].Status.ObservedGeneration = 2 },
		"policy error":            func(m *labpb.MonitoringUpdate, _ *time.Time) { m.Policies[0].Status.LastError = "conntrack failed" },
		"accepted only":           func(m *labpb.MonitoringUpdate, _ *time.Time) { m.Policies[0].Status.State = "Accepted" },
		"wrong spec operation": func(m *labpb.MonitoringUpdate, _ *time.Time) {
			m.Policies[0].OperationId = uuid.Must(uuid.NewV7()).String()
		},
		"old applied revision": func(m *labpb.MonitoringUpdate, _ *time.Time) { m.Policies[0].Status.AppliedRevision = 6 },
	} {
		t.Run(name, func(t *testing.T) {
			q := postgresMocks.NewMockQuerier(gomock.NewController(t))
			rowAt := at
			m := &labpb.MonitoringUpdate{Groups: []*labpb.LabGroup{{Name: "group", Uid: "group-uid", Status: &labpb.LabGroupStatus{CurrentVpnBootAvailable: true, CurrentVpnBootId: "boot-1", CurrentVpnBootObservedUnixMs: at.UnixMilli()}}}, Policies: []*labpb.LabGroupAccessPolicy{{LabGroupName: "group", ExpectedGroupUid: "group-uid", PolicyUid: "policy-uid", Generation: 3, OperationId: operation.String(), DesiredRevision: 7, Status: &labpb.LabGroupAccessPolicyStatus{State: "Applied", ObservedGeneration: 3, AppliedRevision: 7, OperationId: operation.String(), VpnBootId: "boot-1", AppliedAtUnixMs: at.UnixMilli()}}}}
			change(m, &rowAt)
			raw, err := protojson.Marshal(m)
			if err != nil {
				t.Fatal(err)
			}
			q.EXPECT().GetCurrentEventLabAccessMonitoring(gomock.Any(), team).Return(postgres.LabMonitoringCurrent{LabGroupName: "group", ObservedAt: rowAt, Payload: raw}, nil)
			if name == "exact" {
				q.EXPECT().MarkEventLabAccessSyncApplied(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.MarkEventLabAccessSyncAppliedParams) (int64, error) {
					if p.OperationID != operation || p.DesiredRevision != 7 || p.AccessFenceVpnBootID != "boot-1" || p.ExpectedGroupUid != "group-uid" {
						t.Fatal(p)
					}
					return 1, nil
				})
			}
			repo := labAccessSyncRepo.New(q)
			got, err := repo.ObserveAccessFence(context.Background(), team)
			if err != nil {
				t.Fatal(err)
			}
			changed, err := repo.MarkApplied(context.Background(), labAccessSyncRepo.Sync{TeamID: team}, target, "fingerprint", got, at.Add(time.Second))
			if err != nil || (changed == 1) != (name == "exact") {
				t.Fatalf("mark=%d %v observation=%+v", changed, err, got)
			}
		})
	}
}
