package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestJoinEvent_RequiredParticipantFormNeedsCurrentAnswer(t *testing.T) {
	for _, tc := range []struct {
		name          string
		answerVersion bool
		answerExists  bool
		allowed       bool
	}{
		{name: "no answer"},
		{name: "old version", answerExists: true},
		{name: "current version", answerExists: true, answerVersion: true, allowed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := postgresMocks.NewMockQuerier(ctrl)
			allowNonStaff(q)
			uc := newUC(q)
			eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			formVersionID := uuid.Must(uuid.NewV7())
			now := time.Now().UTC()
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
				ID: eventID, Tag: "form-gate", Name: "Form gate", LifecycleConfigured: true,
				PublishAt: now.Add(-time.Hour), StartAt: now.Add(time.Hour),
				AvailableFrom: now.Add(time.Hour), ArchiveAt: pgtype.Timestamptz{Time: now.Add(2 * time.Hour), Valid: true},
				CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
			}, nil)
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
				EventID: eventID, Registration: int16(eventConfigModel.RegistrationOpen),
				CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
			}, nil)
			q.EXPECT().GetLatestEventFormVersion(gomock.Any(), eventID).Return(postgres.EventFormVersion{
				ID: formVersionID, FormID: uuid.Must(uuid.NewV7()), EventID: eventID, Version: 2,
				Enabled: true, Required: true, Document: []byte(`{"blocks":[]}`), CreatedAt: now,
			}, nil)
			if tc.answerExists && tc.answerVersion {
				q.EXPECT().GetEventFormAnswer(gomock.Any(), postgres.GetEventFormAnswerParams{EventID: eventID, UserID: userID, FormVersionID: formVersionID}).Return(postgres.EventFormAnswer{
					EventID: eventID, UserID: userID, FormVersionID: formVersionID, Answers: []byte(`{}`), SubmittedAt: now,
				}, nil)
			} else {
				q.EXPECT().GetEventFormAnswer(gomock.Any(), postgres.GetEventFormAnswerParams{EventID: eventID, UserID: userID, FormVersionID: formVersionID}).Return(postgres.EventFormAnswer{}, pgx.ErrNoRows)
			}
			if tc.allowed {
				q.EXPECT().UpsertEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpsertEventParticipantParams) (postgres.EventParticipant, error) {
					return postgres.EventParticipant{EventID: eventID, UserID: userID, Status: arg.Status, CreatedAt: arg.CreatedAt}, nil
				})
			}
			_, err := uc.JoinEvent(context.Background(), eventID, userID)
			if tc.allowed && err != nil {
				t.Fatalf("current form answer should allow join: %v", err)
			}
			if !tc.allowed && !errors.Is(err, participantModel.ErrParticipantFormRequired.Err()) {
				t.Fatalf("want required form conflict, got %v", err)
			}
		})
	}
}

func TestJoinEvent_OpenRegistration_RollingAfterStartAllowed_WritesApproved(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	// JoinEvent computes now internally, so lifecycle boundaries must be
	// anchored to the real clock. Rolling events stay joinable after Start.
	publishAt := time.Now().Add(-2 * time.Hour)
	startAt := time.Now().Add(-time.Hour)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "t", Name: "n",
		AvailableFrom: publishAt, ArchiveAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		JoinPolicy: int16(eventModel.JoinPolicyRolling),
		PublishAt:  publishAt, StartAt: startAt,
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID:                eventID,
		Registration:           int16(eventConfigModel.RegistrationOpen),
		ScoreboardVisibility:   int16(eventConfigModel.VisibilityHidden),
		ParticipantsVisibility: int16(eventConfigModel.VisibilityHidden),
		CreatedAt:              t0,
		UpdatedAt:              pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().UpsertEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpsertEventParticipantParams) (postgres.EventParticipant, error) {
			if arg.EventID != eventID || arg.UserID != userID {
				t.Fatalf("unexpected identity: %+v", arg)
			}
			if arg.Status != int16(participantModel.StatusApproved) {
				t.Fatalf("want Status = Approved, got %d", arg.Status)
			}
			if !arg.DecidedAt.Valid {
				t.Fatalf("want DecidedAt set for an auto-approved join")
			}
			return postgres.EventParticipant{
				EventID: arg.EventID, UserID: arg.UserID, Status: arg.Status,
				CreatedAt: arg.CreatedAt, DecidedAt: arg.DecidedAt, DecidedBy: arg.DecidedBy,
			}, nil
		})

	v, err := uc.JoinEvent(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("JoinEvent: %v", err)
	}
	if v.Status != participantModel.StatusApproved {
		t.Fatalf("want StatusApproved, got %v", v.Status)
	}
}

