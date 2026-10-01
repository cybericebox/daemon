package eventLabObservationRepo

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

type duplicateQueries struct {
	created          postgres.CreateEventLabObservationParams
	existing         postgres.EventLabObservation
	createdCapacity  postgres.CreatePlatformLabCapacityObservationParams
	existingCapacity postgres.PlatformLabCapacityObservation
}

func (q *duplicateQueries) FindActiveEventTeamsByLabGroup(context.Context, postgres.FindActiveEventTeamsByLabGroupParams) ([]postgres.FindActiveEventTeamsByLabGroupRow, error) {
	return nil, nil
}
func (q *duplicateQueries) CreateEventLabObservation(_ context.Context, arg postgres.CreateEventLabObservationParams) (postgres.EventLabObservation, error) {
	q.created = arg
	return postgres.EventLabObservation{}, pgx.ErrNoRows
}
func (q *duplicateQueries) GetEventLabObservationByAgentSequence(context.Context, postgres.GetEventLabObservationByAgentSequenceParams) (postgres.EventLabObservation, error) {
	return q.existing, nil
}
func (q *duplicateQueries) GetEventLabObservationCursor(context.Context, postgres.GetEventLabObservationCursorParams) (postgres.GetEventLabObservationCursorRow, error) {
	return postgres.GetEventLabObservationCursorRow{}, nil
}
func (q *duplicateQueries) GetPlatformLabObservationCursor(context.Context, uuid.UUID) (postgres.GetPlatformLabObservationCursorRow, error) {
	return postgres.GetPlatformLabObservationCursorRow{}, nil
}
func (q *duplicateQueries) ListEventLabObservations(context.Context, postgres.ListEventLabObservationsParams) ([]postgres.EventLabObservation, error) {
	return nil, nil
}
func (q *duplicateQueries) ListLatestEventLabObservations(context.Context, uuid.UUID) ([]postgres.EventLabObservation, error) {
	return nil, nil
}
func (q *duplicateQueries) ListPlatformLabObservations(context.Context, postgres.ListPlatformLabObservationsParams) ([]postgres.ListPlatformLabObservationsRow, error) {
	return nil, nil
}
func (q *duplicateQueries) FindEventTeamsByLabGroup(context.Context, string) ([]postgres.FindEventTeamsByLabGroupRow, error) {
	return nil, nil
}
func (q *duplicateQueries) GetLabMonitoringCurrent(context.Context, postgres.GetLabMonitoringCurrentParams) (postgres.LabMonitoringCurrent, error) {
	return postgres.LabMonitoringCurrent{}, pgx.ErrNoRows
}
func (q *duplicateQueries) UpsertLabMonitoringCurrent(context.Context, postgres.UpsertLabMonitoringCurrentParams) error {
	return nil
}
func (q *duplicateQueries) DeleteStaleLabMonitoringCurrent(context.Context, postgres.DeleteStaleLabMonitoringCurrentParams) error {
	return nil
}
func (q *duplicateQueries) ListPlatformLabMonitoringCurrent(context.Context, postgres.ListPlatformLabMonitoringCurrentParams) ([]postgres.ListPlatformLabMonitoringCurrentRow, error) {
	return nil, nil
}
func (q *duplicateQueries) ListEventLabMonitoringCurrent(context.Context, uuid.UUID) ([]postgres.ListEventLabMonitoringCurrentRow, error) {
	return nil, nil
}
func (q *duplicateQueries) CreatePlatformLabCapacityObservation(_ context.Context, arg postgres.CreatePlatformLabCapacityObservationParams) (postgres.PlatformLabCapacityObservation, error) {
	q.createdCapacity = arg
	return postgres.PlatformLabCapacityObservation{}, pgx.ErrNoRows
}
func (q *duplicateQueries) GetPlatformLabCapacityObservationByAgentSequence(context.Context, postgres.GetPlatformLabCapacityObservationByAgentSequenceParams) (postgres.PlatformLabCapacityObservation, error) {
	return q.existingCapacity, nil
}
func (q *duplicateQueries) GetPlatformLabCapacityObservationCursor(context.Context, uuid.UUID) (postgres.GetPlatformLabCapacityObservationCursorRow, error) {
	return postgres.GetPlatformLabCapacityObservationCursorRow{}, nil
}
func (q *duplicateQueries) ListLatestPlatformLabCapacityObservations(context.Context) ([]postgres.PlatformLabCapacityObservation, error) {
	return nil, nil
}
func (q *duplicateQueries) ListPlatformLabCapacityObservations(context.Context, postgres.ListPlatformLabCapacityObservationsParams) ([]postgres.PlatformLabCapacityObservation, error) {
	return nil, nil
}

