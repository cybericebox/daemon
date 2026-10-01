package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func participationPtr(p eventConfigModel.Participation) *eventConfigModel.Participation { return &p }
func int32Ptr(v int32) *int32                                                           { return &v }

type availableLaboratoriesCapability struct{}

func (availableLaboratoriesCapability) RequireLaboratories(context.Context) error { return nil }

func TestUpdateEventTheme_PersistsDerivedColors(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	actor := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 9, 25, 10, 0, 0, 0, time.UTC)
	row := postgres.EventConfig{
		EventID: eventID, CreatedAt: t0,
		UpdatedAt:  pgtype.Timestamptz{Time: t0, Valid: true},
		BrandColor: "#211A52", AccentLight: "#211A52",
		AccentDark: "#E6E6EE", AccentLive: "#FFFFFF", ThemeVersion: 1,
	}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventConfigParams) (int64, error) {
		if arg.BrandColor != "#0B120E" || arg.AccentColor != "#5CCB7C" || arg.AccentLight != "#397E4D" || arg.AccentDark != "#5CCB7C" || arg.AccentLive != "#5CCB7C" || arg.ThemeVersion != 2 {
			t.Fatalf("unexpected persisted theme: %+v", arg)
		}
		if !arg.ExpectedUpdatedAt.Time.Equal(t0) {
			t.Fatalf("lost optimistic lock: %+v", arg.ExpectedUpdatedAt)
		}
		return 1, nil
	})
	v, err := uc.UpdateEventTheme(context.Background(), eventID, "#0B120E", "#5CCB7C", actor)
	if err != nil || v.Theme.AccentLight != "#397E4D" || v.Theme.Version != 2 {
		t.Fatalf("unexpected updated view: %+v, %v", v, err)
	}
}

func TestUpdateEventConfig_HappyPath_WritesRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}},
		Media: newTemplateMediaFake(), InfrastructureCapability: availableLaboratoriesCapability{},
	})
	eventID := uuid.Must(uuid.NewV7())
	by := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	row := postgres.EventConfig{
		EventID:                eventID,
		Participation:          pgtype.Int2{},
		Registration:           int16(eventConfigModel.RegistrationClose),
		ScoreboardVisibility:   int16(eventConfigModel.VisibilityHidden),
		ParticipantsVisibility: int16(eventConfigModel.VisibilityHidden),
		CreatedAt:              t0,
		UpdatedAt:              pgtype.Timestamptz{Time: t0, Valid: true},
	}

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, CreatedAt: t0, AvailableFrom: t0}, nil)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventConfigParams) (int64, error) {
			if !arg.ExpectedUpdatedAt.Time.Equal(t0) || !arg.ExpectedUpdatedAt.Valid {
				t.Fatalf("expected ExpectedUpdatedAt = %v, got %+v", t0, arg.ExpectedUpdatedAt)
			}
			if arg.Registration != int16(eventConfigModel.RegistrationOpen) {
				t.Fatalf("want Registration = %d, got %d", eventConfigModel.RegistrationOpen, arg.Registration)
			}
			if arg.ScoreboardVisibility != int16(eventConfigModel.VisibilityPublic) {
				t.Fatalf("want ScoreboardVisibility = %d, got %d", eventConfigModel.VisibilityPublic, arg.ScoreboardVisibility)
			}
			if arg.PreviewDescription != "hello" {
				t.Fatalf("want PreviewDescription = %q, got %q", "hello", arg.PreviewDescription)
			}
			if arg.Participation.Valid {
				t.Fatalf("want Participation still unset, got %+v", arg.Participation)
			}
			if arg.MaxTeamSize != 4 || !arg.MinTeamSize.Valid || arg.MinTeamSize.Int32 != 2 || !arg.MaxTeams.Valid || arg.MaxTeams.Int32 != 12 {
				t.Fatalf("unexpected team limits: max size %d, min %+v, max teams %+v", arg.MaxTeamSize, arg.MinTeamSize, arg.MaxTeams)
			}
			return int64(1), nil
		})

	v, err := uc.UpdateEventConfig(context.Background(), eventID, event.UpdateConfigInput{
		Registration:           eventConfigModel.RegistrationOpen,
		ScoreboardVisibility:   eventConfigModel.VisibilityPublic,
		ParticipantsVisibility: eventConfigModel.VisibilityPrivate,
		PreviewDescription:     "hello",
		MaxTeamSize:            4,
		MinTeamSize:            int32Ptr(2),
		MaxTeams:               int32Ptr(12),
	}, by)
	if err != nil {
		t.Fatalf("UpdateEventConfig: %v", err)
	}
	if v.Registration != eventConfigModel.RegistrationOpen || v.PreviewDescription != "hello" || v.MaxTeamSize != 4 || v.MinTeamSize == nil || *v.MinTeamSize != 2 || v.MaxTeams == nil || *v.MaxTeams != 12 {
		t.Fatalf("view mismatch: %+v", v)
	}
	if v.Participation != nil {
		t.Fatalf("want Participation still nil, got %v", *v.Participation)
	}
}