func TestJoinEvent_ApprovalRegistration_WritesPending(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	availableFrom := time.Now().Add(time.Hour) // event not started yet

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "t", Name: "n",
		AvailableFrom: availableFrom, ArchiveAt: pgtype.Timestamptz{Time: time.Now().Add(2 * time.Hour), Valid: true},
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID:      eventID,
		Registration: int16(eventConfigModel.RegistrationApproval),
		CreatedAt:    t0,
		UpdatedAt:    pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().UpsertEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpsertEventParticipantParams) (postgres.EventParticipant, error) {
			if arg.Status != int16(participantModel.StatusPending) {
				t.Fatalf("want Status = Pending, got %d", arg.Status)
			}
			if arg.DecidedAt.Valid {
				t.Fatalf("want DecidedAt unset for a pending join")
			}
			return postgres.EventParticipant{
				EventID: arg.EventID, UserID: arg.UserID, Status: arg.Status,
				CreatedAt: arg.CreatedAt, DecidedAt: arg.DecidedAt, DecidedBy: arg.DecidedBy,
			}, nil
		})

	v, err := uc.JoinEvent(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("JoinEvent: %v", err)
	}
	if v.Status != participantModel.StatusPending {
		t.Fatalf("want StatusPending, got %v", v.Status)
	}
}

func TestJoinEvent_IndividualOpenRegistration_CreatesSoloTeam(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	now := time.Now()
	publishAt := now.Add(-2 * time.Hour)
	startAt := now.Add(-time.Hour)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "t", Name: "n", AvailableFrom: publishAt, ArchiveAt: pgtype.Timestamptz{Time: now.Add(time.Hour), Valid: true},
		JoinPolicy: int16(eventModel.JoinPolicyRolling),
		PublishAt:  publishAt, StartAt: startAt, CreatedAt: now.Add(-3 * time.Hour), UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true},
		Registration: int16(eventConfigModel.RegistrationOpen), MaxTeamSize: 1,
		CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().UpsertEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpsertEventParticipantParams) (postgres.EventParticipant, error) {
			return postgres.EventParticipant{EventID: arg.EventID, UserID: arg.UserID, Status: arg.Status, CreatedAt: arg.CreatedAt, DecidedAt: arg.DecidedAt}, nil
		})
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true},
		Registration: int16(eventConfigModel.RegistrationOpen), MaxTeamSize: 1,
		CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now,
	}, nil)
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
			if arg.CaptainID != userID || arg.MemberCount != 1 || arg.Name[:5] != "Solo-" {
				t.Fatalf("unexpected solo team: %+v", arg)
			}
			return postgres.EventTeam{ID: arg.ID, EventID: arg.EventID, Name: arg.Name, JoinCode: arg.JoinCode, CaptainID: arg.CaptainID, MemberCount: 1, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
		})
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.AssignEventParticipantTeamParams) (int64, error) {
			if arg.UserID != userID || !arg.TeamRole.Valid || arg.TeamRole.Int16 != int16(participantModel.TeamRoleCaptain) {
				t.Fatalf("unexpected solo assignment: %+v", arg)
			}
			return 1, nil
		})

	v, err := uc.JoinEvent(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("JoinEvent: %v", err)
	}
	if v.Status != participantModel.StatusApproved || !unit.saved {
		t.Fatalf("individual join must create and commit solo team: view=%+v unit=%+v", v, unit)
	}
}

