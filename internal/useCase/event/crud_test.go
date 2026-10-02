package event_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestEventUseCaseExposesScoringPopulationStartReconcile(t *testing.T) {
	if _, ok := reflect.TypeOf(&event.EventUseCase{}).MethodByName("CaptureDueScoringPopulations"); !ok {
		t.Fatal("event runtime must reconcile scoring populations at start")
	}
}

func TestCaptureDueScoringPopulationsCapturesApprovedTeamCount(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventsDueForScoringPopulation(gomock.Any(), gomock.Any()).Return([]postgres.ListEventsDueForScoringPopulationRow{{
		EventID: eventID, Participation: pgtype.Int2{Int16: 1, Valid: true},
	}}, nil)
	q.EXPECT().CountApprovedEventTeams(gomock.Any(), eventID).Return(int64(7), nil)
	q.EXPECT().InsertEventScoringPopulation(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertEventScoringPopulationParams) (postgres.EventScoringPopulation, error) {
			if arg.EventID != eventID || arg.UnitsCount != 7 || arg.CapturedAt.IsZero() {
				t.Fatalf("unexpected snapshot params: %+v", arg)
			}
			return postgres.EventScoringPopulation{EventID: arg.EventID, UnitsCount: arg.UnitsCount, CapturedAt: arg.CapturedAt}, nil
		})
	if err := uc.CaptureDueScoringPopulations(context.Background()); err != nil {
		t.Fatalf("CaptureDueScoringPopulations: %v", err)
	}
	if !unit.saved || !unit.restored {
		t.Fatalf("reconcile must commit and restore its transaction: %+v", unit)
	}
}

func TestCaptureDueScoringPopulationsCountsPersonalTeams(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventsDueForScoringPopulation(gomock.Any(), gomock.Any()).Return([]postgres.ListEventsDueForScoringPopulationRow{{
		EventID: eventID, Participation: pgtype.Int2{Int16: 0, Valid: true},
	}}, nil)
	q.EXPECT().CountApprovedEventTeams(gomock.Any(), eventID).Return(int64(3), nil)
	q.EXPECT().InsertEventScoringPopulation(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.InsertEventScoringPopulationParams) (postgres.EventScoringPopulation, error) {
			if arg.UnitsCount != 3 {
				t.Fatalf("individual scoring population must count visible personal teams: %+v", arg)
			}
			return postgres.EventScoringPopulation{EventID: arg.EventID, UnitsCount: arg.UnitsCount, CapturedAt: arg.CapturedAt}, nil
		})
	if err := uc.CaptureDueScoringPopulations(context.Background()); err != nil {
		t.Fatalf("CaptureDueScoringPopulations: %v", err)
	}
}

// pgconnUniqueViolation simulates a unique-violation on events.tag, as
// Postgres would report it for a duplicate CreateEvent.
var pgconnUniqueViolation = pgconn.PgError{
	Code:   pgerrcode.UniqueViolation,
	Detail: `Key (tag)=(duptag) already exists.`,
}

func newUC(q event.IRepository) *event.EventUseCase {
	return event.NewEventUseCase(event.Dependencies{
		Repo:  q,
		UoW:   testUnitOfWorker{repo: q, unit: &testUoW{}},
		Media: newTemplateMediaFake(),
	})
}

// testUnitOfWorker returns the same mocked repository as a transaction-bound
// port. It keeps CreateEvent tests on the production atomic path.
type testUnitOfWorker struct {
	repo event.IRepository
	unit *testUoW
}

func (w testUnitOfWorker) UnitOfWork(ctx context.Context) (context.Context, event.IRepository, postgres.UoW, error) {
	if q, ok := w.repo.(*postgresMocks.MockQuerier); ok {
		q.EXPECT().GetLatestEventFormVersion(gomock.Any(), gomock.Any()).Return(postgres.EventFormVersion{}, pgx.ErrNoRows).AnyTimes()
	}
	return ctx, w.repo, w.unit, nil
}

type testUoW struct {
	saved    bool
	restored bool
}

func (u *testUoW) Save() error {
	u.saved = true
	return nil
}

func (u *testUoW) Restore() error {
	u.restored = true
	return nil
}

