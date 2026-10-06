package eventLabObservationRepo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

type Queries interface {
	FindActiveEventTeamsByLabGroup(context.Context, postgres.FindActiveEventTeamsByLabGroupParams) ([]postgres.FindActiveEventTeamsByLabGroupRow, error)
	FindEventTeamsByLabGroup(context.Context, string) ([]postgres.FindEventTeamsByLabGroupRow, error)
	GetLabMonitoringCurrent(context.Context, postgres.GetLabMonitoringCurrentParams) (postgres.LabMonitoringCurrent, error)
	UpsertLabMonitoringCurrent(context.Context, postgres.UpsertLabMonitoringCurrentParams) error
	DeleteStaleLabMonitoringCurrent(context.Context, postgres.DeleteStaleLabMonitoringCurrentParams) error
	ListPlatformLabMonitoringCurrent(context.Context, postgres.ListPlatformLabMonitoringCurrentParams) ([]postgres.ListPlatformLabMonitoringCurrentRow, error)
	ListEventLabMonitoringCurrent(context.Context, uuid.UUID) ([]postgres.ListEventLabMonitoringCurrentRow, error)
	CreateEventLabObservation(context.Context, postgres.CreateEventLabObservationParams) (postgres.EventLabObservation, error)
	GetEventLabObservationByAgentSequence(context.Context, postgres.GetEventLabObservationByAgentSequenceParams) (postgres.EventLabObservation, error)
	GetEventLabObservationCursor(context.Context, postgres.GetEventLabObservationCursorParams) (postgres.GetEventLabObservationCursorRow, error)
	GetPlatformLabObservationCursor(context.Context, uuid.UUID) (postgres.GetPlatformLabObservationCursorRow, error)
	ListEventLabObservations(context.Context, postgres.ListEventLabObservationsParams) ([]postgres.EventLabObservation, error)
	ListLatestEventLabObservations(context.Context, uuid.UUID) ([]postgres.EventLabObservation, error)
	ListPlatformLabObservations(context.Context, postgres.ListPlatformLabObservationsParams) ([]postgres.ListPlatformLabObservationsRow, error)
	CreatePlatformLabCapacityObservation(context.Context, postgres.CreatePlatformLabCapacityObservationParams) (postgres.PlatformLabCapacityObservation, error)
	GetPlatformLabCapacityObservationByAgentSequence(context.Context, postgres.GetPlatformLabCapacityObservationByAgentSequenceParams) (postgres.PlatformLabCapacityObservation, error)
	GetPlatformLabCapacityObservationCursor(context.Context, uuid.UUID) (postgres.GetPlatformLabCapacityObservationCursorRow, error)
	ListLatestPlatformLabCapacityObservations(context.Context) ([]postgres.PlatformLabCapacityObservation, error)
	ListPlatformLabCapacityObservations(context.Context, postgres.ListPlatformLabCapacityObservationsParams) ([]postgres.PlatformLabCapacityObservation, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) ActiveTeams(ctx context.Context, labGroupName string, observedAt time.Time) ([]labMonitoringModel.EventTeam, error) {
	rows, err := r.q.FindActiveEventTeamsByLabGroup(ctx, postgres.FindActiveEventTeamsByLabGroupParams{LabGroupName: labGroupName, ObservedAt: observedAt})
	if err != nil {
		return nil, err
	}
	out := make([]labMonitoringModel.EventTeam, 0, len(rows))
	for _, row := range rows {
		out = append(out, labMonitoringModel.EventTeam{EventID: row.EventID, EventTeamID: row.EventTeamID})
	}
	return out, nil
}

// Append returns the existing immutable observation when an agent reconnects
// and repeats a sequence/group pair.
func (r *Repository) Append(ctx context.Context, observation labMonitoringModel.Observation) (labMonitoringModel.Observation, error) {
	row, err := r.q.CreateEventLabObservation(ctx, postgres.CreateEventLabObservationParams{
		ID: observation.ID, EventID: observation.EventID, EventTeamID: observation.EventTeamID,
		LabGroupName: observation.LabGroupName, AgentID: observation.AgentID, Sequence: observation.Sequence,
		ObservedAt: observation.ObservedAt, ReceivedAt: observation.ReceivedAt, SchemaVersion: observation.SchemaVersion,
		Snapshot: observation.Snapshot, Payload: observation.Payload,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		row, err = r.q.GetEventLabObservationByAgentSequence(ctx, postgres.GetEventLabObservationByAgentSequenceParams{AgentID: observation.AgentID, Sequence: observation.Sequence, LabGroupName: observation.LabGroupName})
	}
	if err != nil {
		return labMonitoringModel.Observation{}, err
	}
	return toDomain(row), nil
}

// AppendCapacity returns the existing immutable sample when an agent repeats a
// stream sequence after reconnecting.
func (r *Repository) AppendCapacity(ctx context.Context, observation labMonitoringModel.CapacityObservation) (labMonitoringModel.CapacityObservation, error) {
	row, err := r.q.CreatePlatformLabCapacityObservation(ctx, postgres.CreatePlatformLabCapacityObservationParams{
		ID: observation.ID, AgentID: observation.AgentID, Sequence: observation.Sequence,
		ObservedAt: observation.ObservedAt, ReceivedAt: observation.ReceivedAt,
		SchemaVersion: observation.SchemaVersion, Snapshot: observation.Snapshot, Payload: observation.Payload,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		row, err = r.q.GetPlatformLabCapacityObservationByAgentSequence(ctx, postgres.GetPlatformLabCapacityObservationByAgentSequenceParams{AgentID: observation.AgentID, Sequence: observation.Sequence})
	}
	if err != nil {
		return labMonitoringModel.CapacityObservation{}, err
	}
	return capacityToDomain(row), nil
}

func (r *Repository) Latest(ctx context.Context, eventID uuid.UUID) ([]labMonitoringModel.Observation, error) {
	rows, err := r.q.ListLatestEventLabObservations(ctx, eventID)
	if err != nil {
		return nil, err
	}
	return toDomainList(rows), nil
}

func (r *Repository) List(ctx context.Context, eventID uuid.UUID, eventTeamID uuid.NullUUID, from, to, cursorAt time.Time, cursorID uuid.UUID, limit int32) ([]labMonitoringModel.Observation, error) {
	if cursorID != maxObservationCursorID {
		cursor, err := r.q.GetEventLabObservationCursor(ctx, postgres.GetEventLabObservationCursorParams{EventID: eventID, ID: cursorID})
		if err != nil {
			return nil, err
		}
		cursorAt = cursor.ObservedAt
	}
	rows, err := r.q.ListEventLabObservations(ctx, postgres.ListEventLabObservationsParams{EventID: eventID, EventTeamID: eventTeamID, FromAt: from, ToAt: to, CursorAt: cursorAt, CursorID: cursorID, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	return toDomainList(rows), nil
}

// ApplyCurrent folds one per-group update into the merged current state of
// every event team bound to the group, whatever the event lifecycle: the state
// must already be complete when an event becomes active. Snapshots replace,
// deltas merge; updatedAt stamps every touched row so PruneCurrent can drop
// what a later snapshot no longer reports.
func (r *Repository) ApplyCurrent(ctx context.Context, labGroupName, agentID string, sequence int64, observedAt, updatedAt time.Time, snapshot bool, payload json.RawMessage) error {
	teams, err := r.q.FindEventTeamsByLabGroup(ctx, labGroupName)
	if err != nil {
		return err
	}
	for _, team := range teams {
		var current json.RawMessage
		if !snapshot {
			row, getErr := r.q.GetLabMonitoringCurrent(ctx, postgres.GetLabMonitoringCurrentParams{EventID: team.EventID, EventTeamID: team.EventTeamID, LabGroupName: labGroupName})
			if getErr != nil && !errors.Is(getErr, pgx.ErrNoRows) {
				return getErr
			}
			current = row.Payload
		}
		merged, mergeErr := labMonitoringModel.Merge(current, payload, snapshot)
		if mergeErr != nil {
			return mergeErr
		}
		if err = r.q.UpsertLabMonitoringCurrent(ctx, postgres.UpsertLabMonitoringCurrentParams{
			EventID: team.EventID, EventTeamID: team.EventTeamID, LabGroupName: labGroupName, AgentID: agentID,
			Sequence: sequence, ObservedAt: observedAt, UpdatedAt: updatedAt, Payload: merged,
		}); err != nil {
			return err
		}
	}
	return nil
}

// PruneCurrent removes the agent's groups that a snapshot taken at `before` did
// not refresh: a snapshot is the agent's full picture.
func (r *Repository) PruneCurrent(ctx context.Context, agentID string, before time.Time) error {
	return r.q.DeleteStaleLabMonitoringCurrent(ctx, postgres.DeleteStaleLabMonitoringCurrentParams{AgentID: agentID, Before: before})
}

// CurrentPlatform returns the merged current state of every lab group of an
// active event; includeRecent additionally lists groups updated since
// recentSince whatever the event state.
func (r *Repository) CurrentPlatform(ctx context.Context, includeRecent bool, recentSince, now time.Time) ([]labMonitoringModel.Current, error) {
	rows, err := r.q.ListPlatformLabMonitoringCurrent(ctx, postgres.ListPlatformLabMonitoringCurrentParams{IncludeRecent: includeRecent, RecentSince: recentSince, Now: now})
	if err != nil {
		return nil, err
	}
	out := make([]labMonitoringModel.Current, 0, len(rows))
	for _, row := range rows {
		out = append(out, labMonitoringModel.Current{
			EventID: row.EventID, EventName: row.EventName, EventTeamID: row.EventTeamID, TeamName: row.TeamName, Moderators: row.Moderators,
			LabGroupName: row.LabGroupName, AgentID: row.AgentID, Sequence: row.Sequence, ObservedAt: row.ObservedAt, UpdatedAt: row.UpdatedAt, Payload: row.Payload,
		})
	}
	return out, nil
}

// CurrentForEvent returns the merged current state of every lab group of one event (the team, the
// group and the payload only), for the organizers' stand list.
func (r *Repository) CurrentForEvent(ctx context.Context, eventID uuid.UUID) ([]labMonitoringModel.Current, error) {
	rows, err := r.q.ListEventLabMonitoringCurrent(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]labMonitoringModel.Current, 0, len(rows))
	for _, row := range rows {
		out = append(out, labMonitoringModel.Current{EventID: eventID, EventTeamID: row.EventTeamID, LabGroupName: row.LabGroupName, Payload: row.Payload})
	}
	return out, nil
}

func (r *Repository) ListPlatform(ctx context.Context, eventID, eventTeamID uuid.NullUUID, from, to, cursorAt time.Time, cursorID uuid.UUID, limit int32) ([]labMonitoringModel.NamedObservation, error) {
	if cursorID != maxObservationCursorID {
		cursor, err := r.q.GetPlatformLabObservationCursor(ctx, cursorID)
		if err != nil {
			return nil, err
		}
		cursorAt = cursor.ObservedAt
	}
	rows, err := r.q.ListPlatformLabObservations(ctx, postgres.ListPlatformLabObservationsParams{EventID: eventID, EventTeamID: eventTeamID, FromAt: from, ToAt: to, CursorAt: cursorAt, CursorID: cursorID, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	out := make([]labMonitoringModel.NamedObservation, 0, len(rows))
	for _, row := range rows {
		out = append(out, labMonitoringModel.NamedObservation{
			Observation: toDomain(postgres.EventLabObservation{ID: row.ID, EventID: row.EventID, EventTeamID: row.EventTeamID, LabGroupName: row.LabGroupName, AgentID: row.AgentID, Sequence: row.Sequence, ObservedAt: row.ObservedAt, ReceivedAt: row.ReceivedAt, SchemaVersion: row.SchemaVersion, Snapshot: row.Snapshot, Payload: row.Payload}),
			EventName:   row.EventName, TeamName: row.TeamName, Moderators: row.Moderators,
		})
	}
	return out, nil
}

func (r *Repository) LatestCapacity(ctx context.Context) ([]labMonitoringModel.CapacityObservation, error) {
	rows, err := r.q.ListLatestPlatformLabCapacityObservations(ctx)
	if err != nil {
		return nil, err
	}
	return capacityToDomainList(rows), nil
}

func (r *Repository) ListCapacity(ctx context.Context, from, to, cursorAt time.Time, cursorID uuid.UUID, limit int32) ([]labMonitoringModel.CapacityObservation, error) {
	if cursorID != maxObservationCursorID {
		cursor, err := r.q.GetPlatformLabCapacityObservationCursor(ctx, cursorID)
		if err != nil {
			return nil, err
		}
		cursorAt = cursor.ObservedAt
	}
	rows, err := r.q.ListPlatformLabCapacityObservations(ctx, postgres.ListPlatformLabCapacityObservationsParams{FromAt: from, ToAt: to, CursorAt: cursorAt, CursorID: cursorID, LimitVal: limit})
	if err != nil {
		return nil, err
	}
	return capacityToDomainList(rows), nil
}

var maxObservationCursorID = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))

func toDomain(row postgres.EventLabObservation) labMonitoringModel.Observation {
	return labMonitoringModel.Observation{ID: row.ID, EventID: row.EventID, EventTeamID: row.EventTeamID, LabGroupName: row.LabGroupName, AgentID: row.AgentID, Sequence: row.Sequence, ObservedAt: row.ObservedAt, ReceivedAt: row.ReceivedAt, SchemaVersion: row.SchemaVersion, Snapshot: row.Snapshot, Payload: row.Payload}
}

func toDomainList(rows []postgres.EventLabObservation) []labMonitoringModel.Observation {
	out := make([]labMonitoringModel.Observation, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out
}

func capacityToDomain(row postgres.PlatformLabCapacityObservation) labMonitoringModel.CapacityObservation {
	return labMonitoringModel.CapacityObservation{ID: row.ID, AgentID: row.AgentID, Sequence: row.Sequence, ObservedAt: row.ObservedAt, ReceivedAt: row.ReceivedAt, SchemaVersion: row.SchemaVersion, Snapshot: row.Snapshot, Payload: row.Payload}
}

func capacityToDomainList(rows []postgres.PlatformLabCapacityObservation) []labMonitoringModel.CapacityObservation {
	out := make([]labMonitoringModel.CapacityObservation, 0, len(rows))
	for _, row := range rows {
		out = append(out, capacityToDomain(row))
	}
	return out
}
