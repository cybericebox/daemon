package postgres_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/gofrs/uuid"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func elSeed(t *testing.T) (*testhelpers.TestDB, anFixture, eventLabModel.Lab, []uuid.UUID) {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	f := anSeed(t, db, "lifecycle")
	ctx := context.Background()
	var set uuid.UUID
	if err := db.Pool.QueryRow(ctx, `SELECT event_exercise_id FROM event_challenges WHERE id=$1`, f.challenge).Scan(&set); err != nil {
		t.Fatal(err)
	}
	other := uuid.Must(uuid.NewV7())
	rtExec(t, db, `INSERT INTO event_challenges(id,event_exercise_id,task_id,order_index,points,published,snapshot,created_at) VALUES($1,$2,$3,1,100,false,'{}',$4)`, other, set, uuid.Must(uuid.NewV7()), anStart)
	ids := []uuid.UUID{f.challenge, other}
	lab, err := eventLabModel.New(eventLabModel.NewInput{EventID: f.event, TeamID: f.team, EventExerciseID: set, Ref: eventLabModel.Ref{Group: "team", Lab: "shared"}, ObjectiveIDs: ids, Policy: eventLabModel.DefaultPolicy()}, anStart)
	if err != nil {
		t.Fatal(err)
	}
	if err = eventLabRepo.New(db.Queries).Create(ctx, lab, ids); err != nil {
		t.Fatal(err)
	}
	for _, id := range ids {
		if id != f.challenge {
			rtExec(t, db, `INSERT INTO team_challenges(id,event_id,event_team_id,event_challenge_id,variant_index,snapshot,hints,expected_flag,readiness,created_at) VALUES($1,$2,$3,$4,0,'{}','[]','flag',0,$5)`, uuid.Must(uuid.NewV7()), f.event, f.team, id, anStart)
		}
		b, err := labBindingModel.New(f.event, f.team, id, "team", "shared", anStart)
		if err != nil {
			t.Fatal(err)
		}
		b.LabID = uuid.NullUUID{UUID: lab.ID, Valid: true}
		if _, _, err = labBindingRepo.New(db.Queries).Create(ctx, b); err != nil {
			t.Fatal(err)
		}
	}
	return db, f, lab, ids
}
func elSolve(t *testing.T, db *testhelpers.TestDB, team, challenge uuid.UUID) {
	t.Helper()
	rtExec(t, db, `INSERT INTO team_challenge_solves(team_challenge_id,solved_at) SELECT id,$3 FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=$2`, team, challenge, anStart)
}
func TestEventLabsPinnedMembership(t *testing.T) {
	db, f, lab, ids := elSeed(t)
	ctx := context.Background()
	repo := eventLabRepo.New(db.Queries)
	lab.MarkMaterialized(anStart)
	if ok, err := repo.Update(ctx, lab, 1); !ok || err != nil {
		t.Fatal(ok, err)
	}
	elSolve(t, db, f.team, ids[0])
	if complete, err := repo.Complete(ctx, lab.ID); complete || err != nil {
		t.Fatal("visible first solve completed shared lab", complete, err)
	}
	rtExec(t, db, `UPDATE event_challenges SET published=false WHERE id=$1`, ids[0])
	elSolve(t, db, f.team, ids[1])
	if complete, err := repo.Complete(ctx, lab.ID); !complete || err != nil {
		t.Fatal(complete, err)
	}
	rtExec(t, db, `UPDATE lab_bindings SET generation=1 WHERE event_team_id=$1 AND event_challenge_id=$2`, f.team, ids[1])
	if complete, err := repo.Complete(ctx, lab.ID); complete || err != nil {
		t.Fatal("stale binding generation completed lab", complete, err)
	}
	rtExec(t, db, `UPDATE lab_bindings SET generation=0 WHERE event_team_id=$1`, f.team)
	rtExec(t, db, `DELETE FROM event_lab_objectives WHERE lab_id=$1 AND event_challenge_id=$2`, lab.ID, ids[1])
	if complete, err := repo.Complete(ctx, lab.ID); complete || err != nil {
		t.Fatal("truncated pin completed lab", complete, err)
	}
}
func TestEventLabsIncompleteMaterializationNeverCompletes(t *testing.T) {
	db, f, lab, ids := elSeed(t)
	for _, id := range ids {
		elSolve(t, db, f.team, id)
	}
	if complete, err := eventLabRepo.New(db.Queries).Complete(context.Background(), lab.ID); complete || err != nil {
		t.Fatal(complete, err)
	}
}
func TestEventLabsSurviveEventDeletion(t *testing.T) {
	db, f, lab, ids := elSeed(t)
	rtExec(t, db, `DELETE FROM events WHERE id=$1`, f.event)
	got, err := eventLabRepo.New(db.Queries).Get(context.Background(), lab.ID)
	if err != nil || got.ID != lab.ID {
		t.Fatal(got, err)
	}
	var n int
	if err = db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM event_lab_objectives WHERE lab_id=$1`, lab.ID).Scan(&n); err != nil || n != len(ids) {
		t.Fatal(n, err)
	}
}
func TestEventLabsInitialIdentityAndObservationFence(t *testing.T) {
	db, _, lab, _ := elSeed(t)
	ctx := context.Background()
	repo := eventLabRepo.New(db.Queries)
	if ok, err := repo.RecordInitialIdentity(ctx, lab.ID, lab.Ref, "uid", 7, anStart); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := repo.RecordInitialIdentity(ctx, lab.ID, lab.Ref, "recreated", 8, anStart); ok || err != nil {
		t.Fatal(ok, err)
	}
	lab, _ = repo.Get(ctx, lab.ID)
	if err := lab.Close("solved", uuid.Must(uuid.NewV7()), anStart); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.Update(ctx, lab, 1); !ok || err != nil {
		t.Fatal(ok, err)
	}
	now := anStart.Add(time.Minute)
	obs := eventLabModel.Observation{Ref: lab.Ref, UID: "uid", OperationID: lab.OperationID, Revision: lab.Revision, Generation: 7, ObservedGeneration: 7, DesiredState: "Stopped", ActualState: "Stopped", AccessFenced: true, ObservedAt: &now, Allocation: eventLabModel.Allocation{RuntimeState: "Released", ReleasedAt: &now}}
	stale := obs
	stale.UID = "recreated"
	if ok, err := repo.RecordObservation(ctx, lab.ID, stale); ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := repo.RecordObservation(ctx, lab.ID, obs); !ok || err != nil {
		t.Fatal(ok, err)
	}
	if ok, err := repo.RecordObservation(ctx, lab.ID, obs); ok || err != nil {
		t.Fatal("duplicate accepted", ok, err)
	}
	if due, err := repo.ListDirty(ctx, now, 10); err != nil || len(due) != 0 {
		t.Fatal("confirmed stop stayed dirty", due, err)
	}
	if ok, err := repo.Update(ctx, lab, 1); ok || err != nil {
		t.Fatal("stale revision wrote", ok, err)
	}
}
func TestEventLabsCanonicalLockSerializesWriters(t *testing.T) {
	db, _, lab, _ := elSeed(t)
	ctx := context.Background()
	tx, err := db.Pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback(ctx)
	if _, err = eventLabRepo.New(postgres.New(tx)).Lock(ctx, lab.ID); err != nil {
		t.Fatal(err)
	}
	blocked, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	if _, err = eventLabRepo.New(db.Queries).Lock(blocked, lab.ID); err == nil {
		t.Fatal("second canonical lock did not block")
	}
	if _, err = eventLabRepo.New(db.Queries).GetForChallenge(ctx, lab.TeamID, labInputChallenge(t, db, lab.ID)); err != nil {
		t.Fatal("nonlocking identity read blocked", err)
	}
}
func labInputChallenge(t *testing.T, db *testhelpers.TestDB, id uuid.UUID) uuid.UUID {
	t.Helper()
	var c uuid.UUID
	if err := db.Pool.QueryRow(context.Background(), `SELECT event_challenge_id FROM event_lab_objectives WHERE lab_id=$1 LIMIT 1`, id).Scan(&c); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEventLabsFailureCannotCreditRelease(t *testing.T) {
	db, _, lab, _ := elSeed(t)
	ctx := context.Background()
	repo := eventLabRepo.New(db.Queries)
	if ok, err := repo.RecordInitialIdentity(ctx, lab.ID, lab.Ref, "uid", 1, anStart); !ok || err != nil {
		t.Fatal(ok, err)
	}
	lab, _ = repo.Get(ctx, lab.ID)
	lab.Allocation = eventLabModel.Allocation{RuntimeState: "Allocated", AllocatedRequests: eventLabModel.Compute{CPUMillicores: 50}}
	if err := lab.Close("solved", uuid.Must(uuid.NewV7()), anStart); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.Update(ctx, lab, 1); !ok || err != nil {
		t.Fatal(ok, err)
	}
	now := anStart.Add(time.Minute)
	obs := eventLabModel.Observation{Ref: lab.Ref, UID: "uid", OperationID: lab.OperationID, Revision: lab.Revision, Generation: 1, ObservedGeneration: 1, DesiredState: "Stopped", ActualState: "StopFailed", FailureCode: "capture_failed", ObservedAt: &now, Allocation: eventLabModel.Allocation{RuntimeState: "Released", ReleasedAt: &now}}
	if ok, err := repo.RecordObservation(ctx, lab.ID, obs); !ok || err != nil {
		t.Fatal(ok, err)
	}
	got, err := repo.Get(ctx, lab.ID)
	if err != nil || got.Allocation.RuntimeState != "Allocated" || got.Allocation.AllocatedRequests.CPUMillicores != 50 || got.FailureCode != "capture_failed" {
		t.Fatal(got, err)
	}
	if due, err := repo.ListDirty(ctx, now, 10); err != nil || len(due) != 1 {
		t.Fatal(due, err)
	}
}

func TestEventLabsUnknownAllocationStaysDue(t *testing.T) {
	db, _, lab, _ := elSeed(t)
	rtExec(t, db, `UPDATE event_team_labs SET desired_state='Stopped',actual_state='Stopped',observed_revision=desired_revision,allocation='{}' WHERE id=$1`, lab.ID)
	due, err := eventLabRepo.New(db.Queries).ListDirty(context.Background(), anStart, 10)
	if err != nil || len(due) != 1 {
		t.Fatal("unknown allocation vanished from retry queue", due, err)
	}
}

func TestEventLabsLiveGenerationAdvancementFencedByMetadata(t *testing.T) {
	db, _, lab, _ := elSeed(t)
	ctx := context.Background()
	repo := eventLabRepo.New(db.Queries)
	if ok, err := repo.RecordInitialIdentity(ctx, lab.ID, lab.Ref, "uid", 1, anStart); !ok || err != nil {
		t.Fatal(ok, err)
	}
	lab, _ = repo.Get(ctx, lab.ID)
	if err := lab.Close("solved", uuid.Must(uuid.NewV7()), anStart); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.Update(ctx, lab, 1); !ok || err != nil {
		t.Fatal(ok, err)
	}
	now := anStart.Add(time.Minute)
	obs := eventLabModel.Observation{Ref: lab.Ref, UID: "uid", OperationID: lab.OperationID, Revision: lab.Revision, Generation: 2, ObservedGeneration: 1, DesiredState: "Stopped", ActualState: "Stopped", AccessFenced: true, ObservedAt: &now}
	if ok, err := repo.RecordObservation(ctx, lab.ID, obs); ok || err != nil {
		t.Fatal("stale status accepted", ok, err)
	}
	obs.ObservedGeneration = 2
	if ok, err := repo.RecordObservation(ctx, lab.ID, obs); !ok || err != nil {
		t.Fatal("live metadata generation not advanced", ok, err)
	}
	got, err := repo.Get(ctx, lab.ID)
	if err != nil || got.AgentGeneration != 2 {
		t.Fatal(got, err)
	}
	newer := now.Add(time.Minute)
	obs.ObservedAt = &newer
	obs.Generation = 1
	obs.ObservedGeneration = 1
	if ok, err := repo.RecordObservation(ctx, lab.ID, obs); ok || err != nil {
		t.Fatal("old live metadata floor accepted", ok, err)
	}
}

func TestEventLabsMigrationDownThenUp(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	for _, name := range []string{"0160_event_labs.down.sql", "0160_event_labs.up.sql"} {
		source, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.Pool.Exec(ctx, string(source)); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	var n int
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM information_schema.tables WHERE table_name IN ('event_team_labs','event_lab_objectives')`).Scan(&n); err != nil || n != 2 {
		t.Fatal(n, err)
	}
}
func TestEventLabsPolicySchemaRejectsMissingRetention(t *testing.T) {
	db, f, _, _ := elSeed(t)
	if _, err := eventConfigRepo.New(db.Queries).Create(context.Background(), eventConfigModel.NewEventConfig(f.event, anStart)); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(context.Background(), `UPDATE event_configs SET lab_policy='{"SnapshotMode":"required","MaxActiveLabsPerTeam":null,"RetentionMinutes":null}' WHERE event_id=$1`, f.event); err == nil {
		t.Fatal("schema accepted null required retention")
	}
}