func TestCreateEvent_WritesRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q,
		UoW:  testUnitOfWorker{repo: q, unit: unit},
	})
	adminID := uuid.Must(uuid.NewV7())
	availableFrom := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	archiveAt := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)

	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventParams) (postgres.Event, error) {
			if arg.Tag != "myevent" || arg.Name != "My Event" {
				t.Fatalf("unexpected params: %+v", arg)
			}
			if !arg.AvailableFrom.Equal(availableFrom) || !arg.ArchiveAt.Valid || !arg.ArchiveAt.Time.Equal(archiveAt) {
				t.Fatalf("unexpected window: %+v", arg)
			}
			if arg.ID == uuid.Nil || arg.CreatedAt.IsZero() {
				t.Fatal("domain defaults must be set")
			}
			return postgres.Event{
				ID: arg.ID, Tag: arg.Tag, Name: arg.Name, InternalName: arg.InternalName,
				AvailableFrom: arg.AvailableFrom, ArchiveAt: arg.ArchiveAt,
				CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy,
				UpdatedAt: arg.UpdatedAt, UpdatedBy: arg.UpdatedBy,
			}, nil
		})
	// CreateEvent also creates the 1:1 config row (see config_test.go for the
	// dedicated coverage of its default values); this test only needs the
	// call satisfied so it doesn't fail as unexpected.
	q.EXPECT().CreateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventConfigParams) (postgres.EventConfig, error) {
			return postgres.EventConfig{EventID: arg.EventID, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().CreateEventManager(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventManagerParams) (postgres.EventManager, error) {
			if arg.EventID == uuid.Nil || arg.UserID != adminID || arg.Role != 0 || arg.CreatedAt.IsZero() {
				t.Fatalf("unexpected owner membership: %+v", arg)
			}
			return postgres.EventManager{EventID: arg.EventID, UserID: arg.UserID, Role: arg.Role, CreatedAt: arg.CreatedAt}, nil
		})

	v, err := uc.CreateEvent(context.Background(), event.CreateEventInput{
		Tag: "myevent", Name: "My Event", AvailableFrom: availableFrom, ArchiveAt: archiveAt, CreatedBy: adminID,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if v.Tag != "myevent" || v.Name != "My Event" {
		t.Fatalf("view mismatch: %+v", v)
	}
	if !unit.saved || !unit.restored {
		t.Fatalf("event creation must commit and close its transaction: %+v", unit)
	}
}

// Events inherit notification subscriptions and templates from the platform:
// creation must not copy anything (the strict mocks fail on any copy query).
func TestCreateEvent_DoesNotCopyNotificationDefaults(t *testing.T) {
	ctrl := gomock.NewController(t)
	outer := newFormGateMock(ctrl)
	tx := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: outer, UoW: testUnitOfWorker{repo: tx, unit: unit},
	})
	adminID := uuid.Must(uuid.NewV7())
	start := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)

	outer.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	tx.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventParams) (postgres.Event, error) {
			return postgres.Event{ID: arg.ID, Tag: arg.Tag, Name: arg.Name, AvailableFrom: arg.AvailableFrom,
				ArchiveAt: arg.ArchiveAt, CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy,
				UpdatedAt: arg.UpdatedAt, UpdatedBy: arg.UpdatedBy}, nil
		})
	tx.EXPECT().CreateEventConfig(gomock.Any(), gomock.Any()).Return(postgres.EventConfig{}, nil)
	tx.EXPECT().CreateEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, nil)

	_, err := uc.CreateEvent(context.Background(), event.CreateEventInput{
		Tag: "nocopy", Name: "No Copy", AvailableFrom: start,
		ArchiveAt: start.Add(24 * time.Hour), CreatedBy: adminID,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if !unit.saved {
		t.Fatal("event creation must commit")
	}
}

func TestCreateEvent_DuplicateTag_Returns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)

	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).
		Return(postgres.Event{}, &pgconnUniqueViolation)

	_, err := uc.CreateEvent(context.Background(), event.CreateEventInput{
		Tag: "duptag", Name: "n",
		AvailableFrom: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC),
		ArchiveAt:     time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, eventModel.ErrEventExists.Err()) {
		t.Fatalf("want ErrEventExists, got %v", err)
	}
}

