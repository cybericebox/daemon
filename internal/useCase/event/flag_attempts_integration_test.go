package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	challengeAttempt "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

type flagAttemptFixture struct {
	*standFixture
	uc        *event.EventUseCase
	captainID uuid.UUID
}

// newFlagAttemptFixture opens a static-only event with Blue's captain as an approved participant, and
// widens the rate limit so only the attempt limit is under test.
func newFlagAttemptFixture(t *testing.T) *flagAttemptFixture {
	t.Helper()
	f := newStandFixture(t)
	ctx := context.Background()
	prev := challengeAttempt.ChallengeRateLimit
	challengeAttempt.ChallengeRateLimit = challengeAttempt.RateLimit{Attempts: 1000, Window: time.Second}
	t.Cleanup(func() { challengeAttempt.ChallengeRateLimit = prev })
	if _, err := f.db.Pool.Exec(ctx, `UPDATE events SET infrastructure_allowed = false WHERE id = $1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	f.shiftLifecycle(t, -10*time.Minute, 2*time.Hour)
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)),
		SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now),
	})
	if err := uc.ReconcileEventStands(ctx); err != nil {
		t.Fatal(err)
	}
	var captainID uuid.UUID
	if err := f.db.Pool.QueryRow(ctx, `SELECT captain_id FROM event_teams WHERE id = $1`, f.blueID).Scan(&captainID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role)
		VALUES ($1, $2, $3, now(), $4, 0)`, f.eventID, captainID, int16(participantModel.StatusApproved), f.blueID); err != nil {
		t.Fatal(err)
	}
	return &flagAttemptFixture{standFixture: f, uc: uc, captainID: captainID}
}