func TestParticipantEventInfoRequiresApprovalAndExposesVPNIntent(t *testing.T) {
	for _, tc := range []struct {
		name    string
		status  participantModel.Status
		allowed bool
	}{
		{name: "pending", status: participantModel.StatusPending},
		{name: "approved", status: participantModel.StatusApproved, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := newUC(q)
			eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
				Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(tc.status)}, nil)
			if tc.allowed {
				q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPrivate), ParticipantsVisibility: int16(eventConfigModel.VisibilityHidden)}, nil)
				started := startedEvent(eventID, time.Now())
				started.InfrastructureAllowed = true
				q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
				q.EXPECT().ListEventExercises(gomock.Any(), eventID).Return(nil, nil)
			}
			view, err := uc.GetApprovedParticipantInfo(context.Background(), eventID, userID)
			if tc.allowed {
				if err != nil || view.EventID != eventID || !view.UseVPN || !view.CanViewResults || view.CanViewParticipants || view.HasInfrastructureChallenges {
					t.Fatalf("approved projection: %+v, %v", view, err)
				}
			} else if err == nil {
				t.Fatalf("%s user received approved projection: %+v", tc.name, view)
			}
		})
	}
}

func TestUpdateEventConfig_Participation_FirstPUTSets(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	row := postgres.EventConfig{
		EventID:   eventID,
		CreatedAt: t0,
		UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
		// Participation left zero-value (invalid pgtype.Int2) => nil in domain.
	}

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, CreatedAt: t0, AvailableFrom: t0}, nil)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventConfigParams) (int64, error) {
			if !arg.Participation.Valid || arg.Participation.Int16 != int16(eventConfigModel.ParticipationTeam) {
				t.Fatalf("want Participation = Team, got %+v", arg.Participation)
			}
			return int64(1), nil
		})

	v, err := uc.UpdateEventConfig(context.Background(), eventID, event.UpdateConfigInput{
		Participation: participationPtr(eventConfigModel.ParticipationTeam),
	}, uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatalf("UpdateEventConfig: %v", err)
	}
	if v.Participation == nil || *v.Participation != eventConfigModel.ParticipationTeam {
		t.Fatalf("view mismatch: %+v", v)
	}
}

func TestUpdateEventConfig_Participation_SameValueIdempotent(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	row := postgres.EventConfig{
		EventID:       eventID,
		Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		CreatedAt:     t0,
		UpdatedAt:     pgtype.Timestamptz{Time: t0, Valid: true},
	}

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventConfigParams) (int64, error) {
			if !arg.Participation.Valid || arg.Participation.Int16 != int16(eventConfigModel.ParticipationTeam) {
				t.Fatalf("want Participation = Team, got %+v", arg.Participation)
			}
			return int64(1), nil
		})

	v, err := uc.UpdateEventConfig(context.Background(), eventID, event.UpdateConfigInput{
		Participation: participationPtr(eventConfigModel.ParticipationTeam),
	}, uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatalf("UpdateEventConfig: %v", err)
	}
	if v.Participation == nil || *v.Participation != eventConfigModel.ParticipationTeam {
		t.Fatalf("view mismatch: %+v", v)
	}
}

func TestUpdateEventConfig_Participation_DifferentValueLockedAfterPublication(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	row := postgres.EventConfig{
		EventID:       eventID,
		Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		CreatedAt:     t0,
		UpdatedAt:     pgtype.Timestamptz{Time: t0, Valid: true},
	}

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, CreatedAt: t0, AvailableFrom: t0, LifecycleConfigured: true,
		PublishAt: t0, StartAt: t0.Add(time.Hour),
	}, nil)
	// UpdateEventConfig must NOT be called: the domain mutation fails before
	// the write.

	_, err := uc.UpdateEventConfig(context.Background(), eventID, event.UpdateConfigInput{
		Participation: participationPtr(eventConfigModel.ParticipationIndividual),
	}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventConfigModel.ErrParticipationLocked.Err()) {
		t.Fatalf("want ErrParticipationLocked, got %v", err)
	}
}