func TestUpdateEvent_OptimisticLock(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())
	updatedBy := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	availableFrom := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	archiveAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	row := postgres.Event{
		ID: id, Tag: "oldtag", Name: "old name",
		AvailableFrom: availableFrom, ArchiveAt: pgtype.Timestamptz{Time: archiveAt, Valid: true},
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}

	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(row, nil)
	q.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventParams) (int64, error) {
			if !arg.ExpectedUpdatedAt.Time.Equal(t0) || !arg.ExpectedUpdatedAt.Valid {
				t.Fatalf("expected ExpectedUpdatedAt = %v, got %+v", t0, arg.ExpectedUpdatedAt)
			}
			return int64(1), nil
		})

	v, err := uc.UpdateEvent(context.Background(), id, event.UpdateEventInput{
		Tag: "newtag", Name: "new name", AvailableFrom: availableFrom, ArchiveAt: archiveAt,
	}, updatedBy)
	if err != nil {
		t.Fatalf("UpdateEvent: %v", err)
	}
	if v.Tag != "newtag" {
		t.Fatalf("view mismatch: %+v", v)
	}
}

func TestUpdateEvent_ZeroRowsRowPresent_Returns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	row := postgres.Event{
		ID: id, Tag: "oldtag", Name: "old name",
		AvailableFrom: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		ArchiveAt:     pgtype.Timestamptz{Time: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		CreatedAt:     t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}

	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(row, nil)
	q.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(row, nil)

	_, err := uc.UpdateEvent(context.Background(), id, event.UpdateEventInput{
		Tag: "newtag", Name: "new name",
		AvailableFrom: row.AvailableFrom, ArchiveAt: row.ArchiveAt.Time,
	}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventModel.ErrEventModified.Err()) {
		t.Fatalf("want ErrEventModified, got %v", err)
	}
}

func TestUpdateEvent_ZeroRowsRowGone_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	row := postgres.Event{
		ID: id, Tag: "oldtag", Name: "old name",
		AvailableFrom: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		ArchiveAt:     pgtype.Timestamptz{Time: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC), Valid: true},
		CreatedAt:     t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}

	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(row, nil)
	q.EXPECT().UpdateEvent(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(postgres.Event{}, pgx.ErrNoRows)

	_, err := uc.UpdateEvent(context.Background(), id, event.UpdateEventInput{
		Tag: "newtag", Name: "new name",
		AvailableFrom: row.AvailableFrom, ArchiveAt: row.ArchiveAt.Time,
	}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
		t.Fatalf("want ErrEventNotFound, got %v", err)
	}
}

func TestCreateEvent_DuplicateLiveTag_Returns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)

	// CountLiveEventsWithTag finds another live event with the same tag; the
	// write (CreateEvent) must never be reached. Create must exclude nothing
	// (ExcludeID == uuid.Nil) since there is no existing row to exempt.
	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, params postgres.CountLiveEventsWithTagParams) (int64, error) {
			if params.ExcludeID != uuid.Nil {
				t.Fatalf("want ExcludeID = uuid.Nil, got %v", params.ExcludeID)
			}
			return int64(1), nil
		})

	_, err := uc.CreateEvent(context.Background(), event.CreateEventInput{
		Tag: "livetag", Name: "n",
		AvailableFrom: time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC),
		ArchiveAt:     time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC),
	})
	if !errors.Is(err, eventModel.ErrEventExists.Err()) {
		t.Fatalf("want ErrEventExists, got %v", err)
	}
}

func TestArchiveEvent_PendingEventUsesCanonicalArchiveWrite(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())
	actor := uuid.Must(uuid.NewV7())
	now := time.Now()
	start := now.Add(24 * time.Hour)
	end := start.Add(24 * time.Hour)
	updatedAt := now.Add(-time.Hour)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(postgres.Event{
		ID: id, Tag: "pending", Name: "Pending", AvailableFrom: start, ArchiveAt: pgtype.Timestamptz{Time: end, Valid: true},
		PublishAt: start, StartAt: start, FinishAt: pgtype.Timestamptz{Time: end, Valid: true},
		WithdrawAt: pgtype.Timestamptz{Time: end.Add(time.Microsecond), Valid: true},
		CreatedAt:  updatedAt, UpdatedAt: pgtype.Timestamptz{Time: updatedAt, Valid: true},
	}, nil)
	q.EXPECT().ArchiveEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ArchiveEventParams) (int64, error) {
			if arg.ID != id || !arg.ExpectedUpdatedAt.Time.Equal(updatedAt) || arg.UpdatedBy.UUID != actor {
				t.Fatalf("archive lost identity or audit fields: %+v", arg)
			}
			if !arg.StartAt.Before(arg.ArchiveAt) || arg.PublishAt.After(arg.StartAt) ||
				!arg.FinishAt.Equal(arg.ArchiveAt) || !arg.WithdrawAt.After(arg.FinishAt) {
				t.Fatalf("archive must persist a valid withdrawn lifecycle: %+v", arg)
			}
			return 1, nil
		})
	v, err := uc.ArchiveEvent(context.Background(), id, actor)
	if err != nil || v.Status != eventModel.EventArchivedStatus {
		t.Fatalf("ArchiveEvent: view=%+v err=%v", v, err)
	}
}

