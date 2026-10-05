package event_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestModeratorsBoardShowsPreparedChallengesUnlocked(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	uc := newUC(q)
	eventID, teamID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	ready, published, preparing := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, CreatedAt: time.Now()}, nil)
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true}, nil).Times(2)
	full := []byte(`{"name":"Web","description":{"blocks":[]},"difficulty":1}`)
	q.EXPECT().ListTeamBoardChallenges(gomock.Any(), gomock.Any()).Return([]postgres.ListTeamBoardChallengesRow{
		{EventChallengeID: ready, Readiness: int16(teamChallengeModel.ReadinessReady), Snapshot: full},
		{EventChallengeID: published, Readiness: int16(teamChallengeModel.ReadinessPublished), Published: true, Snapshot: full},
		{EventChallengeID: preparing, Readiness: int16(teamChallengeModel.ReadinessPreparing), Snapshot: full},
	}, nil)
	q.EXPECT().ListTeamChallengePrerequisites(gomock.Any(), teamID).Return([]postgres.ListTeamChallengePrerequisitesRow{{ChallengeID: ready, PrerequisiteChallengeID: published, Name: "Web"}}, nil)
	board, err := uc.ListModeratorsBoard(context.Background(), eventID)
	if err != nil || len(board) != 2 {
		t.Fatalf("board = %+v, %v", board, err)
	}
	if board[0].EventChallengeID != ready || board[0].BoardPublished || board[0].Locked || len(board[0].Prerequisites) != 1 ||
		!strings.Contains(string(board[0].Snapshot), "description") || board[0].SolveCount != nil {
		t.Fatalf("unpublished ready challenge = %+v", board[0])
	}
	if !board[1].BoardPublished {
		t.Fatalf("published challenge = %+v", board[1])
	}
}

func TestModeratorsBoardWithoutOwnerIsUnavailable(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, CreatedAt: time.Now()}, nil)
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{}, pgx.ErrNoRows).Times(3)
	q.EXPECT().CreateModeratorsTeam(gomock.Any(), gomock.Any()).Return(nil)
	if _, err := newUC(q).ListModeratorsBoard(context.Background(), eventID); !errors.Is(err, eventStandModel.ErrStandModeratorsTeamUnavailable.Err()) {
		t.Fatalf("err = %v", err)
	}
	q = newFormGateMock(gomock.NewController(t))
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{}, pgx.ErrNoRows)
	if _, err := newUC(q).ListModeratorsBoard(context.Background(), eventID); !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
		t.Fatalf("unknown event: %v", err)
	}
}

// The moderators team answers like any team: the attempt and the solve are
// recorded. Its team is hidden, so no live result change is announced.
func TestSubmitModeratorsChallenge_RecordsSolveOfHiddenTeam(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID, teamID, challengeID, teamChallengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
	// Before the start: the moderators test tasks outside the window.
	notStarted := startedEvent(eventID, now)
	notStarted.StartAt = now.Add(time.Hour)
	notStarted.PublishAt = now.Add(-time.Hour)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(notStarted, nil)
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true, Hidden: true}, nil).Times(2)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID}).Return(postgres.GetTeamChallengeRow{ID: teamChallengeID, EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, ExpectedFlag: "ICE{ok}", Readiness: int16(teamChallengeModel.ReadinessReady), CreatedAt: now}, nil)
	expectAttemptWindows(q, eventID, teamID, challengeID, teamChallengeID, now, 0, 0)
	q.EXPECT().CreateChallengeAttempt(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateChallengeAttemptParams) (postgres.ChallengeAttempt, error) {
		if p.EventTeamID != teamID || p.UserID != userID || !p.Correct {
			t.Fatalf("attempt = %+v", p)
		}
		return postgres.ChallengeAttempt{ID: p.ID, EventID: p.EventID, EventTeamID: p.EventTeamID, TeamChallengeID: p.TeamChallengeID, UserID: p.UserID, Correct: p.Correct, ReceivedAt: p.ReceivedAt, CreatedAt: p.CreatedAt}, nil
	})
	q.EXPECT().GetEffectiveTeamChallengeSolvedAt(gomock.Any(), teamChallengeID).Return(postgres.GetEffectiveTeamChallengeSolvedAtRow{Solved: false}, nil)
	q.EXPECT().DeleteTeamChallengeSolve(gomock.Any(), teamChallengeID).Return(nil)
	q.EXPECT().GetEffectiveTeamChallengeSolvedAt(gomock.Any(), teamChallengeID).Return(postgres.GetEffectiveTeamChallengeSolvedAtRow{Solved: true, SolvedAt: now}, nil)
	q.EXPECT().GetTeamChallengeScoringContext(gomock.Any(), teamChallengeID).Return(postgres.GetTeamChallengeScoringContextRow{EventID: eventID, EventChallengeID: challengeID, StaticPoints: 100, EventScoringMode: 0, StartAt: now}, nil)
	q.EXPECT().UpsertTeamChallengeSolve(gomock.Any(), postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: teamChallengeID, SolvedAt: now, AwardedPoints: pgtype.Int4{Int32: 100, Valid: true}}).Return(nil)
	q.EXPECT().GetEventTeamVisible(gomock.Any(), postgres.GetEventTeamVisibleParams{ID: teamID, EventID: eventID}).Return(false, nil)
	q.EXPECT().CompleteRequestIdempotency(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	r, err := uc.SubmitModeratorsChallenge(context.Background(), eventID, userID, challengeID, event.SubmitChallengeInput{Answer: " ICE{ok} ", IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: now})
	if err != nil || !r.Correct || !r.FirstSolve || !unit.saved {
		t.Fatalf("submit=%+v err=%v saved=%v", r, err, unit.saved)
	}
}

