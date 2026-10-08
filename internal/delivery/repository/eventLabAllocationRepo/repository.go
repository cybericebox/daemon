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
	return r.q.CreateEventGroupAllocation(ctx, postgres.CreateEventGroupAllocationParams{EventTeamID: g.TeamID, EventID: g.EventID, LabGroupName: g.Name, VpnCpuMillicores: g.Sizes.VPN.CPUMillicores, VpnMemoryBytes: g.Sizes.VPN.MemoryBytes, GatewayCpuMillicores: g.Sizes.Gateway.CPUMillicores, GatewayMemoryBytes: g.Sizes.Gateway.MemoryBytes, Plan: plan, CreatedAt: g.CreatedAt})
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