func TestUpdateEvent_DuplicateLiveTag_Returns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())

	// CountLiveEventsWithTag finds another live event with the same tag; the
	// mutate path (GetEventByID/UpdateEvent) must never be reached. Update
	// must exclude the event's own id, so its own live row isn't counted
	// against itself.
	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, params postgres.CountLiveEventsWithTagParams) (int64, error) {
			if params.ExcludeID != id {
				t.Fatalf("want ExcludeID = %v, got %v", id, params.ExcludeID)
			}
			return int64(1), nil
		})

	_, err := uc.UpdateEvent(context.Background(), id, event.UpdateEventInput{
		Tag: "livetag", Name: "new name",
		AvailableFrom: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		ArchiveAt:     time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventModel.ErrEventExists.Err()) {
		t.Fatalf("want ErrEventExists, got %v", err)
	}
}

func TestUpdateEventLifecycle_WritesCanonicalRuntime(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())
	updatedBy := uuid.Must(uuid.NewV7())
	publishAt := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	startAt := publishAt.Add(time.Hour)
	updatedAt := time.Date(2026, 6, 1, 9, 0, 0, 0, time.UTC)
	row := postgres.Event{
		ID: id, Tag: "summer", Name: "Summer", JoinPolicy: int16(eventModel.JoinPolicyLockedAtStart),
		PublishAt: publishAt, StartAt: startAt,
		AvailableFrom: publishAt, ArchiveAt: pgtype.Timestamptz{Time: startAt.AddDate(1, 0, 0), Valid: true},
		CreatedAt: updatedAt, UpdatedAt: pgtype.Timestamptz{Time: updatedAt, Valid: true},
	}
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(row, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), id).Return(postgres.EventConfig{
		EventID: id, Participation: pgtype.Int2{Int16: 1, Valid: true},
	}, nil)
	q.EXPECT().UpdateEventLifecycle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventLifecycleParams) (int64, error) {
			if arg.ID != id || arg.JoinPolicy != int16(eventModel.JoinPolicyRolling) {
				t.Fatalf("unexpected lifecycle params: %+v", arg)
			}
			if !arg.PublishAt.Equal(publishAt) || !arg.StartAt.Equal(startAt) || arg.FinishAt.Valid || arg.WithdrawAt.Valid {
				t.Fatalf("unexpected lifecycle times: %+v", arg)
			}
			if !arg.ExpectedUpdatedAt.Valid || !arg.ExpectedUpdatedAt.Time.Equal(updatedAt) || arg.UpdatedBy.UUID != updatedBy {
				t.Fatalf("optimistic lock/audit fields missing: %+v", arg)
			}
			return 1, nil
		})

	view, err := uc.UpdateEventLifecycle(context.Background(), id, event.UpdateLifecycleInput{
		JoinPolicy: eventModel.JoinPolicyRolling, PublishAt: publishAt, StartAt: startAt,
	}, updatedBy)
	if err != nil {
		t.Fatalf("UpdateEventLifecycle: %v", err)
	}
	if view.JoinPolicy != eventModel.JoinPolicyRolling || view.Status != eventModel.LifecycleStarted {
		t.Fatalf("unexpected lifecycle view: %+v", view)
	}
}

func TestUpdateEventLifecycle_RequiresParticipation(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())
	publish := time.Now().Add(time.Hour)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(postgres.Event{
		ID: id, CreatedAt: time.Now(), AvailableFrom: time.Now(),
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), id).Return(postgres.EventConfig{EventID: id}, nil)
	_, err := uc.UpdateEventLifecycle(context.Background(), id, event.UpdateLifecycleInput{
		PublishAt: publish, StartAt: publish.Add(time.Hour),
	}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventConfigModel.ErrParticipationRequired.Err()) {
		t.Fatalf("publication without participation should be rejected, got %v", err)
	}
}

