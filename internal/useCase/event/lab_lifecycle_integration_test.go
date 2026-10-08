package event_test

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	challengeAttemptModel "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"testing"
	"time"
)

func approveBlueCaptain(t *testing.T, f *standFixture, at time.Time) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var user uuid.UUID
	if err := f.db.Pool.QueryRow(ctx, `SELECT captain_id FROM event_teams WHERE id=$1`, f.blueID).Scan(&user); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `INSERT INTO event_participants(event_id,user_id,status,team_id,team_role,created_at) VALUES($1,$2,2,$3,0,$4) ON CONFLICT(event_id,user_id) DO UPDATE SET status=2,team_id=$3,team_role=0`, f.eventID, user, f.blueID, at); err != nil {
		t.Fatal(err)
	}
	return user
}
func submitStoredFlag(t *testing.T, f *standFixture, user, challenge uuid.UUID, at time.Time) event.SubmitChallengeResult {
	t.Helper()
	var flag string
	if err := f.db.Pool.QueryRow(context.Background(), `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, challenge).Scan(&flag); err != nil {
		t.Fatal(err)
	}
	out, err := f.uc.SubmitChallenge(context.Background(), f.eventID, user, challenge, event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func prepareLifecycle(t *testing.T) (*standFixture, sharedSet, uuid.UUID, time.Time) {
	t.Helper()
	f := newStandFixture(t)
	set := f.attachSharedSet(t)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.shiftLifecycle(t, -time.Minute, time.Hour)
	f.pass(t)
	at := time.Now()
	user := approveBlueCaptain(t, f, at)
	return f, set, user, at
}
func TestSharedLabCompletionClosesOnlyFinalObjectiveAtomically(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	for i, ch := range set.challenges {
		out := submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
		if !out.Correct || !out.FirstSolve || out.Lab == nil || out.Lab.LogicalClosed != (i == 2) {
			t.Fatalf("answer %d: %+v", i, out)
		}
		lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, ch)
		if err != nil || (lab.ClosedAt != nil) != (i == 2) {
			t.Fatalf("lab %d: %+v %v", i, lab, err)
		}
		if i == 2 && (lab.CloseReason != "solved" || lab.Revision != 2 || lab.AgentUID == "") {
			t.Fatalf("terminal lab: %+v", lab)
		}
		f.agent.mu.Lock()
		stops := len(f.agent.stopCalls)
		f.agent.mu.Unlock()
		if stops != 0 {
			t.Fatal("RPC in answer", stops)
		}
	}
	var solves, closed, dirty int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenge_solves s JOIN team_challenges tc ON tc.id=s.team_challenge_id WHERE tc.event_team_id=$1 AND tc.event_challenge_id=ANY($2)`, f.blueID, set.challenges).Scan(&solves); err != nil || solves != 3 {
		t.Fatal(solves, err)
	}
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_team_labs WHERE logical_closed_at IS NOT NULL`).Scan(&closed); err != nil || closed != 1 {
		t.Fatal(closed, err)
	}
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_lab_access_syncs WHERE event_team_id=$1 AND desired_revision>applied_revision`, f.blueID).Scan(&dirty); err != nil || dirty != 1 {
		t.Fatal(dirty, err)
	}
}
func TestLabLifecycleConcurrentFinalAnswers(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	submitStoredFlag(t, f, user, set.challenges[0], at)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := make(chan struct{})
	done := make(chan error, 2)
	for _, ch := range set.challenges[1:] {
		go func(ch uuid.UUID) {
			var flag string
			err := f.db.Pool.QueryRow(ctx, `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, ch).Scan(&flag)
			<-start
			if err == nil {
				_, err = f.uc.SubmitChallenge(ctx, f.eventID, user, ch, event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at.Add(time.Second)})
			}
			done <- err
		}(ch)
	}
	close(start)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("lock deadlock", ctx.Err())
		}
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.blueID, set.challenges[0])
	if err != nil || lab.Revision != 2 || lab.CloseReason != "solved" {
		t.Fatal(lab, err)
	}
}

func TestLabLifecycleLostWakeAcceptedStopNeverCreditsRelease(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	f.uc.SetLabLifecycleWake(func(context.Context) error { return fmt.Errorf("lost wake") })
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	// A fresh use case has no wake or in-memory retry state.
	restarted := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, Infra: f.agent, InfrastructureCapability: standCapability{}})
	if err := restarted.ReconcilePendingLabLifecycles(ctx); err != nil {
		t.Fatal(err)
	}
	f.agent.mu.Lock()
	stops := append([]eventLabModel.StopRequest(nil), f.agent.stopCalls...)
	f.agent.mu.Unlock()
	if len(stops) != 1 || !stops[0].Terminal || stops[0].Target.ExpectedUID == "" || stops[0].Target.Revision != 2 {
		t.Fatal(stops)
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil || lab.Allocation.RuntimeState == "Released" || lab.ObservedRevision == 2 {
		t.Fatal(lab, err)
	}
	dirty, err := eventLabRepo.New(f.db.Queries).PendingStopped(ctx, time.Now().Add(time.Minute), 100)
	if err != nil || len(dirty) != 1 {
		t.Fatal(dirty, err)
	}
	// The producer observation alone acknowledges an exact fenced release.
	observedAt := time.Now()
	ref := lab.Ref
	f.agent.mu.Lock()
	f.agent.observations = map[eventLabModel.Ref]eventLabModel.Observation{ref: {Ref: ref, UID: lab.AgentUID, Generation: 2, ObservedGeneration: 2, OperationID: lab.OperationID, Revision: 2, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: "NotRequired", AccessFenced: true, AccessFencedAt: &observedAt, AccessFenceVPNBootID: "boot", StoppedAt: &observedAt, ObservedAt: &observedAt, Allocation: eventLabModel.Allocation{RuntimeState: "Released", StorageState: "None", ReleasedAt: &observedAt}}}
	f.agent.mu.Unlock()
	if _, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET next_attempt_at=$1 WHERE id=$2`, time.Now(), lab.ID); err != nil {
		t.Fatal(err)
	}
	if err = restarted.ReconcilePendingLabLifecycles(ctx); err != nil {
		t.Fatal(err)
	}
	lab, err = eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
	if err != nil || lab.Allocation.RuntimeState != "Released" || lab.ObservedRevision != 2 {
		t.Fatal(lab, err)
	}
	dirty, err = eventLabRepo.New(f.db.Queries).PendingStopped(ctx, time.Now().Add(time.Minute), 100)
	if err != nil || len(dirty) != 0 {
		t.Fatal(dirty, err)
	}
}

func TestLabLifecycleSolvedIsTerminalAcrossAnnulAndRecreate(t *testing.T) {
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
	if _, err = f.uc.RecreateTeamStand(ctx, f.eventID, f.blueID, f.ownerID); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	after, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil || after.ID != before.ID || after.Ref != before.Ref || after.Revision != 2 || after.CloseReason != "solved" {
		t.Fatalf("terminal changed: before=%+v after=%+v err=%v", before, after, err)
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	for _, deleted := range f.agent.deleted {
		if deleted == before.Ref.Group+"/"+before.Ref.Lab {
			t.Fatal("solved lab recreated/deleted")
		}
	}
}
func TestLabLifecyclePracticeCompletesWithoutRating(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	stage, err := f.uc.CreateEventStage(ctx, f.eventID, event.CreateStageInput{Name: "Practice", OpensAt: at.Add(time.Minute), ClosesAt: at.Add(2 * time.Minute), Returnable: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(ctx, `UPDATE event_stages SET opens_at=$2,closes_at=$3 WHERE id=$1`, stage.ID, at.Add(-time.Hour), at.Add(-time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(ctx, `UPDATE event_exercises SET stage_id=$2 WHERE id=$1`, set.exerciseID, stage.ID); err != nil {
		t.Fatal(err)
	}
	var revision int64
	if err = f.db.Pool.QueryRow(ctx, `SELECT COALESCE((SELECT revision FROM event_result_revisions WHERE event_id=$1),0)`, f.eventID).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	for i, ch := range set.challenges {
		out := submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
		if !out.Practice || out.FirstSolve || out.Lab.LogicalClosed != (i == 2) {
			t.Fatal(out)
		}
	}
	var solves int
	var after int64
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenge_solves s JOIN team_challenges tc ON tc.id=s.team_challenge_id WHERE tc.event_team_id=$1 AND tc.event_challenge_id=ANY($2)`, f.blueID, set.challenges).Scan(&solves); err != nil || solves != 0 {
		t.Fatal(solves, err)
	}
	if err = f.db.Pool.QueryRow(ctx, `SELECT COALESCE((SELECT revision FROM event_result_revisions WHERE event_id=$1),0)`, f.eventID).Scan(&after); err != nil || after != revision {
		t.Fatal(after, revision, err)
	}
}
func TestLabLifecycleMissingObjectiveNeverCloses(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	if _, err := f.db.Pool.Exec(ctx, `DELETE FROM lab_bindings WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[2]); err != nil {
		t.Fatal(err)
	}
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil || lab.ClosedAt != nil || lab.Revision != 1 {
		t.Fatal(lab, err)
	}
}
func TestLabLifecycleModeratorAcceptedFinalAndReplayRefresh(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	var flag string
	if err := f.db.Pool.QueryRow(ctx, `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[0]).Scan(&flag); err != nil {
		t.Fatal(err)
	}
	in := event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at}
	original, err := f.uc.SubmitChallenge(ctx, f.eventID, user, set.challenges[0], in)
	if err != nil || original.Lab.LogicalClosed {
		t.Fatal(original, err)
	}
	submitStoredFlag(t, f, user, set.challenges[1], at.Add(time.Second))
	out, err := f.uc.SubmitChallenge(ctx, f.eventID, user, set.challenges[2], event.SubmitChallengeInput{Answer: "wrong", IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at.Add(2 * time.Second)})
	if err != nil || out.Correct || out.Lab.LogicalClosed {
		t.Fatal(out, err)
	}
	var attempt uuid.UUID
	if err = f.db.Pool.QueryRow(ctx, `SELECT a.id FROM challenge_attempts a JOIN team_challenges tc ON tc.id=a.team_challenge_id WHERE tc.event_team_id=$1 AND tc.event_challenge_id=$2`, f.blueID, set.challenges[2]).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	if _, err = f.uc.DecideSolutionAttempt(ctx, f.eventID, attempt, event.DecideSolutionAttemptInput{Decision: challengeAttemptModel.DecisionAccepted, Reason: "accepted", DecidedBy: f.ownerID}); err != nil {
		t.Fatal(err)
	}
	replay, err := f.uc.SubmitChallenge(ctx, f.eventID, user, set.challenges[0], in)
	if err != nil || !replay.Correct || !replay.FirstSolve || !replay.Lab.LogicalClosed || replay.Lab.Revision != "2" {
		t.Fatal(replay, err)
	}
}

func TestLabLifecycleReadinessRequiresIdentityAndRepairsColdReadyBinding(t *testing.T) {
	f := newStandFixture(t)
	set := f.attachSharedSet(t)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.shiftLifecycle(t, -time.Minute, time.Hour)
	f.pass(t)
	ctx := context.Background()
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET agent_uid='',agent_generation=0 WHERE id=$1`, lab.ID); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	lab, err = eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
	if err != nil || lab.AgentUID == "" || lab.AgentGeneration <= 0 {
		t.Fatal("cold ready binding identity not repaired", lab, err)
	}
}

func TestLabLifecycleOutageBackoffSurvivesRestart(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	f.agent.stopErr = fmt.Errorf("agent unsupported")
	for i, want := range []time.Duration{10 * time.Second, 20 * time.Second, 40 * time.Second} {
		restarted := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, Infra: f.agent, InfrastructureCapability: standCapability{}})
		if err := restarted.ReconcilePendingLabLifecycles(ctx); err == nil {
			t.Fatal("unsupported acknowledged")
		}
		lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
		if err != nil || lab.NextAttemptAt.Sub(lab.UpdatedAt) != want || lab.ObservedRevision == lab.Revision || lab.Allocation.RuntimeState == "Released" {
			t.Fatal(i, lab, err)
		}
		// Advance the persisted schedule (both timestamps preserve its delay) without sleeping.
		if _, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET next_attempt_at=$1::timestamptz,updated_at=$1::timestamptz-$2::interval WHERE id=$3`, time.Now().Add(-time.Second), fmt.Sprintf("%f seconds", want.Seconds()), lab.ID); err != nil {
			t.Fatal(err)
		}
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	if len(f.agent.stopCalls) != 3 {
		t.Fatal(f.agent.stopCalls)
	}
	for _, req := range f.agent.stopCalls[1:] {
		if req.Target != f.agent.stopCalls[0].Target || !req.Terminal {
			t.Fatal("retry identity changed", req)
		}
	}
}
func TestLabLifecycleRequiredFailureNeverCreditsMatchingRelease(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	held := eventLabModel.Allocation{RuntimeState: "Allocated", StorageState: "Retained", AllocatedRequests: eventLabModel.Compute{CPUMillicores: 120, MemoryBytes: 1024}}
	encoded, _ := json.Marshal(held)
	if _, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET snapshot_mode='required',allocation=$2 WHERE id=$1`, lab.ID, encoded); err != nil {
		t.Fatal(err)
	}
	obsAt := time.Now()
	f.agent.observations = map[eventLabModel.Ref]eventLabModel.Observation{lab.Ref: {Ref: lab.Ref, UID: lab.AgentUID, Generation: 2, ObservedGeneration: 2, OperationID: lab.OperationID, Revision: 2, DesiredState: "Stopped", ActualState: "StopFailed", SnapshotState: "Failed", FailureCode: "SnapshotFailed", AccessFenced: true, ObservedAt: &obsAt, Allocation: eventLabModel.Allocation{RuntimeState: "Released", ReleasedAt: &obsAt}}}
	if err = f.uc.ReconcilePendingLabLifecycles(ctx); err == nil {
		t.Fatal("snapshot failure not reported")
	}
	lab, err = eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
	if err != nil || lab.ActualState != "StopFailed" || lab.Allocation.RuntimeState != "Allocated" || lab.Allocation.AllocatedRequests != held.AllocatedRequests || lab.Allocation.ReleasedAt != nil {
		t.Fatal(lab, err)
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	if len(f.agent.stopCalls) != 1 || f.agent.stopCalls[0].SnapshotMode != "required" {
		t.Fatal(f.agent.stopCalls)
	}
}
func TestLabLifecycleStaleCertificateNeverCreditsRelease(t *testing.T) {
	for _, mismatch := range []string{"uid", "revision", "operation", "generation", "fence"} {
		t.Run(mismatch, func(t *testing.T) {
			f, set, user, at := prepareLifecycle(t)
			ctx := context.Background()
			for i, ch := range set.challenges {
				submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
			}
			lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
			if err != nil {
				t.Fatal(err)
			}
			obsAt := time.Now()
			obs := eventLabModel.Observation{Ref: lab.Ref, UID: lab.AgentUID, Generation: 2, ObservedGeneration: 2, OperationID: lab.OperationID, Revision: 2, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: "NotRequired", AccessFenced: true, ObservedAt: &obsAt, StoppedAt: &obsAt, Allocation: eventLabModel.Allocation{RuntimeState: "Released", ReleasedAt: &obsAt}}
			switch mismatch {
			case "uid":
				obs.UID = "foreign"
			case "revision":
				obs.Revision = 1
			case "operation":
				obs.OperationID = uuid.Must(uuid.NewV7())
			case "generation":
				obs.ObservedGeneration = 1
			case "fence":
				obs.AccessFenced = false
			}
			f.agent.observations = map[eventLabModel.Ref]eventLabModel.Observation{lab.Ref: obs}
			if err = f.uc.ReconcilePendingLabLifecycles(ctx); err != nil {
				t.Fatal(err)
			}
			lab, err = eventLabRepo.New(f.db.Queries).Get(ctx, lab.ID)
			if err != nil || lab.Allocation.RuntimeState == "Released" {
				t.Fatal(lab, err)
			}
		})
	}
}

func TestLabLifecycleFailureDoesNotBlockRemainingBoundedBatch(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	another := f.attachSharedSet(t)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	for i, ch := range another.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i+3)*time.Second))
	}
	first, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	f.agent.stopErrs = map[eventLabModel.Ref]error{first.Ref: fmt.Errorf("unavailable")}
	restarted := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, Infra: f.agent, InfrastructureCapability: standCapability{}, LabLifecycleBatch: 2})
	if err = restarted.ReconcilePendingLabLifecycles(ctx); err == nil {
		t.Fatal("failure not collected")
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	if len(f.agent.stopCalls) != 2 {
		t.Fatal("failure blocked later item", f.agent.stopCalls)
	}
}