func TestJoinEvent_AfterStartForbidden_NoUpsert(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	availableFrom := time.Now().Add(-time.Hour) // event already started

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "t", Name: "n",
		AvailableFrom: availableFrom, ArchiveAt: pgtype.Timestamptz{Time: time.Now().Add(time.Hour), Valid: true},
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID:      eventID,
		Registration: int16(eventConfigModel.RegistrationOpen),
		CreatedAt:    t0,
		UpdatedAt:    pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	// UpsertEventParticipant must NOT be called.

	_, err := uc.JoinEvent(context.Background(), eventID, userID)
	if !errors.Is(err, participantModel.ErrRegistrationAfterStartForbidden.Err()) {
		t.Fatalf("want ErrRegistrationAfterStartForbidden, got %v", err)
	}
}

func TestJoinEvent_RegistrationClosed_NoUpsert(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	availableFrom := time.Now().Add(time.Hour) // event not started yet, so the after-start gate doesn't preempt this check

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "t", Name: "n",
		AvailableFrom: availableFrom, ArchiveAt: pgtype.Timestamptz{Time: time.Now().Add(2 * time.Hour), Valid: true},
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID:      eventID,
		Registration: int16(eventConfigModel.RegistrationClose),
		CreatedAt:    t0,
		UpdatedAt:    pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	// UpsertEventParticipant must NOT be called.

	_, err := uc.JoinEvent(context.Background(), eventID, userID)
	if !errors.Is(err, participantModel.ErrRegistrationClosed.Err()) {
		t.Fatalf("want ErrRegistrationClosed, got %v", err)
	}
}

func TestJoinEvent_AlreadyPending_ReturnsErrAlreadyParticipant(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	availableFrom := time.Now().Add(time.Hour)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, Tag: "t", Name: "n",
		AvailableFrom: availableFrom, ArchiveAt: pgtype.Timestamptz{Time: time.Now().Add(2 * time.Hour), Valid: true},
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID:      eventID,
		Registration: int16(eventConfigModel.RegistrationApproval),
		CreatedAt:    t0,
		UpdatedAt:    pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	// ON CONFLICT DO NOTHING returns no row -> surfaces as pgx.ErrNoRows in the repo, which Upsert maps to created=false.
	q.EXPECT().UpsertEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusPending), CreatedAt: t0,
	}, nil)

	_, err := uc.JoinEvent(context.Background(), eventID, userID)
	if !errors.Is(err, participantModel.ErrAlreadyParticipant.Err()) {
		t.Fatalf("want ErrAlreadyParticipant, got %v", err)
	}
}

func TestJoinEvent_AlreadyApproved_ReturnsErrAlreadyParticipant(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	availableFrom := time.Now().Add(time.Hour)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, AvailableFrom: availableFrom, ArchiveAt: pgtype.Timestamptz{Time: time.Now().Add(2 * time.Hour), Valid: true},
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Registration: int16(eventConfigModel.RegistrationOpen),
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().UpsertEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: t0,
	}, nil)

	_, err := uc.JoinEvent(context.Background(), eventID, userID)
	if !errors.Is(err, participantModel.ErrAlreadyParticipant.Err()) {
		t.Fatalf("want ErrAlreadyParticipant, got %v", err)
	}
}

// A rejected re-join is deliberately NOT an error: JoinEvent returns the
// existing (still Rejected) status so the client can render "you were
// rejected" rather than a generic conflict. See participation.go's doc
// comment on JoinEvent for the rationale.
func TestJoinEvent_AlreadyRejected_ReturnsCurrentStatusNoError(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	availableFrom := time.Now().Add(time.Hour)

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true,
		ID: eventID, AvailableFrom: availableFrom, ArchiveAt: pgtype.Timestamptz{Time: time.Now().Add(2 * time.Hour), Valid: true},
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Registration: int16(eventConfigModel.RegistrationApproval),
		CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)
	q.EXPECT().UpsertEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusRejected), CreatedAt: t0,
	}, nil)

	v, err := uc.JoinEvent(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("JoinEvent: %v", err)
	}
	if v.Status != participantModel.StatusRejected {
		t.Fatalf("want StatusRejected, got %v", v.Status)
	}
}

func TestJoinEvent_EventNotFound_Returns404(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{LifecycleConfigured: true}, pgx.ErrNoRows)

	_, err := uc.JoinEvent(context.Background(), eventID, userID)
	if !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
		t.Fatalf("want ErrEventNotFound, got %v", err)
	}
}

