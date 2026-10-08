package event_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labAccessSyncRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// sharedSet is one exercise with three tasks on one variant that has two devices: the tasks' flags live on
// the devices they name.
type sharedSet struct {
	exerciseID uuid.UUID // the event exercise
	challenges []uuid.UUID
	tasks      []uuid.UUID
	devices    []uuid.UUID // web, db
	vars       []string
	deviceOf   []int
}

func (f *standFixture) attachSharedSet(t *testing.T) sharedSet {
	t.Helper()
	ctx := context.Background()
	now := time.Now()
	exercises := exerciseRepo.New(f.db.Queries)
	value, err := exerciseModel.NewExercise("Shared lab set "+uuid.Must(uuid.NewV4()).String()[:8], "desc", []string{"web"}, uuid.Nil, now)
	if err != nil {
		t.Fatal(err)
	}
	if value, err = exercises.Create(ctx, value); err != nil {
		t.Fatal(err)
	}
	set := sharedSet{devices: []uuid.UUID{uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())}, vars: []string{"FLAG_A", "FLAG_B", "FLAG_C"}, deviceOf: []int{0, 1, 1}}
	variant := exerciseModel.Variant{Index: 0}
	for i := range set.vars {
		id := uuid.Must(uuid.NewV7())
		set.tasks = append(set.tasks, id)
		variant.Tasks = append(variant.Tasks, exerciseModel.Task{
			ID: id, Name: "Task " + set.vars[i], Description: json.RawMessage(`{"blocks":[]}`),
			Difficulty: exerciseModel.DifficultyEasy, Flag: []string{"ICE{" + set.vars[i] + "}"},
		})
	}
	variant.Topology.Devices = []exerciseModel.Device{{ID: set.devices[0], Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "nginx:1"}, {ID: set.devices[1], Name: "db", Type: exerciseModel.DeviceTypeContainer, Image: "postgres:16"}}
	draft, err := exercises.UpsertDraft(ctx, value.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant}}, now, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = exercises.Publish(ctx, value.ID, now); err != nil {
		t.Fatal(err)
	}
	attachment, err := f.db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{
		ID: uuid.Must(uuid.NewV7()), EventID: f.eventID, ExerciseID: value.ID, ExerciseVersionID: draft.ID,
		VariantMode: 1, FixedVariantIndex: pgtype.Int4{Int32: 0, Valid: true}, Revision: 1, CreatedAt: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	set.exerciseID = attachment.ID
	var links []exerciseModel.FlagLink
	for i, task := range set.tasks {
		challenge, err := f.db.Queries.CreateEventChallenge(ctx, postgres.CreateEventChallengeParams{
			ID: uuid.Must(uuid.NewV7()), EventExerciseID: attachment.ID, TaskID: task, OrderIndex: int32(i),
			Points: 100, Published: true, Snapshot: []byte(`{"name":"Shared task"}`), CreatedAt: now,
		})
		if err != nil {
			t.Fatal(err)
		}
		set.challenges = append(set.challenges, challenge.ID)
		links = append(links, exerciseModel.FlagLink{TaskID: task, DeviceID: set.devices[set.deviceOf[i]], Var: set.vars[i]})
	}
	f.sets.topology[draft.ID] = variant.Topology
	f.sets.links[draft.ID] = links
	return set
}

// bindings returns lab_name and generation of every binding of a team's challenges.
func (f *standFixture) bindings(t *testing.T, team uuid.UUID, challenges []uuid.UUID) (names map[string]int, generations map[int32]int, readiness map[int16]int) {
	t.Helper()
	names, generations, readiness = map[string]int{}, map[int32]int{}, map[int16]int{}
	for _, challenge := range challenges {
		var name string
		var generation int32
		var ready int16
		if err := f.db.Pool.QueryRow(context.Background(), `SELECT lab_name, generation, readiness FROM lab_bindings WHERE event_team_id = $1 AND event_challenge_id = $2`, team, challenge).Scan(&name, &generation, &ready); err != nil {
			t.Fatalf("binding of %s: %v", challenge, err)
		}
		names[name]++
		generations[generation]++
		readiness[ready]++
	}
	return names, generations, readiness
}

