package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

type submitFixture struct {
	q                                                          *postgresMocks.MockQuerier
	unit                                                       *testUoW
	uc                                                         *event.EventUseCase
	eventID, userID, teamID, challengeID, teamChallengeID, key uuid.UUID
	now                                                        time.Time
}

// newSubmitFixture stubs everything a participant submission reads before the stage gate.
func newSubmitFixture(t *testing.T, phase int16) *submitFixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	f := &submitFixture{q: q, unit: unit, uc: event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}}),
		eventID: uuid.Must(uuid.NewV7()), userID: uuid.Must(uuid.NewV7()), teamID: uuid.Must(uuid.NewV7()), challengeID: uuid.Must(uuid.NewV7()),
		teamChallengeID: uuid.Must(uuid.NewV7()), key: uuid.Must(uuid.NewV7()), now: time.Now()}
	q.EXPECT().GetEventParticipant(gomock.Any(), postgres.GetEventParticipantParams{EventID: f.eventID, UserID: f.userID}).Return(postgres.EventParticipant{EventID: f.eventID, UserID: f.userID, Status: 2, TeamID: uuid.NullUUID{UUID: f.teamID, Valid: true}, CreatedAt: f.now}, nil).AnyTimes()
	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), f.eventID).Return(startedEvent(f.eventID, f.now), nil).AnyTimes()
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: f.teamID, EventChallengeID: f.challengeID}).Return(postgres.GetTeamChallengeRow{ID: f.teamChallengeID, EventID: f.eventID, EventTeamID: f.teamID, EventChallengeID: f.challengeID, ExpectedFlag: "ICE{ok}", Readiness: 2, CreatedAt: f.now}, nil)
	q.EXPECT().GetEventChallengeAccess(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.GetEventChallengeAccessParams) (postgres.GetEventChallengeAccessRow, error) {
		// the gate is judged at the request time
		if !p.At.Equal(f.now) {
			t.Errorf("the stage gate must use ReceivedAt, got %v want %v", p.At, f.now)
		}
		return postgres.GetEventChallengeAccessRow{Published: true, Phase: phase}, nil
	})
	return f
}

func (f *submitFixture) submit(answer string) (event.SubmitChallengeResult, error) {
	return f.uc.SubmitChallenge(context.Background(), f.eventID, f.userID, f.challengeID, event.SubmitChallengeInput{Answer: answer, IdempotencyKey: f.key, ReceivedAt: f.now})
}

func TestSubmitChallenge_UpcomingStageIsHidden(t *testing.T) {
	f := newSubmitFixture(t, 0)
	if _, err := f.submit("ICE{ok}"); !errors.Is(err, eventChallengeModel.ErrEventChallengeNotFound.Err()) || f.unit.saved {
		t.Fatalf("err=%v saved=%v", err, f.unit.saved)
	}
}

func TestSubmitChallenge_ClosedStageIsRefused(t *testing.T) {
	f := newSubmitFixture(t, 3)
	if _, err := f.submit("ICE{ok}"); !errors.Is(err, eventChallengeModel.ErrEventChallengeStageClosed.Err()) || f.unit.saved {
		t.Fatalf("err=%v saved=%v", err, f.unit.saved)
	}
}

// After a returnable stage closed the answer is verified and stored as practice: no solve, no award, no first
// blood, no result change; a practice solve is noted. The rate limits still apply (the windows are read).
func TestSubmitChallenge_ReturnableStageAfterCloseIsPractice(t *testing.T) {
	for name, correct := range map[string]bool{"correct": true, "wrong": false} {
		t.Run(name, func(t *testing.T) {
			f := newSubmitFixture(t, 2)
			f.q.EXPECT().ListEventChallengePrerequisites(gomock.Any(), f.challengeID).Return(nil, nil)
			expectAttemptWindows(f.q, f.eventID, f.teamID, f.challengeID, f.teamChallengeID, f.now, 1, 1)
			f.q.EXPECT().CreateChallengeAttempt(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateChallengeAttemptParams) (postgres.ChallengeAttempt, error) {
				if !p.Practice || p.Correct != correct {
					t.Errorf("attempt must be stored as practice: %+v", p)
				}
				return postgres.ChallengeAttempt{ID: p.ID, Practice: p.Practice, Correct: p.Correct}, nil
			})
			if correct {
				f.q.EXPECT().UpsertTeamChallengePracticeSolve(gomock.Any(), gomock.Any()).Return(nil)
			}
			f.q.EXPECT().CompleteRequestIdempotency(gomock.Any(), gomock.Any()).Return(int64(1), nil)
			answer := "wrong"
			if correct {
				answer = "ICE{ok}"
			}
			// no UpsertTeamChallengeSolve / result change / solved projection expectations: any call fails the test
			r, err := f.submit(answer)
			if err != nil || r.Correct != correct || !r.Practice || r.FirstSolve || !f.unit.saved {
				t.Fatalf("submit=%+v err=%v saved=%v", r, err, f.unit.saved)
			}
		})
	}
}

func TestSubmitChallenge_PracticeStillHitsTheRateLimit(t *testing.T) {
	f := newSubmitFixture(t, 2)
	f.q.EXPECT().ListEventChallengePrerequisites(gomock.Any(), f.challengeID).Return(nil, nil)
	expectAttemptWindows(f.q, f.eventID, f.teamID, f.challengeID, f.teamChallengeID, f.now, 5, 0)
	expectRejectionLogged(f.q, f.eventID, f.userID, f.teamID, f.challengeID, f.now, "rate_limit")
	if _, err := f.submit("ICE{ok}"); err == nil || f.unit.saved {
		t.Fatalf("a practice attempt must be throttled like any other: err=%v saved=%v", err, f.unit.saved)
	}
}

