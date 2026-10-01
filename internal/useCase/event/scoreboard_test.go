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

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/model/rbac"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestGetResultsSnapshotAllowsAnonymousPublicBoard(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})

	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID}).Return(nil, nil)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID}).Return(nil, nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 7, UpdatedAt: now}, nil)

	got, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{}, false)
	if err != nil {
		t.Fatalf("GetResultsSnapshot: %v", err)
	}
	if got.Revision != 7 || !got.GeneratedAt.Equal(now) {
		t.Fatalf("snapshot = %#v", got)
	}
}

func TestGetResultsSnapshotCapturesRevisionBeforeProjection(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	gomock.InOrder(
		q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil),
		q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, time.Now()), nil),
		q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 7, UpdatedAt: now}, nil),
		q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID}).Return(nil, nil),
		q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID}).Return(nil, nil),
	)

	if _, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{}, false); err != nil {
		t.Fatalf("GetResultsSnapshot: %v", err)
	}
}

func TestGetResultsSnapshotRejectsLoggedInNonParticipantOnPrivateBoard(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPrivate), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: userID}).Return(postgres.EventManager{}, pgx.ErrNoRows)
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(postgres.EventParticipant{}, pgx.ErrNoRows)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	// The shared event-wide read includes the revision, even for a denied reader.
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{}, nil)

	_, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{UserID: &userID, Role: rbac.RoleUser}, false)
	if !errors.Is(err, eventConfigModel.ErrResultsParticipantsOnly.Err()) {
		t.Fatalf("err = %v, want ErrResultsParticipantsOnly", err)
	}
}

func TestGetResultsSnapshotRejectsHiddenBoardWithHiddenReason(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityHidden), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	// The shared event-wide read includes the revision, even for a denied reader.
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{}, nil)

	_, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{}, false)
	if !errors.Is(err, eventConfigModel.ErrResultsHidden.Err()) {
		t.Fatalf("err = %v, want ErrResultsHidden", err)
	}
}

func TestGetApprovedParticipantInfoMatchesResultsAccess(t *testing.T) {
	for name, tc := range map[string]struct {
		visibility eventConfigModel.Visibility
		notStarted bool
		want       event.ResultsAvailability
	}{
		"public":             {eventConfigModel.VisibilityPublic, false, event.ResultsAvailable},
		"private":            {eventConfigModel.VisibilityPrivate, false, event.ResultsAvailable},
		"hidden":             {eventConfigModel.VisibilityHidden, false, event.ResultsHidden},
		"public not started": {eventConfigModel.VisibilityPublic, true, event.ResultsNotStarted},
		"hidden not started": {eventConfigModel.VisibilityHidden, true, event.ResultsHidden},
	} {
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			q := newFormGateMock(ctrl)
			uc := event.NewEventUseCase(event.Dependencies{Repo: q})
			eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
			now := time.Now().UTC()
			q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, CreatedAt: now}, nil).AnyTimes()
			q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(tc.visibility), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil).AnyTimes()
			started := startedEvent(eventID, now)
			if tc.notStarted {
				started = notStartedEvent(eventID, now)
			}
			q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil).AnyTimes()

			got, err := uc.GetApprovedParticipantInfo(context.Background(), eventID, userID)
			if err != nil {
				t.Fatalf("GetApprovedParticipantInfo: %v", err)
			}
			if got.ResultsAvailability != tc.want || got.CanViewResults != (tc.want == event.ResultsAvailable) {
				t.Fatalf("info = %+v, want %s", got, tc.want)
			}
		})
	}
}

func TestGetOwnTeamResultsReturnsOnlyCurrentTeamAttempts(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	participant := postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, CreatedAt: now, TeamID: uuid.NullUUID{UUID: teamID, Valid: true}}
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: eventID, UserID: userID}).Return(participant, nil).Times(4)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventManager(gomock.Any(), postgres.GetEventManagerParams{EventID: eventID, UserID: userID}).Return(postgres.EventManager{}, pgx.ErrNoRows)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID}).Return([]postgres.ListEventScoreboardRow{{TeamID: teamID, TeamName: "Own"}}, nil)
	q.EXPECT().ListTeamScoreTimeline(gomock.Any(), teamID).Return(nil, nil)
	q.EXPECT().ListTeamResultAttempts(gomock.Any(), postgres.ListTeamResultAttemptsParams{EventID: eventID, EventTeamID: teamID}).Return([]postgres.ListTeamResultAttemptsRow{{ID: uuid.Must(uuid.NewV7()), EventTeamID: teamID, UserID: userID, ReceivedAt: now}}, nil)

	got, err := uc.GetOwnTeamResults(context.Background(), eventID, userID)
	if err != nil || len(got.Attempts) != 1 || got.Attempts[0].EventTeamID != teamID {
		t.Fatalf("GetOwnTeamResults = %#v, %v", got, err)
	}
}

// Before the start the audience reads the table: the teams, no points yet.
func TestGetResultsSnapshotAllowsGuestBeforeStart(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(notStartedEvent(eventID, now), nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 1, UpdatedAt: now}, nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID}).Return([]postgres.ListEventScoreboardRow{{TeamID: teamID, TeamName: "Alpha"}}, nil)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID}).Return(nil, nil)

	got, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{}, false)
	if err != nil || len(got.Scoreboard) != 1 || got.Scoreboard[0].Points != 0 {
		t.Fatalf("GetResultsSnapshot = %+v, %v", got.Scoreboard, err)
	}
}

func TestGetResultsSnapshotRejectsParticipantsOnlyGuestBeforeStart(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPrivate), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(notStartedEvent(eventID, now), nil)
	// The shared event-wide read includes the revision, even for a denied reader.
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{}, nil)

	_, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{}, false)
	if !errors.Is(err, eventConfigModel.ErrResultsParticipantsOnly.Err()) {
		t.Fatalf("err = %v, want ErrResultsParticipantsOnly", err)
	}
}

func TestGetResultsSnapshotAllowsManagerBeforeStart(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityHidden), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(notStartedEvent(eventID, now), nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 1, UpdatedAt: now}, nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID}).Return(nil, nil)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID}).Return(nil, nil)

	if _, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{Role: rbac.RoleSuperAdmin}, false); err != nil {
		t.Fatalf("GetResultsSnapshot: %v", err)
	}
}

// notStartedEvent is published but starts in an hour.
func notStartedEvent(eventID uuid.UUID, now time.Time) postgres.Event {
	e := startedEvent(eventID, now.Add(2*time.Hour))
	e.PublishAt = now.Add(-time.Hour)
	return e
}