func TestGetJoinInfo_NoRow_ReturnsStatusNone(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{}, pgx.ErrNoRows)

	expectJoinInfoEvent(q, eventID)
	v, err := uc.GetJoinInfo(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("GetJoinInfo: %v", err)
	}
	if v.Status != participantModel.StatusNone {
		t.Fatalf("want StatusNone, got %v", v.Status)
	}
}

func TestGetJoinInfo_RowExists_ReturnsItsStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{
			EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: t0,
		}, nil)

	expectJoinInfoEvent(q, eventID)
	v, err := uc.GetJoinInfo(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("GetJoinInfo: %v", err)
	}
	if v.Status != participantModel.StatusApproved {
		t.Fatalf("want StatusApproved, got %v", v.Status)
	}
}

func TestApproveParticipant_Pending_WritesApprovedWithDecidedBy(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	by := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{
			EventID: eventID, UserID: userID, Status: int16(participantModel.StatusPending), CreatedAt: t0,
		}, nil)
	q.EXPECT().UpdateEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventParticipantParams) (int64, error) {
			if arg.Status != int16(participantModel.StatusApproved) {
				t.Fatalf("want Status = Approved, got %d", arg.Status)
			}
			if !arg.DecidedBy.Valid || arg.DecidedBy.UUID != by {
				t.Fatalf("want DecidedBy = %v, got %+v", by, arg.DecidedBy)
			}
			if !arg.DecidedAt.Valid {
				t.Fatalf("want DecidedAt set")
			}
			return int64(1), nil
		})
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, CreatedAt: t0, UpdatedAt: pgtype.Timestamptz{Time: t0, Valid: true},
	}, nil)

	if err := uc.ApproveParticipant(context.Background(), eventID, userID, by); err != nil {
		t.Fatalf("ApproveParticipant: %v", err)
	}
}

func TestApproveParticipant_IndividualAfterStart_CreatesSoloTeam(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID, moderatorID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	config := postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true},
		MaxTeamSize: 1, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusPending), CreatedAt: now.Add(-time.Hour),
	}, nil)
	q.EXPECT().UpdateEventParticipant(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(config, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(config, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: now.Add(-time.Hour),
	}, nil)
	q.EXPECT().CreateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateEventTeamParams) (postgres.EventTeam, error) {
		return postgres.EventTeam{ID: arg.ID, EventID: arg.EventID, Name: arg.Name, CaptainID: arg.CaptainID, MemberCount: 1, CreatedAt: arg.CreatedAt, UpdatedAt: arg.UpdatedAt}, nil
	})
	q.EXPECT().AssignEventParticipantTeam(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	if err := uc.ApproveParticipant(context.Background(), eventID, userID, moderatorID); err != nil {
		t.Fatalf("moderator approval after start must create a solo team: %v", err)
	}
	if !unit.saved {
		t.Fatal("solo team was not committed")
	}
}

func TestApproveParticipant_NotPending_ReturnsErrParticipantNotPending(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{
			EventID: eventID, UserID: userID, Status: int16(participantModel.StatusApproved), CreatedAt: t0,
		}, nil)
	// UpdateEventParticipant must NOT be called.

	err := uc.ApproveParticipant(context.Background(), eventID, userID, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, participantModel.ErrParticipantNotPending.Err()) {
		t.Fatalf("want ErrParticipantNotPending, got %v", err)
	}
}

func TestApproveParticipant_NoRow_ReturnsErrParticipantNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	// UpdateEventParticipant must NOT be called.

	err := uc.ApproveParticipant(context.Background(), eventID, userID, uuid.Must(uuid.NewV7()))
	if !errors.Is(err, participantModel.ErrParticipantNotFound.Err()) {
		t.Fatalf("want ErrParticipantNotFound, got %v", err)
	}
}

