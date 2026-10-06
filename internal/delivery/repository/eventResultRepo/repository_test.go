package eventResultRepo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type fakeQueries struct {
	rows        []postgres.EventResultChange
	advanced    postgres.AdvanceEventResultRevisionRow
	created     postgres.EventResultChange
	advanceArgs postgres.AdvanceEventResultRevisionParams
	createArgs  postgres.CreateEventResultChangeParams
	current     postgres.GetEventResultRevisionRow
	currentErr  error
	earliest    int64
	deletedAt   time.Time
}

func (f fakeQueries) ListEventResultChangesAfter(context.Context, postgres.ListEventResultChangesAfterParams) ([]postgres.EventResultChange, error) {
	return f.rows, nil
}

func (f *fakeQueries) AdvanceEventResultRevision(_ context.Context, arg postgres.AdvanceEventResultRevisionParams) (postgres.AdvanceEventResultRevisionRow, error) {
	f.advanceArgs = arg
	return f.advanced, nil
}

func (f *fakeQueries) CreateEventResultChange(_ context.Context, arg postgres.CreateEventResultChangeParams) (postgres.EventResultChange, error) {
	f.createArgs = arg
	return f.created, nil
}

func (f *fakeQueries) GetEventResultRevision(context.Context, uuid.UUID) (postgres.GetEventResultRevisionRow, error) {
	return f.current, f.currentErr
}

func (f *fakeQueries) GetEarliestEventResultChangeRevision(context.Context, uuid.UUID) (int64, error) {
	return f.earliest, nil
}

func (f *fakeQueries) DeleteEventResultChangesBefore(_ context.Context, before time.Time) (int64, error) {
	f.deletedAt = before
	return 2, nil
}

func TestListChangesAfterMapsRowsInRevisionOrder(t *testing.T) {
	eventID := uuid.Must(uuid.NewV4())
	now := time.Now().UTC()
	payload := json.RawMessage(`{"TeamID":"team-1"}`)
	repo := New(&fakeQueries{rows: []postgres.EventResultChange{{
		EventID: eventID, Revision: 4, Kind: string(ChangeTeamChallengeSolved), Payload: payload, CreatedAt: now,
	}}})

	got, err := repo.ListChangesAfter(context.Background(), eventID, 3, 100)
	if err != nil {
		t.Fatalf("ListChangesAfter: %v", err)
	}
	if len(got) != 1 || got[0].Revision != 4 || got[0].Kind != ChangeTeamChallengeSolved {
		t.Fatalf("got %#v", got)
	}
	if string(got[0].Payload) != string(payload) {
		t.Fatalf("payload = %s, want %s", got[0].Payload, payload)
	}
}

func TestAdvanceUsesNewRevisionForChange(t *testing.T) {
	eventID := uuid.Must(uuid.NewV4())
	now := time.Now().UTC()
	fake := &fakeQueries{
		advanced: postgres.AdvanceEventResultRevisionRow{Revision: 5, UpdatedAt: now},
		created:  postgres.EventResultChange{EventID: eventID, Revision: 5, Kind: string(ChangeTeamChallengeSolved), Payload: []byte(`{"TeamID":"team-1"}`), CreatedAt: now},
	}

	got, err := New(fake).Advance(context.Background(), eventID, Change{Kind: ChangeTeamChallengeSolved, Payload: json.RawMessage(`{"TeamID":"team-1"}`), CreatedAt: now})
	if err != nil {
		t.Fatalf("Advance: %v", err)
	}
	if fake.createArgs.Revision != 5 || got.Revision != 5 {
		t.Fatalf("create revision = %d, result = %#v", fake.createArgs.Revision, got)
	}
}

func TestCurrentRevisionAndDeleteBeforeMapRepositoryOperations(t *testing.T) {
	eventID := uuid.Must(uuid.NewV4())
	now := time.Now().UTC()
	fake := &fakeQueries{current: postgres.GetEventResultRevisionRow{Revision: 7, UpdatedAt: now}}
	repo := New(fake)

	current, err := repo.CurrentRevision(context.Background(), eventID)
	if err != nil || current.Revision != 7 || !current.UpdatedAt.Equal(now) {
		t.Fatalf("CurrentRevision = %#v, %v", current, err)
	}
	deleted, err := repo.DeleteChangesBefore(context.Background(), now)
	if err != nil || deleted != 2 || !fake.deletedAt.Equal(now) {
		t.Fatalf("DeleteChangesBefore = %d, %v; cutoff %s", deleted, err, fake.deletedAt)
	}
}

func TestCurrentRevisionReturnsZeroWhenEventHasNoChanges(t *testing.T) {
	repo := New(&fakeQueries{currentErr: pgx.ErrNoRows})
	got, err := repo.CurrentRevision(context.Background(), uuid.Must(uuid.NewV4()))
	if err != nil || got.Revision != 0 || !got.UpdatedAt.IsZero() {
		t.Fatalf("CurrentRevision = %#v, %v", got, err)
	}
}

func TestEarliestChangeRevisionMapsQueryValue(t *testing.T) {
	repo := New(&fakeQueries{earliest: 4})
	got, err := repo.EarliestChangeRevision(context.Background(), uuid.Must(uuid.NewV4()))
	if err != nil || got != 4 {
		t.Fatalf("EarliestChangeRevision = %d, %v", got, err)
	}
}