func TestLabLifecycleFinalSolveClosureAndACLIntentRollbackTogether(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	submitStoredFlag(t, f, user, set.challenges[0], at)
	submitStoredFlag(t, f, user, set.challenges[1], at.Add(time.Second))
	if _, err := f.db.Pool.Exec(ctx, `ALTER TABLE event_lab_access_syncs ADD CONSTRAINT reject_dirty_revision CHECK (desired_revision < 1) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	var flag string
	if err := f.db.Pool.QueryRow(ctx, `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[2]).Scan(&flag); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.SubmitChallenge(ctx, f.eventID, user, set.challenges[2], event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at.Add(2 * time.Second)}); err == nil {
		t.Fatal("injected ACL persistence failure ignored")
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil || lab.Revision != 1 || lab.ClosedAt != nil {
		t.Fatal(lab, err)
	}
	var count int
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenge_solves s JOIN team_challenges tc ON tc.id=s.team_challenge_id WHERE tc.event_team_id=$1 AND tc.event_challenge_id=$2`, f.blueID, set.challenges[2]).Scan(&count); err != nil || count != 0 {
		t.Fatal(count, err)
	}
}
func TestLabLifecycleManualStopIsNotTerminal(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx := context.Background()
	for i, ch := range set.challenges {
		submitStoredFlag(t, f, user, ch, at.Add(time.Duration(i)*time.Second))
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET close_reason='manual' WHERE id=$1`, lab.ID); err != nil {
		t.Fatal(err)
	}
	if err = f.uc.ReconcilePendingLabLifecycles(ctx); err != nil {
		t.Fatal(err)
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	if len(f.agent.stopCalls) != 1 || f.agent.stopCalls[0].Terminal {
		t.Fatal(f.agent.stopCalls)
	}
}