func TestRejectParticipant_Pending_WritesRejected(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	by := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)

	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).
		Return(postgres.EventParticipant{
			EventID: eventID, UserID: userID, Status: int16(participantModel.StatusPending), CreatedAt: t0,
		}, nil)
	q.EXPECT().UpdateEventParticipant(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateEventParticipantParams) (int64, error) {
			if arg.Status != int16(participantModel.StatusRejected) {
				t.Fatalf("want Status = Rejected, got %d", arg.Status)
			}
			if !arg.DecidedBy.Valid || arg.DecidedBy.UUID != by {
				t.Fatalf("want DecidedBy = %v, got %+v", by, arg.DecidedBy)
			}
			return int64(1), nil
		})

	if err := uc.RejectParticipant(context.Background(), eventID, userID, by); err != nil {
		t.Fatalf("RejectParticipant: %v", err)
	}
}

// expectParticipantListSides stubs the batch reads around the page query.
func expectParticipantListSides(q *postgresMocks.MockQuerier, total int64) {
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().CountEventParticipants(gomock.Any(), gomock.Any()).Return(total, nil).AnyTimes()
	q.EXPECT().CountEventParticipantKinds(gomock.Any(), gomock.Any()).Return(postgres.CountEventParticipantKindsRow{Participants: 1, Applications: 2, Invitations: 3}, nil)
}

func TestListParticipants_FirstPage_FiltersByStatus(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())
	t0 := time.Date(2026, 7, 12, 10, 0, 0, 0, time.UTC)
	status := participantModel.StatusPending
	participantID := uuid.Must(uuid.NewV7())

	q.EXPECT().ListEventParticipantsDetailed(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListEventParticipantsDetailedParams) ([]postgres.ListEventParticipantsDetailedRow, error) {
			if arg.EventID != eventID {
				t.Fatalf("want EventID = %v, got %v", eventID, arg.EventID)
			}
			if arg.StatusFilter != int32(participantModel.StatusPending) || arg.Kind != "applications" {
				t.Fatalf("want StatusFilter = Pending and applications kind, got %d %q", arg.StatusFilter, arg.Kind)
			}
			return []postgres.ListEventParticipantsDetailedRow{
				{EventID: eventID, UserID: participantID, Status: int16(participantModel.StatusPending), CreatedAt: t0, FirstName: "Олена", LastName: "Коваль", Email: "olena@example.test", DisplayName: "Олена Коваль"},
			}, nil
		})
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return([]postgres.ListLatestRegistrationAnswersForUsersRow{{UserID: participantID, Answers: []byte(`{"city":"Київ"}`)}}, nil)
	expectParticipantListSides(q, 3)

	res, err := uc.ListParticipants(context.Background(), event.ListParticipantsFilter{
		EventID: eventID, Status: &status, Kind: participantRepo.KindApplications,
	})
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(res.Participants) != 1 || res.HasMore {
		t.Fatalf("result mismatch: %+v", res)
	}
	if res.Participants[0].Name != "Олена Коваль" || res.Participants[0].Email != "olena@example.test" || res.Participants[0].Answers["city"] != "Київ" {
		t.Fatalf("participant identity or answers missing: %+v", res.Participants[0])
	}
	if res.Total != 3 || res.Counts.Invitations != 3 || res.Counts.Applications != 2 {
		t.Fatalf("want Total = 3 and tab counts, got %d %+v", res.Total, res.Counts)
	}
}

func TestListParticipants_PassesSearchToListAndCount(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().ListEventParticipantsDetailed(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListEventParticipantsDetailedParams) ([]postgres.ListEventParticipantsDetailedRow, error) {
			if arg.Search != "олена" || arg.Kind != "participants" {
				t.Fatalf("want search in list params, got %+v", arg)
			}
			return nil, nil
		})
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	q.EXPECT().GetEventByID(gomock.Any(), gomock.Any()).Return(postgres.Event{}, pgx.ErrNoRows).AnyTimes()
	q.EXPECT().CountEventParticipants(gomock.Any(), postgres.CountEventParticipantsParams{EventID: eventID, StatusFilter: -1, Kind: "participants", Search: "олена", FieldFilters: []byte(`[{"key":"city","op":"contains","value":"Київ"}]`)}).Return(int64(1), nil)
	q.EXPECT().CountEventParticipantKinds(gomock.Any(), gomock.Any()).Return(postgres.CountEventParticipantKindsRow{}, nil)

	res, err := uc.ListParticipants(context.Background(), event.ListParticipantsFilter{EventID: eventID, Kind: participantRepo.KindParticipants, Search: "олена", Fields: []event.AnswerFilter{{Key: "city", Op: event.AnswerFilterContains, Value: "Київ"}}})
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if res.Total != 1 {
		t.Fatalf("want Total = 1, got %d", res.Total)
	}
}