func TestUpdateEventLifecycle_CannotMovePublicationAfterItHappened(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())
	created := time.Date(2026, 7, 1, 9, 0, 0, 0, time.UTC)
	q.EXPECT().GetEventByID(gomock.Any(), id).Return(postgres.Event{
		ID: id, Tag: "summer", Name: "Summer", CreatedAt: created, AvailableFrom: created,
		LifecycleConfigured: true, PublishAt: created, StartAt: created.Add(time.Hour),
	}, nil)
	_, err := uc.UpdateEventLifecycle(context.Background(), id, event.UpdateLifecycleInput{
		PublishAt: time.Now().Add(time.Hour), StartAt: time.Now().Add(2 * time.Hour),
	}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventModel.ErrEventLifecycleInvalid.Err()) {
		t.Fatalf("publication time must remain fixed after publication, got %v", err)
	}
}

func TestUpdateEvent_DuplicateLiveTag_TrimsTagBeforeGuard(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())

	// The submitted tag has surrounding whitespace; the guard must trim it
	// the same way the domain (Event.UpdateEvent) does before persisting,
	// so the query matches the stored (trimmed) tag. Without the trim, a
	// live-tag collision would slip past this guard.
	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, params postgres.CountLiveEventsWithTagParams) (int64, error) {
			if params.Tag != "winter" {
				t.Fatalf("want trimmed Tag = %q, got %q", "winter", params.Tag)
			}
			if params.ExcludeID != id {
				t.Fatalf("want ExcludeID = %v, got %v", id, params.ExcludeID)
			}
			return int64(1), nil
		})

	_, err := uc.UpdateEvent(context.Background(), id, event.UpdateEventInput{
		Tag: " winter ", Name: "new name",
		AvailableFrom: time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC),
		ArchiveAt:     time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
	}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventModel.ErrEventExists.Err()) {
		t.Fatalf("want ErrEventExists, got %v", err)
	}
}

func TestDeleteEvent_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())

	// W4: the event's own exercises are archived in the same transaction.
	q.EXPECT().ArchiveEventExercises(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.ArchiveEventExercisesParams) error {
		if !arg.EventID.Valid || arg.EventID.UUID != id {
			t.Fatalf("archive scope = %+v", arg)
		}
		return nil
	})
	q.EXPECT().DeleteEvent(gomock.Any(), id).Return(int64(1), nil)

	if err := uc.DeleteEvent(context.Background(), id); err != nil {
		t.Fatalf("DeleteEvent: %v", err)
	}
}

func TestDeleteEvent_NotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	id := uuid.Must(uuid.NewV7())

	q.EXPECT().ArchiveEventExercises(gomock.Any(), gomock.Any()).Return(nil)
	q.EXPECT().DeleteEvent(gomock.Any(), id).Return(int64(0), nil)

	err := uc.DeleteEvent(context.Background(), id)
	if !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
		t.Fatalf("want ErrEventNotFound, got %v", err)
	}
}

func TestCreateEvent_InfrastructureFlagIsDecidedAtCreation(t *testing.T) {
	yes, no := true, false
	for _, tc := range []struct {
		name       string
		capability event.InfrastructureCapability
		requested  *bool
		want       bool
		wantErr    error
	}{
		{name: "default with laboratory", capability: availableLaboratoriesCapability{}, want: true},
		{name: "default without laboratory", capability: unavailableInfrastructureCapability{}, want: false},
		{name: "explicit off", capability: availableLaboratoriesCapability{}, requested: &no, want: false},
		{name: "explicit on without laboratory", capability: unavailableInfrastructureCapability{}, requested: &yes, wantErr: eventModel.ErrEventInfrastructureUnavailable.Err()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, InfrastructureCapability: tc.capability})
			start := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
			if tc.wantErr == nil {
				q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).Return(int64(0), nil)
				q.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventParams) (postgres.Event, error) {
					if arg.InfrastructureAllowed != tc.want {
						t.Fatalf("stored flag = %t, want %t", arg.InfrastructureAllowed, tc.want)
					}
					return postgres.Event{ID: arg.ID, Tag: arg.Tag, Name: arg.Name, AvailableFrom: arg.AvailableFrom, CreatedAt: arg.CreatedAt, InfrastructureAllowed: arg.InfrastructureAllowed}, nil
				})
				q.EXPECT().CreateEventConfig(gomock.Any(), gomock.Any()).Return(postgres.EventConfig{}, nil)
				q.EXPECT().CreateEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, nil)
			}
			view, err := uc.CreateEvent(context.Background(), event.CreateEventInput{Tag: "infra", Name: "Infra", AvailableFrom: start, CreatedBy: uuid.Must(uuid.NewV7()), InfrastructureAllowed: tc.requested})
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err = %v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil || view.InfrastructureAllowed != tc.want {
				t.Fatalf("view = %+v, err = %v", view, err)
			}
		})
	}
}