func (f *standFixture) deployedLike(prefix string) []string {
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	var out []string
	for _, lab := range f.agent.deployed {
		if strings.Contains(lab, "/"+prefix) {
			out = append(out, lab)
		}
	}
	return out
}

// Three tasks of one exercise on one variant are one Lab per team: deployed once with every task's flag
// in the device the task names, and every task's link, access and status point to it.
func TestStandEngine_OneLabPerExerciseWithEveryTaskFlag(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.pass(t)
	f.pass(t)

	// Blue, red and the moderators team: one set Lab each (and one for the single-task exercise), not three.
	if labs := f.deployedLike("x-"); len(labs) != 6 {
		t.Fatalf("deployed Labs = %v, want one per team and exercise (blue, red, moderators x 2 exercises)", labs)
	}
	if labs := f.deployedLike("c-"); len(labs) != 0 {
		t.Fatalf("a per-task Lab was deployed: %v", labs)
	}
	names, generations, _ := f.bindings(t, f.blueID, set.challenges)
	if len(names) != 1 || generations[0] != 3 {
		t.Fatalf("blue bindings: names %v, generations %v; every task must share one Lab", names, generations)
	}
	var lab, group string
	if err := f.db.Pool.QueryRow(ctx, `SELECT lab_group_name, lab_name FROM lab_bindings WHERE event_team_id = $1 AND event_challenge_id = $2`, f.blueID, set.challenges[0]).Scan(&group, &lab); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lab, "x-") || len(lab) > 63 {
		t.Fatalf("lab name %q", lab)
	}

	// All three flags, each in the device its task names, are the team's stored flags.
	f.agent.mu.Lock()
	topology := f.agent.topos[group+"/"+lab]
	meta := f.agent.metas[group+"/"+lab]
	f.agent.mu.Unlock()
	if len(topology.Devices) != 2 {
		t.Fatalf("topology devices = %d", len(topology.Devices))
	}
	for i, challenge := range set.challenges {
		var stored string
		if err := f.db.Pool.QueryRow(ctx, `SELECT expected_flag FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, f.blueID, challenge).Scan(&stored); err != nil || stored == "" {
			t.Fatalf("stored flag: %q, %v", stored, err)
		}
		var got []string
		for _, env := range topology.Devices[set.deviceOf[i]].EnvVars {
			if env.Name == set.vars[i] {
				got = append(got, env.Value)
			}
		}
		if len(got) != 1 || got[0] != stored {
			t.Fatalf("task %d env %s = %v, want the stored %q once", i, set.vars[i], got, stored)
		}
	}
	if meta.Labels[infraModel.LabelTask] != set.exerciseID.String() {
		t.Fatalf("lab labels = %v, want the exercise", meta.Labels)
	}

	// Every task resolves to that one Lab (links, status and device actions go through the binding).
	bindings := labBindingRepo.New(f.db.Queries)
	for _, challenge := range set.challenges {
		binding, err := bindings.Get(ctx, f.blueID, challenge)
		if err != nil || binding.LabGroupName != group || binding.LabName != lab {
			t.Fatalf("binding of %s = %+v, %v", challenge, binding, err)
		}
	}

	// The Lab becoming ready makes every task ready, then published at the start.
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	for _, challenge := range set.challenges {
		if got := f.readiness(t, f.blueID, challenge); got != 1 {
			t.Fatalf("task readiness = %d, want ready", got)
		}
	}
	if _, _, ready := f.bindings(t, f.blueID, set.challenges); ready[1] != 3 {
		t.Fatalf("binding readiness = %v", ready)
	}
	f.shiftLifecycle(t, -time.Minute, time.Hour)
	f.pass(t)
	for _, challenge := range set.challenges {
		if got := f.readiness(t, f.blueID, challenge); got != 2 {
			t.Fatalf("task readiness = %d, want published", got)
		}
	}

	// Access lists the Lab once for the three tasks.
	access, err := labAccessSyncRepo.New(f.db.Queries).Labs(ctx, f.blueID)
	if err != nil {
		t.Fatal(err)
	}
	if len(access) != 2 {
		t.Fatalf("access labs = %+v, want the set Lab and the single-task Lab", access)
	}
	for _, item := range access {
		if !item.Available {
			t.Fatalf("lab %+v must be open", item)
		}
	}

	// One stand lab per exercise is what the stand counters and the resource plan see.
	var ready int
	if err = f.db.Pool.QueryRow(ctx, `SELECT count(DISTINCT lab_name) FROM lab_bindings WHERE event_team_id = $1`, f.blueID).Scan(&ready); err != nil || ready != 2 {
		t.Fatalf("distinct labs of the team = %d, %v", ready, err)
	}
	plan, err := f.uc.GetResourcePlan(ctx, f.eventID)
	if err != nil {
		t.Fatal(err)
	}
	for _, task := range plan.Tasks {
		if task.EventExerciseID == set.exerciseID && task.Reserved.Devices != 2 {
			t.Fatalf("the set reserves %d devices per team, want its 2 (one Lab, not one per task)", task.Reserved.Devices)
		}
	}
}

// Recreate also removes a Lab of the team's group that no binding uses (left from an earlier generation).
func TestStandEngine_RecreateDeletesStaleLabsOfTheGroup(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.attachSharedSet(t)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	group, err := labBindingModel.GroupName(f.eventID, f.blueID)
	if err != nil {
		t.Fatal(err)
	}
	stale := "x-stale-v0-g1"
	f.agent.mu.Lock()
	f.agent.deployed = append(f.agent.deployed, group+"/"+stale)
	f.agent.mu.Unlock()

	if _, err := f.uc.RecreateTeamStand(ctx, f.eventID, f.blueID, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	var staleDeleted int
	for _, lab := range f.agent.deleted {
		if lab == group+"/"+stale {
			staleDeleted++
		}
	}
	if staleDeleted != 1 {
		t.Fatalf("the stale lab was deleted %d times, want once; deleted: %v", staleDeleted, f.agent.deleted)
	}
}

// Recreate replaces the shared Lab once: all three bindings move to the same new generation and name.
func TestStandEngine_RecreateMovesTheSharedLab(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	before, _, _ := f.bindings(t, f.blueID, set.challenges)

	if _, err := f.uc.RecreateTeamStand(ctx, f.eventID, f.blueID, uuid.Nil); err != nil {
		t.Fatal(err)
	}
	deletedSet := 0
	f.agent.mu.Lock()
	for _, lab := range f.agent.deleted {
		for name := range before {
			if strings.HasSuffix(lab, "/"+name) {
				deletedSet++
			}
		}
	}
	f.agent.mu.Unlock()
	if deletedSet != 1 {
		t.Fatalf("the shared Lab was deleted %d times, want once", deletedSet)
	}
	names, generations, ready := f.bindings(t, f.blueID, set.challenges)
	if len(names) != 1 || generations[1] != 3 || ready[0] != 3 {
		t.Fatalf("after recreate: names %v generations %v readiness %v", names, generations, ready)
	}
	for name := range names {
		if !strings.HasSuffix(name, "-g1") {
			t.Fatalf("recreated name %q", name)
		}
		f.pass(t)
		if labs := f.deployedLike(strings.TrimPrefix(name, "")); len(labs) != 1 {
			t.Fatalf("recreated Lab deployed %v, want once", labs)
		}
	}
}

// A stand that still has per-task Labs (c-<challenge>) is moved to the shared Lab: the old Labs are deleted.
func TestStandEngine_MovesPerTaskLabsToTheSharedLab(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	if _, err := f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET lab_name = 'c-' || event_challenge_id::text WHERE event_team_id = $1 AND event_challenge_id = ANY($2)`, f.blueID, set.challenges); err != nil {
		t.Fatal(err)
	}
	f.agent.mu.Lock()
	f.agent.deleted = nil
	f.agent.mu.Unlock()

	f.pass(t)
	f.agent.mu.Lock()
	deleted := append([]string(nil), f.agent.deleted...)
	f.agent.mu.Unlock()
	if len(deleted) != 3 {
		t.Fatalf("deleted = %v, want the three per-task Labs", deleted)
	}
	for _, lab := range deleted {
		if !strings.Contains(lab, "/c-") {
			t.Fatalf("deleted %q is not a per-task Lab", lab)
		}
	}
	names, generations, _ := f.bindings(t, f.blueID, set.challenges)
	if len(names) != 1 || generations[1] != 3 {
		t.Fatalf("after the move: names %v generations %v", names, generations)
	}
	for name := range names {
		if strings.HasPrefix(name, "c-") {
			t.Fatalf("still per-task: %q", name)
		}
	}
	f.pass(t)
	f.pass(t)
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	count := 0
	for _, lab := range f.agent.deployed {
		if strings.Contains(lab, "-g1") && strings.Contains(lab, "-v0") {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("shared Lab deployed %d times (%v)", count, f.agent.deployed)
	}
}

func TestEventLabsAssignmentPinsFullSharedSet(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.pass(t)
	var n int
	var materialized bool
	if err := f.db.Pool.QueryRow(ctx, `SELECT objective_count,materialized FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=$2`, f.blueID, set.exerciseID).Scan(&n, &materialized); err != nil || n != 3 || !materialized {
		t.Fatalf("full canonical pin: count=%d materialized=%v err=%v", n, materialized, err)
	}
	var distinct int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(DISTINCT lab_id) FROM lab_bindings WHERE event_team_id=$1 AND event_challenge_id=ANY($2)`, f.blueID, set.challenges).Scan(&distinct); err != nil || distinct != 1 {
		t.Fatal(distinct, err)
	}
}
func TestEventLabsBackfillPreservesSharedIdentity(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.pass(t)
	var beforeGroup, beforeName string
	var generation int32
	if err := f.db.Pool.QueryRow(ctx, `SELECT lab_group_name,lab_name,generation FROM lab_bindings WHERE event_team_id=$1 AND event_challenge_id=$2`, f.blueID, set.challenges[0]).Scan(&beforeGroup, &beforeName, &generation); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET lab_id=NULL WHERE event_team_id=$1 AND event_challenge_id=ANY($2)`, f.blueID, set.challenges); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `DELETE FROM event_lab_objectives WHERE lab_id IN (SELECT id FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=$2)`, f.blueID, set.exerciseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `DELETE FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=$2`, f.blueID, set.exerciseID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE team_challenges SET readiness=2 WHERE event_team_id=$1 AND event_challenge_id=ANY($2)`, f.blueID, set.challenges); err != nil {
		t.Fatal(err)
	}
	f.pass(t)
	var id uuid.UUID
	var group, name string
	var gotGeneration int32
	var count int
	var materialized bool
	if err := f.db.Pool.QueryRow(ctx, `SELECT id,lab_group_name,lab_name,generation,objective_count,materialized FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=$2`, f.blueID, set.exerciseID).Scan(&id, &group, &name, &gotGeneration, &count, &materialized); err != nil {
		t.Fatal(err)
	}
	if group != beforeGroup || name != beforeName || gotGeneration != generation || count != 3 || !materialized {
		t.Fatal(group, name, gotGeneration, count, materialized)
	}
	f.pass(t)
	var copies int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=$2`, f.blueID, set.exerciseID).Scan(&copies); err != nil || copies != 1 {
		t.Fatal(copies, err)
	}
	var attached int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM lab_bindings WHERE lab_id=$1`, id).Scan(&attached); err != nil || attached != 3 {
		t.Fatal(attached, err)
	}
}

func TestRequiredLabPolicyFailsBeforeProvisioning(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	if _, err := f.db.Pool.Exec(ctx, `UPDATE event_configs SET lab_policy='{"SnapshotMode":"required","MaxActiveLabsPerTeam":null,"RetentionMinutes":60}' WHERE event_id=$1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	err := f.uc.ReconcileEventStands(ctx)
	if err == nil {
		t.Fatal("REQUIRED without capture support prepared labs")
	}
	var n int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM lab_bindings WHERE event_id=$1`, f.eventID).Scan(&n); err != nil || n != 0 {
		t.Fatal("provisioned unsupported required labs", n, err)
	}
	if labs := f.deployedLike("x-"); len(labs) != 0 {
		t.Fatal("deployed", labs)
	}
}

func TestEventLabsInconsistentBackfillNeverProvisions(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.pass(t)
	if _, err := f.db.Pool.Exec(ctx, `UPDATE lab_bindings SET lab_id=NULL,generation=CASE WHEN event_challenge_id=$3 THEN 1 ELSE 0 END,deployed_at=NULL,readiness=0 WHERE event_team_id=$1 AND event_challenge_id=ANY($2)`, f.blueID, set.challenges, set.challenges[0]); err != nil {
		t.Fatal(err)
	}
	f.agent.mu.Lock()
	f.agent.deployed = nil
	f.agent.mu.Unlock()
	if err := f.uc.ReconcileEventStands(ctx); err == nil {
		t.Fatal("inconsistent assignment silently repaired")
	}
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	for _, ref := range f.agent.deployed {
		if strings.Contains(ref, "/x-"+labBindingModel.ShortID(set.exerciseID)) {
			t.Fatal("inconsistent canonical-missing lab provisioned", ref)
		}
	}
	var materialized int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM lab_bindings WHERE event_team_id=$1 AND event_challenge_id=ANY($2) AND lab_id IS NOT NULL`, f.blueID, set.challenges).Scan(&materialized); err != nil || materialized != 0 {
		t.Fatal(materialized, err)
	}
}