func TestSubmitModeratorsChallenge_RefusedOnceWithdrawn(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	withdrawn := startedEvent(eventID, now)
	withdrawn.WithdrawAt = pgtype.Timestamptz{Time: now.Add(-time.Minute), Valid: true}
	withdrawn.FinishAt = pgtype.Timestamptz{Time: now.Add(-time.Hour), Valid: true}
	withdrawn.PublishAt, withdrawn.StartAt = now.Add(-3*time.Hour), now.Add(-2*time.Hour)
	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(withdrawn, nil)
	_, err := uc.SubmitModeratorsChallenge(context.Background(), eventID, userID, uuid.Must(uuid.NewV7()), event.SubmitChallengeInput{Answer: "x", IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: now})
	if !errors.Is(err, eventModel.ErrEventRuntimeNotOpen.Err()) || unit.saved {
		t.Fatalf("err=%v saved=%v", err, unit.saved)
	}
}

func TestGetModeratorsTeamListsManagersByName(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	eventID, teamID, owner, viewer := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, CreatedAt: time.Now()}, nil)
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true}, nil).Times(2)
	q.EXPECT().ListEventManagers(gomock.Any(), eventID).Return([]postgres.EventManager{{EventID: eventID, UserID: owner, Role: 0}, {EventID: eventID, UserID: viewer, Role: 2}}, nil)
	q.EXPECT().GetUserByID(gomock.Any(), owner).Return(postgres.User{ID: owner, FirstName: "Олена", LastName: "Коваль", Email: "o@example.com"}, nil)
	q.EXPECT().GetUserByID(gomock.Any(), viewer).Return(postgres.User{ID: viewer, Email: "v@example.com"}, nil)
	view, err := newUC(q).GetModeratorsTeam(context.Background(), eventID)
	if err != nil || view.TeamID != teamID || len(view.Members) != 2 || view.Members[0].Name != "Олена Коваль" || view.Members[1].Name != "v@example.com" {
		t.Fatalf("view = %+v, %v", view, err)
	}
}

func TestGetModeratorsTeamWithoutOwnerIsUnavailable(t *testing.T) {
	q := newFormGateMock(gomock.NewController(t))
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, CreatedAt: time.Now()}, nil)
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{}, pgx.ErrNoRows).Times(3)
	q.EXPECT().CreateModeratorsTeam(gomock.Any(), gomock.Any()).Return(nil)
	if _, err := newUC(q).GetModeratorsTeam(context.Background(), eventID); !errors.Is(err, eventStandModel.ErrStandModeratorsTeamUnavailable.Err()) {
		t.Fatalf("err = %v", err)
	}
}
