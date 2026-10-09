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

	"github.com/cybericebox/daemon/internal/delivery/repository/eventResultRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

func TestDecideSolutionAttempt_Accepted_RecalculatesUsingOriginalAttemptTime(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	attemptID := uuid.Must(uuid.NewV7())
	teamChallengeID := uuid.Must(uuid.NewV7())
	actorID := uuid.Must(uuid.NewV7())
	receivedAt := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	eventChallengeID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventSolutionAttemptIdentity(gomock.Any(), postgres.GetEventSolutionAttemptIdentityParams{ID: attemptID, EventID: eventID}).Return(postgres.GetEventSolutionAttemptIdentityRow{EventTeamID: teamID, EventChallengeID: eventChallengeID}, nil)
	q.EXPECT().GetEventSolutionAttemptForDecision(gomock.Any(), postgres.GetEventSolutionAttemptForDecisionParams{ID: attemptID, EventID: eventID}).Return(postgres.GetEventSolutionAttemptForDecisionRow{ID: attemptID, EventID: eventID, EventTeamID: teamID, TeamChallengeID: teamChallengeID, EventChallengeID: eventChallengeID, Correct: false, ReceivedAt: receivedAt}, nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: eventChallengeID}).Return(postgres.GetTeamChallengeRow{ID: teamChallengeID, EventID: eventID, EventTeamID: teamID, EventChallengeID: eventChallengeID, CreatedAt: receivedAt}, nil)
	q.EXPECT().CreateChallengeAttemptDecision(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, in postgres.CreateChallengeAttemptDecisionParams) (postgres.ChallengeAttemptDecision, error) {
		if in.ChallengeAttemptID != attemptID || in.Decision != int16(challengeAttempt.DecisionAccepted) || in.Reason != "Flag manually verified" || in.DecidedBy != actorID {
			t.Fatalf("decision insert = %+v", in)
		}
		return postgres.ChallengeAttemptDecision{ID: in.ID, ChallengeAttemptID: in.ChallengeAttemptID, Decision: in.Decision, Reason: in.Reason, DecidedBy: in.DecidedBy, DecidedAt: in.DecidedAt}, nil
	})
	q.EXPECT().GetEffectiveTeamChallengeSolvedAt(gomock.Any(), teamChallengeID).Return(postgres.GetEffectiveTeamChallengeSolvedAtRow{Solved: true, SolvedAt: receivedAt}, nil)
	q.EXPECT().GetTeamChallengeScoringContext(gomock.Any(), teamChallengeID).Return(postgres.GetTeamChallengeScoringContextRow{EventID: eventID, EventChallengeID: eventChallengeID, StaticPoints: 100, EventScoringMode: 0, StartAt: receivedAt}, nil)
	q.EXPECT().UpsertTeamChallengeSolve(gomock.Any(), postgres.UpsertTeamChallengeSolveParams{TeamChallengeID: teamChallengeID, SolvedAt: receivedAt, AwardedPoints: pgtype.Int4{Int32: 100, Valid: true}}).Return(nil)
	q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{Revision: 1, UpdatedAt: receivedAt}, nil)
	q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).Return(postgres.EventResultChange{EventID: eventID, Revision: 1, Kind: "team_challenge_solved", CreatedAt: receivedAt}, nil)

	v, err := uc.DecideSolutionAttempt(context.Background(), eventID, attemptID, event.DecideSolutionAttemptInput{Decision: challengeAttempt.DecisionAccepted, Reason: "Flag manually verified", DecidedBy: actorID})
	if err != nil {
		t.Fatalf("DecideSolutionAttempt: %v", err)
	}
	if v.Decision != challengeAttempt.DecisionAccepted || !v.Correct || v.Reason != "Flag manually verified" || !unit.saved {
		t.Fatalf("decision view = %+v, unit = %+v", v, unit)
	}
}

