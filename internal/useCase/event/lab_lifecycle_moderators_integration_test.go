package event_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"strings"
	"testing"
	"time"
)

func requireModeratorsTerminal(t *testing.T, f *standFixture, set sharedSet, team uuid.UUID, before eventLabModel.Lab) {
	t.Helper()
	ctx := context.Background()
	after, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, team, set.challenges[0])
	if err != nil || after.ID != before.ID || after.Ref != before.Ref || after.AgentUID != before.AgentUID || after.Generation != before.Generation || after.Revision != before.Revision || after.OperationID != before.OperationID || after.CloseReason != "solved" {
		t.Fatal("moderator terminal generation changed", after, err)
	}
	for _, ch := range set.challenges {
		binding, err := labBindingRepo.New(f.db.Queries).Get(ctx, team, ch)
		if err != nil || !binding.LabID.Valid || binding.LabID.UUID != before.ID || binding.Generation != before.Generation {
			t.Fatal("moderator terminal binding lost", binding, err)
		}
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	for _, ref := range f.agent.deleted {
		if ref == before.Ref.Group+"/"+before.Ref.Lab {
			t.Fatal("moderator terminal deleted")
		}
	}
}
func solveModeratorsShared(t *testing.T, f *standFixture, set sharedSet, team uuid.UUID, at time.Time) eventLabModel.Lab {
	t.Helper()
	ctx := context.Background()
	for i, ch := range set.challenges {
		var flag string
		if err := f.db.Pool.QueryRow(ctx, `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, team, ch).Scan(&flag); err != nil {
			t.Fatal(err)
		}
		out, err := f.uc.SubmitModeratorsChallenge(ctx, f.eventID, f.ownerID, ch, event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at.Add(time.Duration(i) * time.Second)})
		if err != nil || !out.Correct || out.Lab == nil || out.Lab.LogicalClosed != (i == 2) {
			t.Fatal(out, err)
		}
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, team, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	return lab
}
func TestLabLifecycleFirstModeratorsCreationWaitsForSourceLock(t *testing.T) {
	f, set, _, at := prepareLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := f.db.Pool.Exec(ctx, `DELETE FROM event_teams WHERE event_id=$1 AND moderators`, f.eventID); err != nil {
		t.Fatal(err)
	}
	next := publishLifecycleSource(t, f, set)
	var held uuid.UUID
	if err := f.db.Pool.QueryRow(ctx, `SELECT id FROM event_team_labs WHERE event_id=$1 AND event_exercise_id=$2 ORDER BY id LIMIT 1`, f.eventID, set.exerciseID).Scan(&held); err != nil {
		t.Fatal(err)
	}
	holder, err := f.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	var pid int32
	if err = holder.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	if _, err = holder.Exec(ctx, `SELECT id FROM event_team_labs WHERE id=$1 FOR UPDATE`, held); err != nil {
		t.Fatal(err)
	}
	sourceDone := make(chan error, 1)
	go func() {
		_, err := f.uc.ReplaceEventExercise(ctx, f.eventID, set.exerciseID, event.ReplaceEventExerciseInput{ExerciseVersionID: next, RecreateStands: true}, f.ownerID)
		sourceDone <- err
	}()
	for {
		var waiting bool
		if err = f.db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%LockEventTeamLabsForSourceChange%')`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	boardDone := make(chan error, 1)
	go func() { _, err := f.uc.ListModeratorsBoard(ctx, f.eventID); boardDone <- err }()
	for {
		var waiting bool
		if err = f.db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND query LIKE '%LockEventForTeamChange%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case err := <-boardDone:
			t.Fatalf("moderator creation bypassed in-progress source guard: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	var count int
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_teams WHERE event_id=$1 AND moderators`, f.eventID).Scan(&count); err != nil || count != 0 {
		t.Fatal("moderator inserted into source lock gap", count, err)
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{sourceDone, boardDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("creation/source deadlock", ctx.Err())
		}
	}
	var mod uuid.UUID
	if err = f.db.Pool.QueryRow(ctx, `SELECT id FROM event_teams WHERE event_id=$1 AND moderators`, f.eventID).Scan(&mod); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	var snapshot []byte
	if err = f.db.Pool.QueryRow(ctx, `SELECT snapshot FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, mod, set.challenges[0]).Scan(&snapshot); err != nil || !strings.Contains(string(snapshot), "Updated source") {
		t.Fatal("moderator prepared stale source", string(snapshot), err)
	}
	before := solveModeratorsShared(t, f, set, mod, at)
	f.pass(t)
	requireModeratorsTerminal(t, f, set, mod, before)
}
func TestLabLifecycleConcurrentFirstModeratorsCreationRechecksUnderGuard(t *testing.T) {
	f, _, _, _ := prepareLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if _, err := f.db.Pool.Exec(ctx, `DELETE FROM event_teams WHERE event_id=$1 AND moderators`, f.eventID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `CREATE TABLE moderator_insert_attempts(id bigserial PRIMARY KEY); CREATE FUNCTION record_moderator_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.moderators THEN INSERT INTO moderator_insert_attempts DEFAULT VALUES; END IF; RETURN NEW; END $$; CREATE TRIGGER count_moderator_insert BEFORE INSERT ON event_teams FOR EACH ROW EXECUTE FUNCTION record_moderator_insert()`); err != nil {
		t.Fatal(err)
	}
	holder, err := f.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(ctx, `SELECT id FROM events WHERE id=$1 FOR UPDATE`, f.eventID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 2)
	for range 2 {
		go func() { _, err := f.uc.ListModeratorsBoard(ctx, f.eventID); done <- err }()
	}
	for {
		var waiting int
		if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND (query LIKE '%LockEventForTeamChange%' OR query LIKE '%CreateModeratorsTeam%')`).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting >= 2 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("both creators did not reach guarded miss", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("double creation deadlock", ctx.Err())
		}
	}
	var attempts, teams int
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM moderator_insert_attempts`).Scan(&attempts); err != nil || attempts != 1 {
		t.Fatal("missing guarded recheck", attempts, err)
	}
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_teams WHERE event_id=$1 AND moderators`, f.eventID).Scan(&teams); err != nil || teams != 1 {
		t.Fatal(teams, err)
	}
}
func TestLabLifecycleModeratorsSubmissionUsesCallerTransaction(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing", true: "existing"}[existing], func(t *testing.T) {
			f, _, _, _ := prepareLifecycle(t)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			var mod uuid.UUID
			var flag string
			if err := f.db.Pool.QueryRow(ctx, `SELECT id FROM event_teams WHERE event_id=$1 AND moderators`, f.eventID).Scan(&mod); err != nil {
				t.Fatal(err)
			}
			if err := f.db.Pool.QueryRow(ctx, `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, mod, f.staticChallenge).Scan(&flag); err != nil {
				t.Fatal(err)
			}
			challenge := f.staticChallenge
			if !existing {
				if _, err := f.db.Pool.Exec(ctx, `DELETE FROM event_teams WHERE id=$1`, mod); err != nil {
					t.Fatal(err)
				}
				challenge = uuid.Must(uuid.NewV7())
			}
			cfg := f.db.Pool.Config().Copy()
			cfg.MaxConns = 1
			pool, err := pgxpool.NewWithConfig(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			defer pool.Close()
			uc := event.NewEventUseCase(event.Dependencies{Repo: postgres.New(pool), UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(pool))})
			out, err := uc.SubmitModeratorsChallenge(ctx, f.eventID, f.ownerID, challenge, event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: time.Now()})
			if ctx.Err() != nil {
				t.Fatal("nested/global transaction connection deadlock", ctx.Err())
			}
			if existing {
				if err != nil || !out.Correct {
					t.Fatal(out, err)
				}
			} else {
				if err == nil {
					t.Fatal("missing challenge accepted")
				}
				var count int
				if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_teams WHERE event_id=$1 AND moderators`, f.eventID).Scan(&count); err != nil || count != 0 {
					t.Fatal("failed submission leaked moderator team outside transaction", count, err)
				}
			}
		})
	}
}
func TestLabLifecycleExistingModeratorsTerminalSurvivesSource(t *testing.T) {
	f, set, _, at := prepareLifecycle(t)
	ctx := context.Background()
	var mod uuid.UUID
	if err := f.db.Pool.QueryRow(ctx, `SELECT id FROM event_teams WHERE event_id=$1 AND moderators`, f.eventID).Scan(&mod); err != nil {
		t.Fatal(err)
	}
	before := solveModeratorsShared(t, f, set, mod, at)
	next := publishLifecycleSource(t, f, set)
	if _, err := f.uc.ReplaceEventExercise(ctx, f.eventID, set.exerciseID, event.ReplaceEventExerciseInput{ExerciseVersionID: next, RecreateStands: true}, f.ownerID); err != nil {
		t.Fatal(err)
	}
	requireModeratorsTerminal(t, f, set, mod, before)
	f.pass(t)
	requireModeratorsTerminal(t, f, set, mod, before)
}

func TestLabLifecycleExistingModeratorsReadDoesNotAcquireCreationGuard(t *testing.T) {
	f, _, _, _ := prepareLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	holder, err := f.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Rollback(context.Background())
	if _, err = holder.Exec(ctx, `SELECT id FROM events WHERE id=$1 FOR UPDATE`, f.eventID); err != nil {
		t.Fatal(err)
	}
	// A read-only caller without a transaction factory must still use the existing team.
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries})
	board, err := uc.ListModeratorsBoard(ctx, f.eventID)
	if err != nil || len(board) == 0 || ctx.Err() != nil {
		t.Fatal("existing read took creation/source guard", board, err, ctx.Err())
	}
}