func TestEventLabsPartialBackfillPinsEveryObjectiveWithoutMaterializing(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.pass(t)
	for _, query := range []string{
		`UPDATE lab_bindings SET lab_id=NULL WHERE event_team_id=$1 AND event_challenge_id=ANY($2)`,
		`DELETE FROM event_lab_objectives WHERE lab_id IN (SELECT id FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=(SELECT event_exercise_id FROM event_challenges WHERE id=ANY($2) LIMIT 1))`,
		`DELETE FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=(SELECT event_exercise_id FROM event_challenges WHERE id=ANY($2) LIMIT 1)`,
		`DELETE FROM lab_bindings WHERE event_team_id=$1 AND event_challenge_id=(SELECT event_challenge_id FROM team_challenges WHERE event_team_id=$1 AND event_challenge_id=ANY($2) ORDER BY event_challenge_id LIMIT 1)`,
		`UPDATE team_challenges SET readiness=2 WHERE event_team_id=$1 AND event_challenge_id=ANY($2)`,
	} {
		if _, err := f.db.Pool.Exec(ctx, query, f.blueID, set.challenges); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.uc.ReconcileEventStands(ctx); err != nil {
		t.Fatal(err)
	}
	var count int
	var materialized bool
	if err := f.db.Pool.QueryRow(ctx, `SELECT objective_count,materialized FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=$2`, f.blueID, set.exerciseID).Scan(&count, &materialized); err != nil || count != 3 || materialized {
		t.Fatal(count, materialized, err)
	}
}

func TestEventLabsRequiredBackfillRequiresPreparationValidation(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	set := f.attachSharedSet(t)
	f.pass(t)
	for _, query := range []string{
		`UPDATE lab_bindings SET lab_id=NULL WHERE event_team_id=$1 AND event_challenge_id=ANY($2)`,
		`DELETE FROM event_lab_objectives WHERE lab_id IN (SELECT id FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=(SELECT event_exercise_id FROM event_challenges WHERE id=ANY($2) LIMIT 1))`,
		`DELETE FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=(SELECT event_exercise_id FROM event_challenges WHERE id=ANY($2) LIMIT 1)`,
	} {
		if _, err := f.db.Pool.Exec(ctx, query, f.blueID, set.challenges); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Pool.Exec(ctx, `UPDATE event_configs SET lab_policy='{"SnapshotMode":"required","MaxActiveLabsPerTeam":null,"RetentionMinutes":60}' WHERE event_id=$1`, f.eventID); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.ReconcileEventStands(ctx); err == nil {
		t.Fatal("unsupported REQUIRED legacy assignment silently backfilled")
	}
	var count int
	if err := f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=$2`, f.blueID, set.exerciseID).Scan(&count); err != nil || count != 0 {
		t.Fatal("unvalidated required policy was captured", count, err)
	}
}