func TestUpdateEventConfig_Participation_ChangesBeforePublication(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, CreatedAt: t0, AvailableFrom: t0,
		LifecycleConfigured: true, PublishAt: time.Now().Add(time.Hour), StartAt: time.Now().Add(2 * time.Hour),
	}, nil)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventConfigParams) (int64, error) {
		if !arg.Participation.Valid || arg.Participation.Int16 != int16(eventConfigModel.ParticipationIndividual) {
			t.Fatalf("want individual participation, got %+v", arg.Participation)
		}
		return 1, nil
	})
	view, err := uc.UpdateEventConfig(context.Background(), eventID, event.UpdateConfigInput{
		Participation: participationPtr(eventConfigModel.ParticipationIndividual),
	}, uuid.Must(uuid.NewV7()))
	if err != nil || view.Participation == nil || *view.Participation != eventConfigModel.ParticipationIndividual {
		t.Fatalf("participation change before publication: %+v, %v", view, err)
	}
}

func TestUpdateEventConfig_TeamSizeLockedAfterPublication(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	publish := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		MaxTeamSize: 5, MinTeamSize: pgtype.Int4{Int32: 2, Valid: true},
		CreatedAt: publish, UpdatedAt: pgtype.Timestamptz{Time: publish, Valid: true},
	}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, CreatedAt: publish, AvailableFrom: publish,
		LifecycleConfigured: true, PublishAt: publish, StartAt: publish.Add(time.Hour),
	}, nil)
	_, err := uc.UpdateEventConfig(context.Background(), eventID, event.UpdateConfigInput{
		Participation: participationPtr(eventConfigModel.ParticipationTeam),
		MaxTeamSize:   4, MinTeamSize: int32Ptr(2),
	}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventConfigModel.ErrTeamSizeLocked.Err()) {
		t.Fatalf("changing team size after publication must be rejected, got %v", err)
	}
}

func TestUpdateEventConfig_ZeroRowsRowPresent_Returns409(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	row := postgres.EventConfig{
		EventID:   eventID,
		CreatedAt: t0,
		UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil)

	_, err := uc.UpdateEventConfig(context.Background(), eventID, event.UpdateConfigInput{}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventModel.ErrEventModified.Err()) {
		t.Fatalf("want ErrEventModified, got %v", err)
	}
}

func TestUpdateEventConfig_ZeroRowsRowGone_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	row := postgres.EventConfig{
		EventID:   eventID,
		CreatedAt: t0,
		UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(row, nil)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{}, pgx.ErrNoRows)

	_, err := uc.UpdateEventConfig(context.Background(), eventID, event.UpdateConfigInput{}, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, eventConfigModel.ErrEventConfigNotFound.Err()) {
		t.Fatalf("want ErrEventConfigNotFound, got %v", err)
	}
}

func TestGetEventConfig_MissingRow_LazilyCreatesDefault(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{}, pgx.ErrNoRows)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, Tag: "t", Name: "n", CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().CreateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventConfigParams) (postgres.EventConfig, error) {
			if arg.EventID != eventID {
				t.Fatalf("want EventID = %v, got %v", eventID, arg.EventID)
			}
			if arg.Participation.Valid {
				t.Fatalf("want Participation unset, got %+v", arg.Participation)
			}
			if arg.Registration != int16(eventConfigModel.RegistrationClose) {
				t.Fatalf("want default RegistrationClose, got %d", arg.Registration)
			}
			if arg.ScoreboardVisibility != int16(eventConfigModel.VisibilityHidden) ||
				arg.ParticipantsVisibility != int16(eventConfigModel.VisibilityHidden) {
				t.Fatalf("want default hidden visibilities, got %+v", arg)
			}
			return postgres.EventConfig{
				EventID: arg.EventID, Registration: arg.Registration,
				ScoreboardVisibility: arg.ScoreboardVisibility, ParticipantsVisibility: arg.ParticipantsVisibility,
				CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt,
			}, nil
		})

	v, err := uc.GetEventConfig(context.Background(), eventID)
	if err != nil {
		t.Fatalf("GetEventConfig: %v", err)
	}
	if v.EventID != eventID || v.Participation != nil {
		t.Fatalf("view mismatch: %+v", v)
	}
}

