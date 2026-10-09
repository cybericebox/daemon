package event_test

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"strings"
	"testing"
	"time"
)

func publishLifecycleSource(t *testing.T, f *standFixture, set sharedSet) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var previous uuid.UUID
	if err := f.db.Pool.QueryRow(ctx, `SELECT exercise_version_id FROM event_exercises WHERE id=$1`, set.exerciseID).Scan(&previous); err != nil {
		t.Fatal(err)
	}
	exercises := exerciseRepo.New(f.db.Queries)
	version, err := exercises.GetVersion(ctx, previous)
	if err != nil {
		t.Fatal(err)
	}
	version.Variants[0].Tasks[0].Name = "Updated source"
	next, err := exercises.UpsertDraft(ctx, version.ExerciseID, uuid.Must(uuid.NewV7()), version, time.Now(), uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exercises.Publish(ctx, version.ExerciseID, time.Now()); err != nil {
		t.Fatal(err)
	}
	f.sets.topology[next.ID] = f.sets.topology[previous]
	f.sets.links[next.ID] = f.sets.links[previous]
	return next.ID
}
func requireTerminalSourcePreserved(t *testing.T, f *standFixture, set sharedSet, before eventLabModel.Lab, expectedSolves int) {
	t.Helper()
	ctx := context.Background()
	after, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil || after.ID != before.ID || after.AgentUID != before.AgentUID || after.Ref != before.Ref || after.Generation != before.Generation || after.Revision != before.Revision || after.OperationID != before.OperationID || after.CloseReason != "solved" || after.SnapshotMode != before.SnapshotMode || after.SnapshotState != before.SnapshotState || after.Allocation.RuntimeState != before.Allocation.RuntimeState {
		t.Fatalf("source change replaced terminal generation: before=%+v after=%+v err=%v", before, after, err)
	}
	for _, ch := range set.challenges {
		binding, err := labBindingRepo.New(f.db.Queries).Get(ctx, f.blueID, ch)
		if err != nil || !binding.LabID.Valid || binding.LabID.UUID != before.ID || binding.Generation != before.Generation || binding.LabGroupName != before.Ref.Group || binding.LabName != before.Ref.Lab {
			t.Fatalf("terminal binding cleared or rebound: %+v %v", binding, err)
		}
	}
	var labs, solves int
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=$2`, f.blueID, set.exerciseID).Scan(&labs); err != nil || labs != 1 {
		t.Fatal("terminal reprovisioned", labs, err)
	}
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenge_solves s JOIN team_challenges tc ON tc.id=s.team_challenge_id WHERE tc.event_team_id=$1 AND tc.event_challenge_id=ANY($2)`, f.blueID, set.challenges).Scan(&solves); err != nil || solves != expectedSolves {
		t.Fatal("history changed", solves, err)
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	for _, deleted := range f.agent.deleted {
		if deleted == before.Ref.Group+"/"+before.Ref.Lab {
			t.Fatal("terminal generation deleted before lifecycle barrier")
		}
	}
	for _, deployed := range f.agent.deployed {
		if strings.HasPrefix(deployed, before.Ref.Group+"/"+before.Ref.Lab+"-g") {
			t.Fatal("terminal generation redeployed", deployed)
		}
	}
}
func TestLabLifecycleSourceSwitchPreservesSolvedAndRecreatesUnresolvedTeams(t *testing.T) {
	for _, snapshot := range []string{"skip", "required-pending", "required-failed"} {
		t.Run(snapshot, func(t *testing.T) {
			f, set, user, at := prepareLifecycle(t)
			ctx := context.Background()
			for i, ch := range set.challenges {
				submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
			}
			before, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
			if err != nil {
				t.Fatal(err)
			}
			if snapshot != "skip" {
				state, actual := "Pending", "Snapshotting"
				if snapshot == "required-failed" {
					state, actual = "Failed", "StopFailed"
				}
				allocation, _ := json.Marshal(eventLabModel.Allocation{RuntimeState: "Allocated", StorageState: "Retained", AllocatedRequests: eventLabModel.Compute{CPUMillicores: 500, MemoryBytes: 1024}})
				if _, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET snapshot_mode='required',snapshot_state=$2,actual_state=$3,allocation=$4 WHERE id=$1`, before.ID, state, actual, allocation); err != nil {
					t.Fatal(err)
				}
				before, err = eventLabRepo.New(f.db.Queries).Get(ctx, before.ID)
				if err != nil {
					t.Fatal(err)
				}
			}
			redBefore, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.redID, set.challenges[0])
			if err != nil {
				t.Fatal(err)
			}
			next := publishLifecycleSource(t, f, set)
			if _, err = f.uc.ReplaceEventExercise(ctx, f.eventID, set.exerciseID, event.ReplaceEventExerciseInput{ExerciseVersionID: next, RecreateStands: true}, f.ownerID); err != nil {
				t.Fatal(err)
			}
			requireTerminalSourcePreserved(t, f, set, before, 3)
			f.pass(t)
			requireTerminalSourcePreserved(t, f, set, before, 3)
			redAfter, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.redID, set.challenges[0])
			if err != nil || redAfter.ID == redBefore.ID || redAfter.Generation != redBefore.Generation+1 || redAfter.DesiredState != "Running" {
				t.Fatalf("unresolved team did not move: %+v %v", redAfter, err)
			}
		})
	}
}
func TestLabLifecycleSourceSwitchSerializesWithConcurrentFinalSolve(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	submitStoredFlag(t, f, user, set.challenges[0], at)
	submitStoredFlag(t, f, user, set.challenges[1], at.Add(time.Second))
	next := publishLifecycleSource(t, f, set)
	before, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	var tc uuid.UUID
	var flag string
	if err = f.db.Pool.QueryRow(ctx, `SELECT id,expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[2]).Scan(&tc, &flag); err != nil {
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
	if _, err = holder.Exec(ctx, `SELECT id FROM team_challenges WHERE id=$1 FOR UPDATE`, tc); err != nil {
		t.Fatal(err)
	}
	solveDone := make(chan error, 1)
	go func() {
		_, err := f.uc.SubmitChallenge(ctx, f.eventID, user, set.challenges[2], event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at.Add(2 * time.Second)})
		solveDone <- err
	}()
	// Observe the real answer waiting on the question while holding its team/Lab.
	for {
		var waiting bool
		if err = f.db.Pool.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE wait_event_type='Lock' AND $1=ANY(pg_blocking_pids(pid)) AND query LIKE '%GetTeamChallenge%')`, pid).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("answer did not take canonical lock", ctx.Err())
		case <-time.After(5 * time.Millisecond):
		}
	}
	switchDone := make(chan error, 1)
	go func() {
		_, err := f.uc.ReplaceEventExercise(ctx, f.eventID, set.exerciseID, event.ReplaceEventExerciseInput{ExerciseVersionID: next, RecreateStands: true}, f.ownerID)
		switchDone <- err
	}()
	time.Sleep(50 * time.Millisecond)
	if err = holder.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	for _, done := range []chan error{solveDone, switchDone} {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("source/final solve deadlock", ctx.Err())
		}
	}
	before.Revision = 2
	before.CloseReason = "solved"
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, before.ID)
	if err != nil {
		t.Fatal(err)
	}
	before.OperationID = current.OperationID
	requireTerminalSourcePreserved(t, f, set, before, 3)
	f.pass(t)
	requireTerminalSourcePreserved(t, f, set, before, 3)
}

func TestLabLifecycleSourceSwitchPreservesTerminalAfterAnnulment(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	before, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.AnnulSolve(ctx, f.eventID, f.blueID, set.challenges[2], "review", f.ownerID); err != nil {
		t.Fatal(err)
	}
	var snapshot []byte
	var variant int32
	if err = f.db.Pool.QueryRow(ctx, `SELECT snapshot,variant_index FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[0]).Scan(&snapshot, &variant); err != nil {
		t.Fatal(err)
	}
	next := publishLifecycleSource(t, f, set)
	if _, err = f.uc.ReplaceEventExercise(ctx, f.eventID, set.exerciseID, event.ReplaceEventExerciseInput{ExerciseVersionID: next, RecreateStands: true}, f.ownerID); err != nil {
		t.Fatal(err)
	}
	requireTerminalSourcePreserved(t, f, set, before, 2)
	var after []byte
	var afterVariant int32
	if err = f.db.Pool.QueryRow(ctx, `SELECT snapshot,variant_index FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[0]).Scan(&after, &afterVariant); err != nil || string(after) != string(snapshot) || variant != afterVariant {
		t.Fatal("terminal history re-pinned", string(after), afterVariant, err)
	}
	f.pass(t)
	requireTerminalSourcePreserved(t, f, set, before, 2)
}