func TestListParticipants_RejectsUnknownKind(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	_, err := uc.ListParticipants(context.Background(), event.ListParticipantsFilter{EventID: uuid.Must(uuid.NewV7()), Kind: "everyone"})
	if !errors.Is(err, participantModel.ErrParticipantKindInvalid.Err()) {
		t.Fatalf("unknown kind err = %v", err)
	}
}

func TestListParticipants_NoFilter_UsesSentinel(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID := uuid.Must(uuid.NewV7())

	q.EXPECT().ListEventParticipantsDetailed(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.ListEventParticipantsDetailedParams) ([]postgres.ListEventParticipantsDetailedRow, error) {
			if arg.StatusFilter != -1 || arg.Kind != "" {
				t.Fatalf("want StatusFilter = -1 (any) and no kind, got %d %q", arg.StatusFilter, arg.Kind)
			}
			return nil, nil
		})
	expectParticipantListSides(q, 0)

	res, err := uc.ListParticipants(context.Background(), event.ListParticipantsFilter{EventID: eventID})
	if err != nil {
		t.Fatalf("ListParticipants: %v", err)
	}
	if len(res.Participants) != 0 || res.Total != 0 {
		t.Fatalf("result mismatch: %+v", res)
	}
}

func TestListParticipants_ExpiredInvitation(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventParticipantsDetailed(gomock.Any(), gomock.Any()).Return([]postgres.ListEventParticipantsDetailedRow{{
		EventID: eventID, UserID: userID, Status: int16(participantModel.StatusPending), Invited: true, CreatedAt: time.Now(),
	}}, nil)
	q.EXPECT().ListLatestRegistrationAnswersForUsers(gomock.Any(), gomock.Any()).Return(nil, nil)
	// A finished event: the registration window is closed.
	past := time.Now().Add(-48 * time.Hour)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, LifecycleConfigured: true, PublishAt: past, StartAt: past.Add(time.Hour),
		ManualFinishedAt: pgtype.Timestamptz{Time: past.Add(2 * time.Hour), Valid: true},
	}, nil)
	expectParticipantListSides(q, 1)
	result, err := uc.ListParticipants(context.Background(), event.ListParticipantsFilter{EventID: eventID})
	if err != nil || len(result.Participants) != 1 || !result.Participants[0].InvitationExpired {
		t.Fatalf("invitation of a finished event must be expired: result=%+v err=%v", result, err)
	}
}

func TestListParticipantsReportsTeamVisibility(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventParticipantsDetailed(gomock.Any(), gomock.Any()).Return([]postgres.ListEventParticipantsDetailedRow{{
		EventID: eventID, UserID: userID, TeamID: uuid.NullUUID{UUID: teamID, Valid: true},
		Status: int16(participantModel.StatusApproved), CreatedAt: time.Now(), TeamHidden: true, TeamName: "Олена",
	}}, nil)
	expectParticipantListSides(q, 1)
	result, err := uc.ListParticipants(context.Background(), event.ListParticipantsFilter{EventID: eventID})
	if err != nil || len(result.Participants) != 1 || !result.Participants[0].Hidden || result.Participants[0].TeamName != "Олена" {
		t.Fatalf("participant must expose technical team visibility: result=%+v err=%v", result, err)
	}
}