func TestAppendReturnsExistingObservationForDuplicateAgentSequence(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	queries := &duplicateQueries{existing: postgres.EventLabObservation{ID: id, AgentID: "agent-a", Sequence: 8, LabGroupName: "group-a", Payload: []byte(`{"safe":true}`)}}
	repo := New(queries)
	got, err := repo.Append(context.Background(), labMonitoringModel.Observation{ID: uuid.Must(uuid.NewV7()), EventID: uuid.Must(uuid.NewV7()), EventTeamID: uuid.Must(uuid.NewV7()), AgentID: "agent-a", Sequence: 8, LabGroupName: "group-a", ObservedAt: time.Now(), ReceivedAt: time.Now(), SchemaVersion: 1, Payload: []byte(`{"safe":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || queries.created.AgentID != "agent-a" || queries.created.Sequence != 8 {
		t.Fatalf("duplicate was not resolved as existing observation: got=%+v create=%+v", got, queries.created)
	}
}

func TestAppendCapacityReturnsExistingObservationForDuplicateAgentSequence(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	queries := &duplicateQueries{existingCapacity: postgres.PlatformLabCapacityObservation{ID: id, AgentID: "agent-a", Sequence: 8, Payload: []byte(`{"safe":true}`)}}
	repo := New(queries)
	got, err := repo.AppendCapacity(context.Background(), labMonitoringModel.CapacityObservation{ID: uuid.Must(uuid.NewV7()), AgentID: "agent-a", Sequence: 8, ObservedAt: time.Now(), ReceivedAt: time.Now(), SchemaVersion: 1, Payload: []byte(`{"safe":true}`)})
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != id || queries.createdCapacity.AgentID != "agent-a" || queries.createdCapacity.Sequence != 8 {
		t.Fatalf("duplicate was not resolved as existing capacity observation: got=%+v create=%+v", got, queries.createdCapacity)
	}
}

// currentQueries is an in-memory lab_monitoring_current with two teams bound to
// the same lab group.
type currentQueries struct {
	duplicateQueries
	teams []postgres.FindEventTeamsByLabGroupRow
	rows  map[uuid.UUID]postgres.LabMonitoringCurrent
}

func (q *currentQueries) FindEventTeamsByLabGroup(context.Context, string) ([]postgres.FindEventTeamsByLabGroupRow, error) {
	return q.teams, nil
}
func (q *currentQueries) GetLabMonitoringCurrent(_ context.Context, arg postgres.GetLabMonitoringCurrentParams) (postgres.LabMonitoringCurrent, error) {
	row, ok := q.rows[arg.EventTeamID]
	if !ok {
		return postgres.LabMonitoringCurrent{}, pgx.ErrNoRows
	}
	return row, nil
}
func (q *currentQueries) UpsertLabMonitoringCurrent(_ context.Context, arg postgres.UpsertLabMonitoringCurrentParams) error {
	q.rows[arg.EventTeamID] = postgres.LabMonitoringCurrent{EventID: arg.EventID, EventTeamID: arg.EventTeamID, LabGroupName: arg.LabGroupName, AgentID: arg.AgentID, Sequence: arg.Sequence, Payload: arg.Payload}
	return nil
}

func TestApplyCurrentBuildsTheFullStateFromSnapshotPlusDeltas(t *testing.T) {
	teamA, teamB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	eventID := uuid.Must(uuid.NewV7())
	queries := &currentQueries{
		teams: []postgres.FindEventTeamsByLabGroupRow{{EventID: eventID, EventTeamID: teamA}, {EventID: eventID, EventTeamID: teamB}},
		rows:  map[uuid.UUID]postgres.LabMonitoringCurrent{},
	}
	repo := New(queries)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	if err := repo.ApplyCurrent(ctx, "group-a", "agent-a", 1, now, now, true, []byte(`{"groups":[{"name":"group-a"}],"labs":[{"namespace":"ns","name":"web","status":{"phase":"Pending"}}]}`)); err != nil {
		t.Fatal(err)
	}
	if err := repo.ApplyCurrent(ctx, "group-a", "agent-a", 2, now, now, false, []byte(`{"labs":[{"namespace":"ns","name":"web","status":{"phase":"Ready"}}]}`)); err != nil {
		t.Fatal(err)
	}
	for _, team := range []uuid.UUID{teamA, teamB} {
		row := queries.rows[team]
		if row.Sequence != 2 {
			t.Fatalf("team %s sequence = %d, want 2", team, row.Sequence)
		}
		got := string(row.Payload)
		if !strings.Contains(got, `"group-a"`) || !strings.Contains(got, `"Ready"`) || strings.Contains(got, `"Pending"`) {
			t.Fatalf("team %s state is not snapshot+delta merged: %s", team, got)
		}
	}
}