func TestGetEventConfig_MissingRowAndEvent_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{}, pgx.ErrNoRows)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{}, pgx.ErrNoRows)
	// CreateEventConfig must NOT be called for a nonexistent event.

	_, err := uc.GetEventConfig(context.Background(), eventID)
	if !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
		t.Fatalf("want ErrEventNotFound, got %v", err)
	}
}

func TestGetEventConfig_ConcurrentCreateRace_ReturnsWinnerRow(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	// event_configs.event_id is the primary key: a concurrent first-read of
	// the same config-less event can lose the Create race with a
	// unique-violation. createDefaultConfig must tolerate that by re-reading
	// and returning the winner's row instead of surfacing a platform error.
	pgUniqueViolation := &pgconn.PgError{
		Code:   pgerrcode.UniqueViolation,
		Detail: `Key (event_id)=(` + eventID.String() + `) already exists.`,
	}

	winnerRow := postgres.EventConfig{
		EventID:                eventID,
		Registration:           int16(eventConfigModel.RegistrationClose),
		ScoreboardVisibility:   int16(eventConfigModel.VisibilityHidden),
		ParticipantsVisibility: int16(eventConfigModel.VisibilityHidden),
		CreatedAt:              t0,
		UpdatedAt:              pgtype.Timestamptz{Time: t0, Valid: true},
	}

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{}, pgx.ErrNoRows)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, Tag: "t", Name: "n", CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().CreateEventConfig(gomock.Any(), gomock.Any()).Return(postgres.EventConfig{}, pgUniqueViolation)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(winnerRow, nil)

	v, err := uc.GetEventConfig(context.Background(), eventID)
	if err != nil {
		t.Fatalf("GetEventConfig: %v", err)
	}
	if v.EventID != eventID {
		t.Fatalf("view mismatch: %+v", v)
	}
	if v.Registration != eventConfigModel.RegistrationClose {
		t.Fatalf("want winner's Registration = %d, got %d", eventConfigModel.RegistrationClose, v.Registration)
	}
}

func TestGetEventInfo_ComposesEventAndConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	availableFrom := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	archiveAt := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	publishAt := time.Date(2026, 7, 10, 0, 0, 0, 0, time.UTC)
	startAt := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	finishAt := time.Date(2026, 8, 15, 0, 0, 0, 0, time.UTC)
	withdrawAt := time.Date(2026, 8, 20, 0, 0, 0, 0, time.UTC)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, Tag: "summer", Name: "Summer Camp", LifecycleConfigured: true,
		AvailableFrom: availableFrom, ArchiveAt: pgtype.Timestamptz{Time: archiveAt, Valid: true},
		PublishAt: publishAt, StartAt: startAt,
		FinishAt:   pgtype.Timestamptz{Time: finishAt, Valid: true},
		WithdrawAt: pgtype.Timestamptz{Time: withdrawAt, Valid: true},
		CreatedAt:  t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID:                eventID,
		Participation:          pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
		Registration:           int16(eventConfigModel.RegistrationOpen),
		ScoreboardVisibility:   int16(eventConfigModel.VisibilityPublic),
		ParticipantsVisibility: int16(eventConfigModel.VisibilityPublic),
		PreviewDescription:     "come join",
		CreatedAt:              t0,
		UpdatedAt:              pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)

	v, err := uc.GetEventInfo(context.Background(), eventID)
	if err != nil {
		t.Fatalf("GetEventInfo: %v", err)
	}
	if v.Tag != "summer" || v.Name != "Summer Camp" {
		t.Fatalf("event fields mismatch: %+v", v)
	}
	if !v.StartTime.Equal(startAt) || v.FinishTime == nil || !v.FinishTime.Equal(finishAt) {
		t.Fatalf("lifecycle mismatch: %+v", v)
	}
	if v.Participation == nil || *v.Participation != eventConfigModel.ParticipationTeam {
		t.Fatalf("participation mismatch: %+v", v)
	}
	if v.Registration != eventConfigModel.RegistrationOpen || v.PreviewDescription != "come join" {
		t.Fatalf("config fields mismatch: %+v", v)
	}
}

func TestGetEventInfo_EventNotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{}, pgx.ErrNoRows)

	_, err := uc.GetEventInfo(context.Background(), eventID)
	if !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
		t.Fatalf("want ErrEventNotFound, got %v", err)
	}
}