// The moderators team tests tasks before they are shown, so it bypasses the stage gate (no access read at all).
func TestSubmitModeratorsChallenge_BypassesTheStageGate(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, userID, teamID, challengeID, teamChallengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now()
	q.EXPECT().ReserveRequestIdempotency(gomock.Any(), gomock.Any()).Return([]postgres.RequestIdempotency{{}}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil).AnyTimes()
	q.EXPECT().GetModeratorsTeam(gomock.Any(), eventID).Return(postgres.EventTeam{ID: teamID, EventID: eventID, Moderators: true}, nil).AnyTimes()
	q.EXPECT().GetTeamChallenge(gomock.Any(), postgres.GetTeamChallengeParams{EventTeamID: teamID, EventChallengeID: challengeID}).Return(postgres.GetTeamChallengeRow{ID: teamChallengeID, EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID, ExpectedFlag: "ICE{ok}", Readiness: 1, CreatedAt: now}, nil)
	q.EXPECT().GetEventChallengeAccess(gomock.Any(), gomock.Any()).Times(0)
	q.EXPECT().LockEventTeamChallenge(gomock.Any(), gomock.Any()).Return(teamChallengeID, nil)
	q.EXPECT().GetTeamChallengeAttemptWindow(gomock.Any(), gomock.Any()).Return(postgres.GetTeamChallengeAttemptWindowRow{}, nil)
	q.EXPECT().GetTeamAttemptWindow(gomock.Any(), gomock.Any()).Return(postgres.GetTeamAttemptWindowRow{}, nil)
	q.EXPECT().CreateChallengeAttempt(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateChallengeAttemptParams) (postgres.ChallengeAttempt, error) {
		if p.Practice {
			t.Error("a moderators attempt is never practice")
		}
		return postgres.ChallengeAttempt{ID: p.ID}, nil
	})
	q.EXPECT().CompleteRequestIdempotency(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	r, err := uc.SubmitModeratorsChallenge(context.Background(), eventID, userID, challengeID, event.SubmitChallengeInput{Answer: "wrong", IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: now})
	if err != nil || r.Correct || r.Practice || !unit.saved {
		t.Fatalf("submit=%+v err=%v saved=%v", r, err, unit.saved)
	}
}

func hintStageFixture(t *testing.T, phase int16) (*event.EventUseCase, *postgresMocks.MockQuerier, *testUoW, hintFixture) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	f := newHintFixture()
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).
		Return(postgres.EventParticipant{EventID: f.eventID, UserID: f.userID, Status: 2, TeamID: uuid.NullUUID{UUID: f.teamID, Valid: true}, CreatedAt: f.now}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), f.eventID).Return(startedEvent(f.eventID, f.now), nil)
	q.EXPECT().GetEventConfig(gomock.Any(), f.eventID).Return(postgres.EventConfig{EventID: f.eventID, HintChargeMode: 1}, nil)
	q.EXPECT().GetTeamChallengeHints(gomock.Any(), gomock.Any()).
		Return(postgres.GetTeamChallengeHintsRow{ID: f.teamChallengeID, EventID: f.eventID, EventTeamID: f.teamID, EventChallengeID: f.challengeID, Readiness: 2, StagePhase: phase,
			TeamHints: []byte(`[{"id":"` + f.hintID.String() + `","text":"Variant text"}]`), BoardHints: []byte(`[{"id":"` + f.hintID.String() + `","cost":20,"text":"Canonical"}]`),
			HintsEnabled: true, Published: true}, nil)
	return uc, q, unit, f
}

func TestUnlockHint_UpcomingStageIsHiddenAndClosedIsRefused(t *testing.T) {
	uc, _, unit, f := hintStageFixture(t, 0)
	if _, err := uc.UnlockHint(context.Background(), f.eventID, f.userID, f.challengeID, f.hintID); !errors.Is(err, eventChallengeModel.ErrEventChallengeNotFound.Err()) || unit.saved {
		t.Fatalf("upcoming: err=%v saved=%v", err, unit.saved)
	}
	uc, _, unit, f = hintStageFixture(t, 3)
	if _, err := uc.UnlockHint(context.Background(), f.eventID, f.userID, f.challengeID, f.hintID); !errors.Is(err, eventChallengeModel.ErrEventChallengeStageClosed.Err()) || unit.saved {
		t.Fatalf("closed: err=%v saved=%v", err, unit.saved)
	}
}

// After a returnable stage ended an unlock is allowed and free: the rating is frozen, so no result change either.
func TestUnlockHint_ReturnableStageAfterCloseIsFree(t *testing.T) {
	uc, q, unit, f := hintStageFixture(t, 2)
	q.EXPECT().ListTeamChallengePrerequisites(gomock.Any(), f.teamID).Return(nil, nil)
	q.EXPECT().CreateHintUnlock(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, arg postgres.CreateHintUnlockParams) (postgres.CreateHintUnlockRow, error) {
		if arg.Cost != 0 {
			t.Errorf("an unlock after the stage closed must cost 0, got %d", arg.Cost)
		}
		return postgres.CreateHintUnlockRow{TeamChallengeID: arg.TeamChallengeID, HintID: arg.HintID, UnlockedBy: arg.UnlockedBy, UnlockedAt: arg.UnlockedAt, Cost: arg.Cost, Created: true}, nil
	})
	hint, err := uc.UnlockHint(context.Background(), f.eventID, f.userID, f.challengeID, f.hintID)
	if err != nil || !hint.Unlocked || hint.Cost != 0 || !unit.saved {
		t.Fatalf("hint=%+v err=%v saved=%v", hint, err, unit.saved)
	}
}
