package infrastructure

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

type (
	// StandsFilter narrows the platform stand table. Statuses empty = all.
	StandsFilter struct {
		EventID  uuid.NullUUID
		Statuses []eventStandModel.Status
		// Kind is "" for every stand, "event" for event teams or "moderators" for the moderators team.
		Kind     string
		Search   string
		Page     int
		PageSize int
	}

	PlatformStandView struct {
		EventID         uuid.UUID
		EventName       string
		EventTag        string
		TeamID          uuid.UUID
		TeamName        string
		Moderators      bool
		Status          eventStandModel.Status
		Reason          string
		UpdatedAt       time.Time
		StatusChangedAt time.Time
		Generation      int32
		// Resources is what the stand's labs use now; zero value when not observed.
		Resources ResourcesView
		// Launch is how many of the stand's labs wait in the launch queue (and the place of the
		// best one) and whether an image is pulled by tag; from the current monitoring state.
		Launch LaunchView
	}

	StandsPage struct {
		Items    []PlatformStandView
		Total    int64
		Page     int
		PageSize int
	}

	StandEventView struct {
		ID   uuid.UUID
		Name string
		Tag  string
	}

	StandCountsView struct {
		Total, Creating, Ready, Failed, Removed, Active int64
	}

	// CapacityUsageView is the cluster-wide requested/allocatable ratio in
	// percent, summed over agents. Percentages are nil while no capacity sample
	// with a non-zero allocatable amount exists.
	CapacityUsageView struct {
		Available     bool
		CPUPercent    *float64
		MemoryPercent *float64
	}

	// TestLabCountsView counts the catalog test labs: Active have a running
	// lease, Expired wait for the cleanup pass.
	TestLabCountsView struct {
		Total, Active, Expired int64
	}

	// SummaryView: Stands counts every team stand (event teams and the moderators
	// team), Moderators is the moderators part of it, TestLabs are the catalog
	// test labs, which are not team stands.
	SummaryView struct {
		Stands     StandCountsView
		Moderators StandCountsView
		TestLabs   TestLabCountsView
		Capacity   CapacityUsageView
	}
)