func TestSetIndividualParticipantHiddenUpdatesPersonalTeam(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationIndividual), Valid: true},
		CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
	}, nil)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{
		EventID: eventID, UserID: userID, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}, Status: int16(participantModel.StatusApproved),
	}, nil)
	q.EXPECT().GetEventTeamByID(gomock.Any(), postgres.GetEventTeamByIDParams{EventID: eventID, ID: teamID}).Return(postgres.EventTeam{
		ID: teamID, EventID: eventID, Name: "Personal", CaptainID: userID, MemberCount: 1, CreatedAt: now, UpdatedAt: now,
	}, nil)
	q.EXPECT().UpdateEventTeam(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.UpdateEventTeamParams) (int64, error) {
		if arg.ID != teamID || !arg.Hidden || arg.Name != "Personal" {
			t.Fatalf("visibility must update personal team without renaming: %+v", arg)
		}
		return 1, nil
	})
	q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{Revision: 1, UpdatedAt: time.Now()}, nil)
	q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).Return(postgres.EventResultChange{EventID: eventID, Revision: 1, Kind: "scoreboard_recalculated", CreatedAt: time.Now()}, nil)
	if err := uc.SetIndividualParticipantHidden(context.Background(), eventID, userID, true); err != nil {
		t.Fatal(err)
	}
	if !unit.saved {
		t.Fatal("visibility update must be committed")
	}
}

// allowNonStaff answers the event-staff lookup of a join, invitation or
// participation read: the user holds no event-local management membership.
func allowNonStaff(q *postgresMocks.MockQuerier) {
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows).AnyTimes()
}

// expectJoinInfoEvent answers the event and config reads GetJoinInfo adds to
// the caller's stored participation: a published, not yet started event with
// open registration and team participation.
func expectJoinInfoEvent(q *postgresMocks.MockQuerier, eventID uuid.UUID) {
	now := time.Now()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, LifecycleConfigured: true, PublishAt: now.Add(-time.Hour), StartAt: now.Add(time.Hour),
	}, nil).AnyTimes()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Registration: int16(eventConfigModel.RegistrationOpen),
		Participation: pgtype.Int2{Int16: int16(eventConfigModel.ParticipationTeam), Valid: true},
	}, nil).AnyTimes()
}

func TestGetJoinInfo_ReportsParticipationBlock(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	expectJoinInfoEvent(q, eventID)

	v, err := uc.GetJoinInfo(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("GetJoinInfo: %v", err)
	}
	if v.Participation == nil || !v.Participation.Register.Allowed || !v.Participation.RegistrationWindowOpen || v.Participation.RegistrationClosesAt == nil {
		t.Fatalf("a signed-in user before the start may register: %+v", v.Participation)
	}
}

func TestGetJoinInfo_StaffCannotRegister(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := newUC(q)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{EventID: eventID, UserID: userID, Role: 0}, nil)
	expectJoinInfoEvent(q, eventID)

	v, err := uc.GetJoinInfo(context.Background(), eventID, userID)
	if err != nil {
		t.Fatalf("GetJoinInfo: %v", err)
	}
	if v.Participation.Register.Allowed || v.Participation.Register.Reason != event.ReasonStaff || !v.Participation.Staff {
		t.Fatalf("staff must not be offered registration: %+v", v.Participation.Register)
	}
}

// Every organizer role is staff, and staff never registers in normal mode:
// the server rejects it with its own error code, before any write.
func TestJoinEvent_StaffIsRejectedWithClearCode(t *testing.T) {
	for _, role := range []int16{0, 1, 2} { // owner, manager, viewer
		ctrl := gomock.NewController(t)
		q := newFormGateMock(ctrl)
		uc := newUC(q)
		eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
		expectJoinInfoEvent(q, eventID)
		q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{EventID: eventID, UserID: userID, Role: role}, nil)

		_, err := uc.JoinEvent(context.Background(), eventID, userID)
		if !errors.Is(err, participantModel.ErrStaffCannotParticipate.Err()) {
			t.Fatalf("role %d: want ErrStaffCannotParticipate, got %v", role, err)
		}
	}
}

func TestJoinEvent_UnpublishedIsRejectedAsNotOpen(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	allowNonStaff(q)
	uc := newUC(q)
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{
		ID: eventID, LifecycleConfigured: true, PublishAt: now.Add(time.Hour), StartAt: now.Add(2 * time.Hour),
	}, nil)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, Registration: int16(eventConfigModel.RegistrationOpen)}, nil)

	_, err := uc.JoinEvent(context.Background(), eventID, userID)
	if !errors.Is(err, participantModel.ErrRegistrationNotOpen.Err()) {
		t.Fatalf("want ErrRegistrationNotOpen, got %v", err)
	}
}