func TestCreateEvent_AlsoCreatesConfig(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	adminID := uuid.Must(uuid.NewV7())
	availableFrom := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	archiveAt := time.Date(2026, 8, 12, 10, 0, 0, 0, time.UTC)

	q.EXPECT().CountLiveEventsWithTag(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	q.EXPECT().CreateEvent(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventParams) (postgres.Event, error) {
			return postgres.Event{
				ID: arg.ID, Tag: arg.Tag, Name: arg.Name,
				AvailableFrom: arg.AvailableFrom, ArchiveAt: arg.ArchiveAt,
				CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy,
				UpdatedAt: arg.UpdatedAt, UpdatedBy: arg.UpdatedBy,
			}, nil
		})
	q.EXPECT().CreateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventConfigParams) (postgres.EventConfig, error) {
			if arg.Participation.Valid {
				t.Fatalf("want Participation unset on create, got %+v", arg.Participation)
			}
			if arg.Registration != int16(eventConfigModel.RegistrationClose) {
				t.Fatalf("want default RegistrationClose, got %d", arg.Registration)
			}
			if arg.ScoreboardVisibility != int16(eventConfigModel.VisibilityHidden) ||
				arg.ParticipantsVisibility != int16(eventConfigModel.VisibilityHidden) {
				t.Fatalf("want default hidden visibilities, got %+v", arg)
			}
			if arg.EventID == uuid.Nil {
				t.Fatal("want EventID to match the created event")
			}
			return postgres.EventConfig{
				EventID: arg.EventID, Registration: arg.Registration,
				ScoreboardVisibility: arg.ScoreboardVisibility, ParticipantsVisibility: arg.ParticipantsVisibility,
				CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt,
			}, nil
		})
	q.EXPECT().CreateEventManager(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventManagerParams) (postgres.EventManager, error) {
			if arg.EventID == uuid.Nil || arg.UserID != adminID || arg.Role != 0 {
				t.Fatalf("want owner membership for the event creator, got %+v", arg)
			}
			return postgres.EventManager{EventID: arg.EventID, UserID: arg.UserID, Role: arg.Role, CreatedAt: arg.CreatedAt}, nil
		})

	v, err := uc.CreateEvent(context.Background(), event.CreateEventInput{
		Tag: "myevent", Name: "My Event", AvailableFrom: availableFrom, ArchiveAt: archiveAt, CreatedBy: adminID,
	})
	if err != nil {
		t.Fatalf("CreateEvent: %v", err)
	}
	if v.Tag != "myevent" {
		t.Fatalf("view mismatch: %+v", v)
	}
}

func TestParticipantEventInfoExposesBoardPresentationAndInfrastructure(t *testing.T) {
	for _, tc := range []struct {
		name        string
		allowed     bool
		exercises   []postgres.EventExercise
		wantInfra   bool
		listsBoards bool
	}{
		{name: "infrastructure not allowed", allowed: false},
		{name: "allowed without exercises", allowed: true, listsBoards: true},
		{name: "allowed with a superseded dynamic exercise", allowed: true, listsBoards: true,
			exercises: []postgres.EventExercise{{ID: uuid.Must(uuid.NewV7()), ExerciseVersionID: uuid.Must(uuid.NewV7()), SupersededAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}}},
		{name: "allowed with an active dynamic exercise", allowed: true, listsBoards: true, wantInfra: true,
			exercises: []postgres.EventExercise{{ID: uuid.Must(uuid.NewV7()), ExerciseVersionID: uuid.Must(uuid.NewV7())}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: &testUoW{}}, Media: newTemplateMediaFake(), Topologies: dynamicTopologyResolver{}})
			eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).
				Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved)}, nil)
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ShowDifficulty: false}, nil)
			started := startedEvent(eventID, time.Now())
			started.InfrastructureAllowed = tc.allowed
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
			if tc.listsBoards {
				q.EXPECT().ListEventExercises(gomock.Any(), eventID).Return(tc.exercises, nil)
			}
			view, err := uc.GetApprovedParticipantInfo(context.Background(), eventID, userID)
			if err != nil {
				t.Fatalf("GetApprovedParticipantInfo: %v", err)
			}
			if view.ShowDifficulty || view.HintsDisabled || view.HasInfrastructureChallenges != tc.wantInfra {
				t.Fatalf("unexpected projection: %+v", view)
			}
		})
	}
}
