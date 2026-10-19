// Package clusterReservationRepo persists the cluster reservation parts (one row per part: the desired state written
// whole by SavePart, plus two deliberate narrow writes, MarkSynced for the agent's answer and Observe for the status
// the agent reports, so neither can undo a newer desired change) and the agent of each unit of a calendar reservation.
package clusterReservationRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

type Queries interface {
	SaveClusterReservation(context.Context, postgres.SaveClusterReservationParams) error
	MarkClusterReservationSynced(context.Context, postgres.MarkClusterReservationSyncedParams) error
	ObserveClusterReservation(context.Context, postgres.ObserveClusterReservationParams) error
	EndClusterReservation(context.Context, postgres.EndClusterReservationParams) (int64, error)
	DeleteClusterReservation(context.Context, uuid.UUID) error
	GetClusterReservation(context.Context, uuid.UUID) (postgres.ClusterReservation, error)
	ListClusterReservationsOfAgent(context.Context, uuid.UUID) ([]postgres.ClusterReservation, error)
	ListClusterReservationsOfCalendar(context.Context, uuid.NullUUID) ([]postgres.ClusterReservation, error)
	ListClusterReservationsOfCalendars(context.Context, []uuid.UUID) ([]postgres.ClusterReservation, error)
	GetClusterReservationOfTestRun(context.Context, uuid.NullUUID) (postgres.ClusterReservation, error)
	ListClusterReservationsOfTestRuns(context.Context, postgres.ListClusterReservationsOfTestRunsParams) ([]postgres.ClusterReservation, error)
	GetClusterReservationOfTestGroup(context.Context, uuid.NullUUID) (postgres.ClusterReservation, error)
	ListClusterReservationAgents(context.Context) ([]uuid.UUID, error)
	ListUnsyncedClusterReservationAgents(context.Context) ([]uuid.UUID, error)
	ListClusterReservationUnits(context.Context, uuid.UUID) ([]postgres.ClusterReservationUnit, error)
	AssignClusterReservationUnit(context.Context, postgres.AssignClusterReservationUnitParams) (uuid.UUID, error)
	ReleaseClusterReservationUnit(context.Context, postgres.ReleaseClusterReservationUnitParams) error
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func timePtr(v pgtype.Timestamptz) *time.Time {
	if !v.Valid {
		return nil
	}
	t := v.Time.UTC()
	return &t
}

func tstz(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func uuidPtr(v uuid.NullUUID) *uuid.UUID {
	if !v.Valid {
		return nil
	}
	id := v.UUID
	return &id
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil || *id == uuid.Nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func partFrom(row postgres.ClusterReservation) calModel.Part {
	return calModel.Part{
		ID: row.ID, AgentID: row.AgentID, Source: calModel.PartSource(row.Source),
		CalendarID: uuidPtr(row.CalendarReservationID), TestDeployID: uuidPtr(row.TestDeployID), OwnerID: uuidPtr(row.OwnerID),
		Window:     calModel.Window{Start: row.StartsAt.UTC(), End: row.EndsAt.UTC()},
		Amount:     calModel.Amount{CPUMillicores: row.CpuMillicores, MemoryBytes: row.MemoryBytes},
		DesiredRev: row.DesiredRev, SyncedRev: row.SyncedRev, SyncedAt: timePtr(row.SyncedAt), SyncError: row.SyncError,
		SyncReason: row.SyncReason, SyncFailedSince: timePtr(row.SyncFailedSince), ShrinkHeld: row.ShrinkHeld, Forced: row.Forced, RetireHeld: row.RetireHeld,
		Status: calModel.PartStatus{
			Phase: row.Phase, NoticeAt: timePtr(row.NoticeAt), EndsAt: timePtr(row.EndsNoticeAt), EndingReason: row.EndingReason,
			Used:   calModel.Amount{CPUMillicores: row.UsedCpuMillicores, MemoryBytes: row.UsedMemoryBytes},
			Groups: int(row.GroupCount), Exceeded: row.Exceeded, ObservedAt: timePtr(row.ObservedAt),
		},
		RetireAt: timePtr(row.RetireAt), CreatedAt: row.CreatedAt.UTC(), UpdatedAt: row.UpdatedAt.UTC(),
	}
}

func partsFrom(rows []postgres.ClusterReservation) []calModel.Part {
	out := make([]calModel.Part, 0, len(rows))
	for _, row := range rows {
		out = append(out, partFrom(row))
	}
	return out
}

// SavePart writes the desired state of a part (insert or update); the agent's answer and the observed status are
// left as they are.
func (r *Repository) SavePart(ctx context.Context, p calModel.Part) error {
	return r.q.SaveClusterReservation(ctx, postgres.SaveClusterReservationParams{
		ID: p.ID, AgentID: p.AgentID, Source: string(p.Source),
		CalendarReservationID: nullUUID(p.CalendarID), TestDeployID: nullUUID(p.TestDeployID), OwnerID: nullUUID(p.OwnerID),
		StartsAt: p.Window.Start, EndsAt: p.Window.End, CpuMillicores: p.Amount.CPUMillicores, MemoryBytes: p.Amount.MemoryBytes,
		DesiredRev: p.DesiredRev, SyncedRev: p.SyncedRev, SyncedAt: tstz(p.SyncedAt), SyncError: p.SyncError, Forced: p.Forced, RetireHeld: p.RetireHeld,
		RetireAt: tstz(p.RetireAt), CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	})
}

// MarkSynced records the agent's answer for a revision (narrow: it never lowers the confirmed revision).
func (r *Repository) MarkSynced(ctx context.Context, id uuid.UUID, a calModel.SyncAnswer) error {
	return r.q.MarkClusterReservationSynced(ctx, postgres.MarkClusterReservationSyncedParams{
		ID: id, Ok: a.OK, SyncedRev: a.Rev, At: a.At, SyncError: a.Error, SyncReason: a.Reason, ShrinkHeld: a.ShrinkHeld,
	})
}

// Observe stores what the agent reported about a part (narrow).
func (r *Repository) Observe(ctx context.Context, id uuid.UUID, s calModel.PartStatus) error {
	return r.q.ObserveClusterReservation(ctx, postgres.ObserveClusterReservationParams{
		ID: id, Phase: s.Phase, NoticeAt: tstz(s.NoticeAt), EndsNoticeAt: tstz(s.EndsAt), EndingReason: s.EndingReason,
		UsedCpuMillicores: s.Used.CPUMillicores, UsedMemoryBytes: s.Used.MemoryBytes, GroupCount: int32(s.Groups),
		Exceeded: s.Exceeded, ObservedAt: tstz(s.ObservedAt),
	})
}

// EndPart records the end of a part and reports whether this call ended it (false: it had ended already).
func (r *Repository) EndPart(ctx context.Context, id uuid.UUID, s calModel.PartStatus) (bool, error) {
	n, err := r.q.EndClusterReservation(ctx, postgres.EndClusterReservationParams{
		ID: id, NoticeAt: tstz(s.NoticeAt), EndsNoticeAt: tstz(s.EndsAt), EndingReason: s.EndingReason,
		UsedCpuMillicores: s.Used.CPUMillicores, UsedMemoryBytes: s.Used.MemoryBytes, GroupCount: int32(s.Groups),
		Exceeded: s.Exceeded, ObservedAt: tstz(s.ObservedAt),
	})
	return n == 1, err
}

// DeletePart drops a part the agent confirmed gone.
func (r *Repository) DeletePart(ctx context.Context, id uuid.UUID) error {
	return r.q.DeleteClusterReservation(ctx, id)
}

func (r *Repository) GetPart(ctx context.Context, id uuid.UUID) (calModel.Part, error) {
	row, err := r.q.GetClusterReservation(ctx, id)
	if err != nil {
		return calModel.Part{}, err
	}
	return partFrom(row), nil
}

// PartsOfAgent lists every part of one agent, retired ones included.
func (r *Repository) PartsOfAgent(ctx context.Context, agent uuid.UUID) ([]calModel.Part, error) {
	rows, err := r.q.ListClusterReservationsOfAgent(ctx, agent)
	if err != nil {
		return nil, err
	}
	return partsFrom(rows), nil
}

// PartsOfCalendar lists the live parts of a calendar reservation.
func (r *Repository) PartsOfCalendar(ctx context.Context, calendarID uuid.UUID) ([]calModel.Part, error) {
	rows, err := r.q.ListClusterReservationsOfCalendar(ctx, nullUUID(&calendarID))
	if err != nil {
		return nil, err
	}
	return partsFrom(rows), nil
}

// PartsOfCalendars lists the parts of several calendar reservations, retired ones included.
func (r *Repository) PartsOfCalendars(ctx context.Context, ids []uuid.UUID) ([]calModel.Part, error) {
	rows, err := r.q.ListClusterReservationsOfCalendars(ctx, ids)
	if err != nil {
		return nil, err
	}
	return partsFrom(rows), nil
}

// RunPart is the live part of a test run.
func (r *Repository) RunPart(ctx context.Context, deployID uuid.UUID) (calModel.Part, error) {
	row, err := r.q.GetClusterReservationOfTestRun(ctx, nullUUID(&deployID))
	if err != nil {
		return calModel.Part{}, err
	}
	return partFrom(row), nil
}

// RunPartsOf lists the live test-run parts of an author on one agent.
func (r *Repository) RunPartsOf(ctx context.Context, owner, agent uuid.UUID) ([]calModel.Part, error) {
	rows, err := r.q.ListClusterReservationsOfTestRuns(ctx, postgres.ListClusterReservationsOfTestRunsParams{AgentID: agent, OwnerID: nullUUID(&owner)})
	if err != nil {
		return nil, err
	}
	return partsFrom(rows), nil
}

// GroupPart is the live group part of an author.
func (r *Repository) GroupPart(ctx context.Context, owner uuid.UUID) (calModel.Part, error) {
	row, err := r.q.GetClusterReservationOfTestGroup(ctx, nullUUID(&owner))
	if err != nil {
		return calModel.Part{}, err
	}
	return partFrom(row), nil
}

// PartAgents lists every agent that holds a part.
func (r *Repository) PartAgents(ctx context.Context) ([]uuid.UUID, error) {
	return r.q.ListClusterReservationAgents(ctx)
}

// UnsyncedAgents lists the agents with a part to send.
func (r *Repository) UnsyncedAgents(ctx context.Context) ([]uuid.UUID, error) {
	return r.q.ListUnsyncedClusterReservationAgents(ctx)
}

// Units maps the placed units of a calendar reservation to their agents.
func (r *Repository) Units(ctx context.Context, calendarID uuid.UUID) (map[string]uuid.UUID, error) {
	rows, err := r.q.ListClusterReservationUnits(ctx, calendarID)
	if err != nil {
		return nil, err
	}
	out := make(map[string]uuid.UUID, len(rows))
	for _, row := range rows {
		out[row.UnitKey] = row.AgentID
	}
	return out, nil
}

// AssignUnit places a unit on an agent unless it is placed already; it returns the agent that holds it.
func (r *Repository) AssignUnit(ctx context.Context, calendarID uuid.UUID, unit string, agent uuid.UUID, at time.Time) (uuid.UUID, error) {
	return r.q.AssignClusterReservationUnit(ctx, postgres.AssignClusterReservationUnitParams{
		CalendarReservationID: calendarID, UnitKey: unit, AgentID: agent, AssignedAt: at,
	})
}

// ReleaseUnit forgets the agent of a unit whose group is gone.
func (r *Repository) ReleaseUnit(ctx context.Context, calendarID uuid.UUID, unit string) error {
	return r.q.ReleaseClusterReservationUnit(ctx, postgres.ReleaseClusterReservationUnitParams{CalendarReservationID: calendarID, UnitKey: unit})
}