func TestEventLabsUnknownObservationPreservesComputeAndStorage(t *testing.T) {
	db, _, lab, _ := elSeed(t)
	ctx := context.Background()
	repo := eventLabRepo.New(db.Queries)
	if ok, err := repo.RecordInitialIdentity(ctx, lab.ID, lab.Ref, "uid", 1, anStart); !ok || err != nil {
		t.Fatal(ok, err)
	}
	lab, _ = repo.Get(ctx, lab.ID)
	lab.Allocation = eventLabModel.Allocation{RuntimeState: "Allocated", StorageState: "Retained", AllocatedRequests: eventLabModel.Compute{CPUMillicores: 750, MemoryBytes: 512 * 1024 * 1024}, SnapshotQuotaBytes: 1024 * 1024 * 1024}
	if ok, err := repo.Update(ctx, lab, 1); !ok || err != nil {
		t.Fatal(ok, err)
	}
	now := anStart.Add(time.Minute)
	o := eventLabModel.Observation{Ref: lab.Ref, UID: "uid", OperationID: lab.OperationID, Revision: lab.Revision, Generation: 1, ObservedGeneration: 1, DesiredState: "Running", ActualState: "Unknown", ObservedAt: &now, Allocation: eventLabModel.Allocation{RuntimeState: "Unknown", StorageState: "Unknown"}}
	if ok, err := repo.RecordObservation(ctx, lab.ID, o); !ok || err != nil {
		t.Fatal(ok, err)
	}
	got, err := repo.Get(ctx, lab.ID)
	if err != nil || got.Allocation.AllocatedRequests.CPUMillicores != 750 || got.Allocation.AllocatedRequests.MemoryBytes != 512*1024*1024 || got.Allocation.SnapshotQuotaBytes != 1024*1024*1024 {
		t.Fatal(got, err)
	}
}
func TestEventLabsRequiredReleaseNeedsCaptureAndAccessFence(t *testing.T) {
	db, _, lab, _ := elSeed(t)
	ctx := context.Background()
	repo := eventLabRepo.New(db.Queries)
	if ok, err := repo.RecordInitialIdentity(ctx, lab.ID, lab.Ref, "uid", 1, anStart); !ok || err != nil {
		t.Fatal(ok, err)
	}
	lab, _ = repo.Get(ctx, lab.ID)
	lab.SnapshotMode = "required"
	lab.Allocation = eventLabModel.Allocation{RuntimeState: "Allocated", StorageState: "Retained", AllocatedRequests: eventLabModel.Compute{CPUMillicores: 750}, SnapshotQuotaBytes: 1024 * 1024 * 1024}
	if err := lab.Close("solved", uuid.Must(uuid.NewV7()), anStart); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.Update(ctx, lab, 1); !ok || err != nil {
		t.Fatal(ok, err)
	}
	for i, state := range []string{"Unknown", "Failed", "Pending", "Succeeded", "Succeeded"} {
		now := anStart.Add(time.Duration(i+1) * time.Minute)
		o := eventLabModel.Observation{Ref: lab.Ref, UID: "uid", OperationID: lab.OperationID, Revision: lab.Revision, Generation: 2, ObservedGeneration: 2, DesiredState: "Stopped", ActualState: "Stopped", SnapshotState: state, ObservedAt: &now, AccessFenced: i != 3, Allocation: eventLabModel.Allocation{RuntimeState: "Released", StorageState: "Unknown", ReleasedAt: &now}}
		if ok, err := repo.RecordObservation(ctx, lab.ID, o); !ok || err != nil {
			t.Fatal(ok, err)
		}
		got, err := repo.Get(ctx, lab.ID)
		if err != nil {
			t.Fatal(err)
		}
		if i < 4 && (got.Allocation.RuntimeState != "Allocated" || got.Allocation.AllocatedRequests.CPUMillicores != 750) {
			t.Fatal("uncertified release credited", i, got)
		}
		if got.Allocation.SnapshotQuotaBytes != 1024*1024*1024 {
			t.Fatal("retained quota dropped", got)
		}
		if i == 4 && got.Allocation.RuntimeState != "Released" {
			t.Fatal("valid release refused", got)
		}
	}
}