func TestLabLifecycleConcurrentModeratorFinalAndSubmission(t *testing.T) {
	f, set, user, at := prepareLifecycle(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	submitStoredFlag(t, f, user, set.challenges[0], at)
	if _, err := f.uc.SubmitChallenge(ctx, f.eventID, user, set.challenges[2], event.SubmitChallengeInput{Answer: "wrong", IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	var attempt uuid.UUID
	var flag string
	if err := f.db.Pool.QueryRow(ctx, `SELECT a.id FROM challenge_attempts a JOIN team_challenges tc ON tc.id=a.team_challenge_id WHERE tc.event_team_id=$1 AND tc.event_challenge_id=$2`, f.blueID, set.challenges[2]).Scan(&attempt); err != nil {
		t.Fatal(err)
	}
	if err := f.db.Pool.QueryRow(ctx, `SELECT expected_flag FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[1]).Scan(&flag); err != nil {
		t.Fatal(err)
	}
	start := make(chan struct{})
	done := make(chan error, 2)
	go func() {
		<-start
		_, err := f.uc.DecideSolutionAttempt(ctx, f.eventID, attempt, event.DecideSolutionAttemptInput{Decision: challengeAttemptModel.DecisionAccepted, Reason: "accepted", DecidedBy: f.ownerID})
		done <- err
	}()
	go func() {
		<-start
		_, err := f.uc.SubmitChallenge(ctx, f.eventID, user, set.challenges[1], event.SubmitChallengeInput{Answer: flag, IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: at.Add(2 * time.Second)})
		done <- err
	}()
	close(start)
	for range 2 {
		select {
		case err := <-done:
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("moderator/answer deadlock", ctx.Err())
		}
	}
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(context.Background(), f.blueID, set.challenges[0])
	if err != nil || lab.Revision != 2 || lab.CloseReason != "solved" {
		t.Fatal(lab, err)
	}
}

func TestLabLifecycleReadinessWaitsForCanonicalLock(t *testing.T) {
	f := newStandFixture(t)
	set := f.attachSharedSet(t)
	f.pass(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET agent_uid='uid',agent_generation=1 WHERE id=$1`, lab.ID); err != nil {
		t.Fatal(err)
	}
	binding, err := labBindingRepo.New(f.db.Queries).Get(ctx, f.blueID, set.challenges[0])
	if err != nil {
		t.Fatal(err)
	}
	tx, err := f.db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = tx.Exec(ctx, `SELECT id FROM event_team_labs WHERE id=$1 FOR UPDATE`, lab.ID); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		changed, err := labBindingRepo.New(f.db.Queries).MarkReady(ctx, binding)
		if err == nil && !changed {
			err = fmt.Errorf("ready not applied")
		}
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("readiness bypassed canonical lock: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	if err = tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
}