func TestDecideSolutionAttempt_RejectedLastSolveRecordsUnsolvedChange(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID := uuid.Must(uuid.NewV7())
	attemptID := uuid.Must(uuid.NewV7())
	teamChallengeID := uuid.Must(uuid.NewV7())
	eventChallengeID := uuid.Must(uuid.NewV7())
	teamID := uuid.Must(uuid.NewV7())
	actorID := uuid.Must(uuid.NewV7())
	receivedAt := time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC)

	q.EXPECT().GetEventSolutionAttemptIdentity(gomock.Any(), postgres.GetEventSolutionAttemptIdentityParams{ID: attemptID, EventID: eventID}).Return(postgres.GetEventSolutionAttemptIdentityRow{EventTeamID: teamID, EventChallengeID: eventChallengeID}, nil)
	q.EXPECT().GetEventSolutionAttemptForDecision(gomock.Any(), postgres.GetEventSolutionAttemptForDecisionParams{ID: attemptID, EventID: eventID}).Return(postgres.GetEventSolutionAttemptForDecisionRow{ID: attemptID, EventID: eventID, EventTeamID: teamID, TeamChallengeID: teamChallengeID, EventChallengeID: eventChallengeID, Correct: true, ReceivedAt: receivedAt}, nil)
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: eventChallengeID}).Return(postgres.GetTeamChallengeRow{ID: teamChallengeID, EventID: eventID, EventTeamID: teamID, EventChallengeID: eventChallengeID, SolvedAt: pgtype.Timestamptz{Time: receivedAt, Valid: true}, CreatedAt: receivedAt}, nil)
	q.EXPECT().CreateChallengeAttemptDecision(gomock.Any(), gomock.Any()).Return(postgres.ChallengeAttemptDecision{}, nil)
	q.EXPECT().GetEffectiveTeamChallengeSolvedAt(gomock.Any(), teamChallengeID).Return(postgres.GetEffectiveTeamChallengeSolvedAtRow{Solved: false}, nil)
	q.EXPECT().DeleteTeamChallengeSolve(gomock.Any(), teamChallengeID).Return(nil)
	q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{Revision: 2, UpdatedAt: receivedAt}, nil)
	q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, in postgres.CreateEventResultChangeParams) (postgres.EventResultChange, error) {
		if in.EventID != eventID || in.Revision != 2 || in.Kind != string(eventResultRepo.ChangeTeamChallengeUnsolved) {
			t.Fatalf("result change = %+v", in)
		}
		return postgres.EventResultChange{EventID: in.EventID, Revision: in.Revision, Kind: in.Kind, Payload: in.Payload, CreatedAt: in.CreatedAt}, nil
	})

	v, err := uc.DecideSolutionAttempt(context.Background(), eventID, attemptID, event.DecideSolutionAttemptInput{Decision: challengeAttempt.DecisionRejected, Reason: "Incorrect flag", DecidedBy: actorID})
	if err != nil {
		t.Fatalf("DecideSolutionAttempt: %v", err)
	}
	if v.Decision != challengeAttempt.DecisionRejected || v.Correct || !unit.saved {
		t.Fatalf("decision view = %+v, unit = %+v", v, unit)
	}
}

func TestListSolutionAttempts_UnknownCursorIsInvalid(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, cursor := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetEventSolutionAttemptCursor(gomock.Any(), postgres.GetEventSolutionAttemptCursorParams{ID: cursor, EventID: eventID}).Return(postgres.GetEventSolutionAttemptCursorRow{}, pgx.ErrNoRows)

	_, err := uc.ListSolutionAttempts(context.Background(), event.ListSolutionAttemptsFilter{EventID: eventID, Cursor: cursor})
	if !errors.Is(err, challengeAttempt.ErrAttemptCursorInvalid.Err()) {
		t.Fatalf("err = %v, want ErrAttemptCursorInvalid", err)
	}
}

func TestListSolutionAttempts_ReturnsChallengeName(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventSolutionAttempts(gomock.Any(), gomock.Any()).Return([]postgres.ListEventSolutionAttemptsRow{{ID: uuid.Must(uuid.NewV7()), EventID: eventID, ChallengeName: "Warmup", ReceivedAt: time.Now()}}, nil)
	q.EXPECT().CountEventSolutionAttempts(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	got, err := uc.ListSolutionAttempts(context.Background(), event.ListSolutionAttemptsFilter{EventID: eventID})
	if err != nil {
		t.Fatalf("ListSolutionAttempts: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].ChallengeName != "Warmup" {
		t.Fatalf("items = %+v", got.Items)
	}
}