// ListStands returns one page of stands across events.
func (u *InfrastructureUseCase) ListStands(ctx context.Context, filter StandsFilter) (StandsPage, error) {
	items, total, err := u.stands.List(ctx, platformStandRepo.Filter{
		EventID: filter.EventID, Statuses: filter.Statuses, Kind: filter.Kind, Search: filter.Search,
		Limit: int32(filter.PageSize), Offset: int32((filter.Page - 1) * filter.PageSize),
	})
	if err != nil {
		return StandsPage{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list platform stands").Err()
	}
	// The resources are optional: a failed read leaves them unknown, the list still loads.
	var resources map[standKey]ResourcesView
	var launch map[standKey]LaunchView
	if current, currentErr := u.observations.CurrentPlatform(ctx, true, u.now().Add(-labMonitoringModel.RecentWindow), u.now()); currentErr == nil {
		resources = standResources(current)
		launch = standLaunch(current)
	}
	out := make([]PlatformStandView, 0, len(items))
	for _, item := range items {
		team := item.TeamName
		if item.Moderators {
			// The stored name is technical; clients label the moderators team.
			team = ""
		}
		out = append(out, PlatformStandView{
			EventID: item.EventID, EventName: item.EventName, EventTag: item.EventTag, TeamID: item.TeamID, TeamName: team,
			Moderators: item.Moderators, Status: item.Status, Reason: item.Reason, UpdatedAt: item.UpdatedAt,
			StatusChangedAt: item.StatusChangedAt, Generation: item.Generation, Resources: resources[standKey{item.EventID, item.TeamID}], Launch: launch[standKey{item.EventID, item.TeamID}],
		})
	}
	return StandsPage{Items: out, Total: total, Page: filter.Page, PageSize: filter.PageSize}, nil
}

// ListStandEvents lists the events that have stands, for the table's filter.
func (u *InfrastructureUseCase) ListStandEvents(ctx context.Context) ([]StandEventView, error) {
	events, err := u.stands.Events(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list stand events").Err()
	}
	out := make([]StandEventView, 0, len(events))
	for _, event := range events {
		out = append(out, StandEventView{ID: event.ID, Name: event.Name, Tag: event.Tag})
	}
	return out, nil
}

// InfrastructureSummary is the dashboard aggregate: stand counts and cluster
// CPU/RAM usage in one read.
func (u *InfrastructureUseCase) InfrastructureSummary(ctx context.Context) (SummaryView, error) {
	eventCounts, moderatorCounts, err := u.stands.CountByKind(ctx)
	if err != nil {
		return SummaryView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count platform stands").Err()
	}
	testLabs, err := u.stands.CountTestLabs(ctx, u.now())
	if err != nil {
		return SummaryView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count test laboratories").Err()
	}
	capacity, err := u.observations.LatestCapacity(ctx)
	if err != nil {
		return SummaryView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load laboratory capacity monitoring").Err()
	}
	summary := SummaryView{
		Stands:     standCounts(eventCounts, moderatorCounts),
		Moderators: standCounts(moderatorCounts),
		TestLabs:   TestLabCountsView{Total: testLabs.Active + testLabs.Expired, Active: testLabs.Active, Expired: testLabs.Expired},
	}

	var allocCPU, reqCPU, allocMem, reqMem int64
	for _, sample := range capacity {
		// The agent reports the platform's own tenant view: the quota is the capacity and what its
		// pods request is the reservation. Samples from before tenancy carry allocatable*/requested*.
		var payload struct {
			QuotaCPU          flexInt `json:"cpuQuotaMillicores"`
			ReservedCPU       flexInt `json:"cpuReservedMillicores"`
			QuotaMemory       flexInt `json:"memoryQuotaBytes"`
			ReservedMemory    flexInt `json:"memoryReservedBytes"`
			AllocatableCPU    flexInt `json:"allocatableCpuMillicores"`
			RequestedCPU      flexInt `json:"requestedCpuMillicores"`
			AllocatableMemory flexInt `json:"allocatableMemoryBytes"`
			RequestedMemory   flexInt `json:"requestedMemoryBytes"`
		}
		if json.Unmarshal(sample.Payload, &payload) != nil {
			continue
		}
		allocCPU += int64(first(payload.QuotaCPU, payload.AllocatableCPU))
		reqCPU += int64(first(payload.ReservedCPU, payload.RequestedCPU))
		allocMem += int64(first(payload.QuotaMemory, payload.AllocatableMemory))
		reqMem += int64(first(payload.ReservedMemory, payload.RequestedMemory))
	}
	summary.Capacity = CapacityUsageView{Available: allocCPU > 0 || allocMem > 0, CPUPercent: percent(reqCPU, allocCPU), MemoryPercent: percent(reqMem, allocMem)}
	return summary, nil
}

// standCounts sums the per-status counts of the given kinds.
func standCounts(kinds ...platformStandRepo.StandKindCounts) StandCountsView {
	var view StandCountsView
	for _, counts := range kinds {
		view.Creating += counts[eventStandModel.StatusCreating]
		view.Ready += counts[eventStandModel.StatusReady]
		view.Failed += counts[eventStandModel.StatusFailed]
		view.Removed += counts[eventStandModel.StatusRemoved]
	}
	view.Active = view.Creating + view.Ready
	view.Total = view.Active + view.Failed + view.Removed
	return view
}

// first is the first of the values that is not zero (a zero field is absent in protojson).
func first(values ...flexInt) flexInt {
	for _, v := range values {
		if v != 0 {
			return v
		}
	}
	return 0
}

func percent(requested, allocatable int64) *float64 {
	if allocatable <= 0 {
		return nil
	}
	value := math.Round(float64(requested)/float64(allocatable)*1000) / 10
	return &value
}

// flexInt reads a protojson int64, which is encoded as a string.
type flexInt int64

func (f *flexInt) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err == nil {
		value, parseErr := strconv.ParseInt(text, 10, 64)
		if parseErr != nil {
			return parseErr
		}
		*f = flexInt(value)
		return nil
	}
	var number int64
	if err := json.Unmarshal(data, &number); err != nil {
		return err
	}
	*f = flexInt(number)
	return nil
}
