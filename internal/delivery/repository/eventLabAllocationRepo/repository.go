// Package eventLabAllocationRepo exposes narrow held-ledger read models and
// insert-only group admission envelopes. Lifecycle writes remain in eventLabRepo.
package eventLabAllocationRepo

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/resourceCalendarRepo"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"time"
)

type Queries interface {
	LockEventConfigForLabSizing(context.Context, uuid.UUID) (uuid.UUID, error)
	ListUnaccountedEventLabStarts(context.Context) ([]postgres.ListUnaccountedEventLabStartsRow, error)
	LockResourceCalendar(context.Context) error
	ListEventLabAllocations(context.Context, uuid.UUID) ([]postgres.EventTeamLab, error)
	ListPlatformLabAllocations(context.Context) ([]postgres.EventTeamLab, error)
	ListEventGroupAllocations(context.Context, uuid.UUID) ([]postgres.EventTeamGroupAllocation, error)
	ListPlatformGroupAllocations(context.Context) ([]postgres.EventTeamGroupAllocation, error)
	CreateEventGroupAllocation(context.Context, postgres.CreateEventGroupAllocationParams) error
	GetEventResourceReservation(context.Context, uuid.NullUUID) (postgres.ResourceReservation, error)
	GetEventPlannedMaxUsers(context.Context, uuid.UUID) (int64, error)
}
type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q} }
func (r *Repository) Labs(ctx context.Context, eventID uuid.UUID) ([]eventLabModel.Lab, error) {
	rows, err := r.q.ListEventLabAllocations(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return labs(rows)
}
func (r *Repository) PlatformLabs(ctx context.Context) ([]eventLabModel.Lab, error) {
	rows, err := r.q.ListPlatformLabAllocations(ctx)
	if err != nil {
		return nil, err
	}
	return labs(rows)
}
func labs(rows []postgres.EventTeamLab) ([]eventLabModel.Lab, error) {
	out := make([]eventLabModel.Lab, 0, len(rows))
	for _, row := range rows {
		l, err := eventLabRepo.ToDomain(row)
		if err != nil {
			return nil, err
		}
		out = append(out, l)
	}
	return out, nil
}

type Group struct {
	Lifecycle       eventLabModel.Group
	TeamID, EventID uuid.UUID
	Name            string
	Sizes           infraModel.GroupSizes
	Plan            infraModel.GroupPlan
	CreatedAt       time.Time
}

func groups(rows []postgres.EventTeamGroupAllocation) []Group {
	out := make([]Group, 0, len(rows))
	for _, row := range rows {
		g := Group{TeamID: row.EventTeamID, EventID: row.EventID, Name: row.LabGroupName, CreatedAt: row.CreatedAt}
		g.Lifecycle = groupFromRow(row)
		g.Sizes.VPN.CPUMillicores = row.VpnCpuMillicores
		g.Sizes.VPN.MemoryBytes = row.VpnMemoryBytes
		g.Sizes.Gateway.CPUMillicores = row.GatewayCpuMillicores
		g.Sizes.Gateway.MemoryBytes = row.GatewayMemoryBytes
		_ = json.Unmarshal(row.Plan, &g.Plan)
		out = append(out, g)
	}
	return out
}
func (r *Repository) Groups(ctx context.Context, eventID uuid.UUID) ([]Group, error) {
	rows, err := r.q.ListEventGroupAllocations(ctx, eventID)
	return groups(rows), err
}
func (r *Repository) PlatformGroups(ctx context.Context) ([]Group, error) {
	rows, err := r.q.ListPlatformGroupAllocations(ctx)
	return groups(rows), err
}
func (r *Repository) CreateGroup(ctx context.Context, g Group) error {
	plan, err := json.Marshal(g.Plan)
	if err != nil {
		return err
	}
	total := g.Sizes.Total()
	entity := eventLabModel.NewGroup(g.EventID, g.TeamID, g.Name, eventLabModel.Compute{CPUMillicores: total.CPUMillicores, MemoryBytes: total.MemoryBytes}, g.CreatedAt)
	return r.q.CreateEventGroupAllocation(ctx, postgres.CreateEventGroupAllocationParams{EventTeamID: g.TeamID, EventID: g.EventID, LabGroupName: g.Name, VpnCpuMillicores: g.Sizes.VPN.CPUMillicores, VpnMemoryBytes: g.Sizes.VPN.MemoryBytes, GatewayCpuMillicores: g.Sizes.Gateway.CPUMillicores, GatewayMemoryBytes: g.Sizes.Gateway.MemoryBytes, OperationID: entity.OperationID, NextAttemptAt: entity.NextAttemptAt, Allocation: initialGroupAllocation(g), UpdatedAt: g.CreatedAt, Plan: plan, CreatedAt: g.CreatedAt})
}

func (r *Repository) Budget(ctx context.Context, eventID uuid.UUID) (calModel.Reservation, error) {
	row, err := r.q.GetEventResourceReservation(ctx, uuid.NullUUID{UUID: eventID, Valid: true})
	return resourceCalendarRepo.FromRow(row), err
}

func (r *Repository) UnaccountedStarts(ctx context.Context) (map[uuid.UUID]bool, error) {
	rows, err := r.q.ListUnaccountedEventLabStarts(ctx)
	out := map[uuid.UUID]bool{}
	for _, row := range rows {
		out[row.EventID] = true
	}
	return out, err
}

func initialGroupAllocation(g Group) []byte {
	total := g.Sizes.Total()
	v := eventLabModel.Allocation{ConfiguredRequests: eventLabModel.Compute{CPUMillicores: total.CPUMillicores, MemoryBytes: total.MemoryBytes}, ConfiguredLimits: eventLabModel.Compute{CPUMillicores: total.CPUMillicores, MemoryBytes: total.MemoryBytes}, AllocatedRequests: eventLabModel.Compute{CPUMillicores: total.CPUMillicores, MemoryBytes: total.MemoryBytes}, RuntimeState: "Admitted", StorageState: "None"}
	raw, _ := json.Marshal(v)
	return raw
}
func groupFromRow(row postgres.EventTeamGroupAllocation) eventLabModel.Group {
	var a eventLabModel.Allocation
	_ = json.Unmarshal(row.Allocation, &a)
	pointer := func(v pgtype.Timestamptz) *time.Time {
		if !v.Valid {
			return nil
		}
		return &v.Time
	}
	var target *eventLabModel.GroupTarget
	_ = json.Unmarshal(row.RetirementStopTarget, &target)
	return eventLabModel.Group{RetirementStopTarget: target, RetirementState: row.RetirementState, RetirementObservedAt: pointer(row.RetirementObservedAt), RetirementError: row.RetirementError, EventID: row.EventID, TeamID: row.EventTeamID, Name: row.LabGroupName, AgentUID: row.AgentUid, AgentGeneration: row.AgentGeneration, Revision: row.DesiredRevision, ObservedRevision: row.ObservedRevision, OperationID: row.OperationID, DesiredState: row.DesiredState, ActualState: row.ActualState, Ready: row.Ready, AccessFenced: row.AccessFenced, PendingStarts: row.PendingStarts, ObservedAt: pointer(row.ObservedAt), RetentionUntil: pointer(row.RetentionUntil), ProtectedUntil: pointer(row.ProtectedUntil), Allocation: a, ConfiguredRequests: eventLabModel.Compute{CPUMillicores: row.VpnCpuMillicores + row.GatewayCpuMillicores, MemoryBytes: row.VpnMemoryBytes + row.GatewayMemoryBytes}, FailureCode: row.FailureCode, FailureMessage: row.FailureMessage, NextAttemptAt: row.NextAttemptAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

// ToGroupDomain is shared with the whole group lifecycle repository.
func ToGroupDomain(row postgres.EventTeamGroupAllocation) eventLabModel.Group {
	return groupFromRow(row)
}