func (f *flagAttemptFixture) limits(t *testing.T, eventLimit, taskLimit *int32) {
	t.Helper()
	ctx := context.Background()
	if _, err := f.db.Pool.Exec(ctx, `UPDATE event_configs SET max_flag_attempts = $2 WHERE event_id = $1`, f.eventID, eventLimit); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE event_challenges SET max_flag_attempts = $2 WHERE id = $1`, f.staticChallenge, taskLimit); err != nil {
		t.Fatal(err)
	}
}

func (f *flagAttemptFixture) submit(answer string) (event.SubmitChallengeResult, error) {
	return f.uc.SubmitChallenge(context.Background(), f.eventID, f.captainID, f.staticChallenge, event.SubmitChallengeInput{Answer: answer, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: time.Now()})
}

func (f *flagAttemptFixture) wrong(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		if result, err := f.submit("ICE{wrong}"); err != nil || result.Correct {
			t.Fatalf("wrong attempt %d = %+v, %v", i+1, result, err)
		}
	}
}

func (f *flagAttemptFixture) refused(t *testing.T, answer string) {
	t.Helper()
	if _, err := f.submit(answer); !errors.Is(err, challengeAttempt.ErrAttemptLimitReached.Err()) {
		t.Fatalf("submit %q = %v, want the attempt limit refusal", answer, err)
	}
}

// left reads the participant board: the remaining attempts and the effective limit of the task.
func (f *flagAttemptFixture) left(t *testing.T) (left, max *int32) {
	t.Helper()
	board, err := f.uc.ListOwnChallenges(context.Background(), f.eventID, f.captainID)
	if err != nil || len(board) != 1 {
		t.Fatalf("board = %+v, %v", board, err)
	}
	return board[0].AttemptsLeft, board[0].MaxAttempts
}

func (f *flagAttemptFixture) wantLeft(t *testing.T, want int32) {
	t.Helper()
	if left, _ := f.left(t); left == nil || *left != want {
		t.Fatalf("attempts left = %v, want %d", left, want)
	}
}

func limit(n int32) *int32 { return &n }

func TestFlagAttempts_UnlimitedByDefault(t *testing.T) {
	f := newFlagAttemptFixture(t)
	f.wrong(t, 12)
	if left, max := f.left(t); left != nil || max != nil {
		t.Fatalf("unlimited shows nothing, got left %v max %v", left, max)
	}
}

func TestFlagAttempts_EventLimitRefusesBeforeTheFlagCheck(t *testing.T) {
	f := newFlagAttemptFixture(t)
	f.limits(t, limit(3), nil)
	f.wrong(t, 2)
	f.wantLeft(t, 1)
	f.wrong(t, 1)
	f.wantLeft(t, 0)
	// Even the right flag is refused once no attempts are left, and nothing is stored for it.
	f.refused(t, "ICE{x}")
	f.refused(t, "ICE{wrong}")
	if n := f.count(t, `SELECT count(*) FROM challenge_attempts WHERE event_team_id = $1`, f.blueID); n != 3 {
		t.Fatalf("stored attempts = %d, want 3", n)
	}
	if left, max := f.left(t); left == nil || *left != 0 || max == nil || *max != 3 {
		t.Fatalf("left %v max %v", left, max)
	}
}

func TestFlagAttempts_TaskOverrideHigherAndLower(t *testing.T) {
	f := newFlagAttemptFixture(t)
	f.limits(t, limit(2), limit(4))
	f.wrong(t, 3)
	f.wantLeft(t, 1)
	f.wrong(t, 1)
	f.refused(t, "ICE{wrong}")

	f2 := newFlagAttemptFixture(t)
	f2.limits(t, limit(5), limit(1))
	f2.wrong(t, 1)
	f2.refused(t, "ICE{wrong}")
	// Clearing the override falls back to the event value, and past attempts stay counted.
	f2.limits(t, limit(5), nil)
	f2.wantLeft(t, 4)
}

func TestFlagAttempts_ChangingTheLimitAppliesAtOnce(t *testing.T) {
	f := newFlagAttemptFixture(t)
	f.limits(t, limit(2), nil)
	f.wrong(t, 2)
	f.refused(t, "ICE{wrong}")
	f.limits(t, limit(3), nil)
	f.wantLeft(t, 1)
	f.wrong(t, 1)
	f.limits(t, limit(1), nil)
	f.refused(t, "ICE{wrong}")
	if left, _ := f.left(t); left == nil || *left != 0 {
		t.Fatalf("a limit under the past attempts leaves 0, got %v", left)
	}
	f.limits(t, nil, nil)
	if left, _ := f.left(t); left != nil {
		t.Fatalf("unlimited again: %v", left)
	}
	f.wrong(t, 1)
}

func TestFlagAttempts_SolvedStopsCounting(t *testing.T) {
	f := newFlagAttemptFixture(t)
	f.limits(t, limit(2), nil)
	f.wrong(t, 1)
	if result, err := f.submit("ICE{x}"); err != nil || !result.Correct || !result.FirstSolve {
		t.Fatalf("the solve = %+v, %v", result, err)
	}
	if left, max := f.left(t); left != nil || max != nil {
		t.Fatalf("a solved task shows no attempts, got %v %v", left, max)
	}
	// Later wrong submissions are not limited and, after the solve, not counted.
	f.wrong(t, 4)
	var wrongBeforeSolve int
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT wrong FROM (
		SELECT count(*) AS wrong FROM effective_challenge_attempts a JOIN team_challenge_solves s ON s.team_challenge_id = a.team_challenge_id
		WHERE NOT a.effective_correct AND a.received_at < s.solved_at) x`).Scan(&wrongBeforeSolve); err != nil || wrongBeforeSolve != 1 {
		t.Fatalf("wrong before the solve = %d, %v", wrongBeforeSolve, err)
	}
}

func TestFlagAttempts_RateLimitedSubmissionIsNotAnAttempt(t *testing.T) {
	f := newFlagAttemptFixture(t)
	f.limits(t, limit(5), nil)
	challengeAttempt.ChallengeRateLimit = challengeAttempt.RateLimit{Attempts: 2, Window: time.Hour}
	f.wrong(t, 2)
	if _, err := f.submit("ICE{wrong}"); !errors.Is(err, challengeAttempt.ErrTooManyAttempts.Err()) {
		t.Fatalf("third = %v, want the rate limit", err)
	}
	f.wantLeft(t, 3)
	if n := f.count(t, `SELECT count(*) FROM challenge_attempts WHERE event_team_id = $1`, f.blueID); n != 2 {
		t.Fatalf("stored attempts = %d, want 2", n)
	}
}

func TestFlagAttempts_JournalShowsUsedAndAllowed(t *testing.T) {
	f := newFlagAttemptFixture(t)
	f.limits(t, limit(4), nil)
	f.wrong(t, 2)
	list, err := f.uc.ListSolutionAttempts(context.Background(), event.ListSolutionAttemptsFilter{EventID: f.eventID})
	if err != nil || len(list.Items) != 2 {
		t.Fatalf("journal = %+v, %v", list, err)
	}
	for _, item := range list.Items {
		if item.AttemptsAllowed == nil || *item.AttemptsAllowed != 4 || item.AttemptsUsed != 2 {
			t.Fatalf("journal row = %+v", item)
		}
	}
}
