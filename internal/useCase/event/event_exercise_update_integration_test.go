package event_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// publishSetVersion publishes the next version of the set's exercise after mutate changed a copy of the pinned one.
func publishSetVersion(t *testing.T, f *standFixture, set sharedSet, mutate func(*exerciseModel.ExerciseVersion)) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	var previous uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT exercise_version_id FROM event_exercises WHERE id=$1`, set.exerciseID).Scan(&previous))
	exercises := exerciseRepo.New(f.db.Queries)
	version, err := exercises.GetVersion(ctx, previous)
	require.NoError(t, err)
	mutate(&version)
	next, err := exercises.UpsertDraft(ctx, version.ExerciseID, uuid.Must(uuid.NewV7()), version, time.Now(), uuid.NullUUID{})
	require.NoError(t, err)
	_, err = exercises.Publish(ctx, version.ExerciseID, time.Now())
	require.NoError(t, err)
	f.sets.topology[next.ID] = version.Variants[0].Topology
	f.sets.links[next.ID] = f.sets.links[previous]
	return next.ID
}

func changeTopology(v *exerciseModel.ExerciseVersion) {
	v.Variants[0].Topology.Devices[0].Image = "registry.test/updated:2"
}

func changeText(v *exerciseModel.ExerciseVersion) {
	for i := range v.Variants[0].Tasks {
		v.Variants[0].Tasks[i].Description = []byte(`{"blocks":[{"text":"new"}]}`)
	}
}

func readySetFixture(t *testing.T) (*standFixture, sharedSet) {
	t.Helper()
	f := newStandFixture(t)
	set := f.attachSharedSet(t)
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	f.pass(t)
	return f, set
}

func (f *standFixture) setLabs(t *testing.T, set sharedSet) int {
	return f.count(t, `SELECT count(*) FROM event_team_labs WHERE event_exercise_id=$1`, set.exerciseID)
}

// An update that changes only texts keeps every stand: no confirmation, no generation, nothing deleted.
func TestUpdateEventExercise_TextOnlyChangeKeepsTheStands(t *testing.T) {
	f, set := readySetFixture(t)
	ctx := context.Background()
	labsBefore := f.setLabs(t, set)
	namesBefore, gensBefore, _ := f.bindings(t, f.blueID, set.challenges)

	next := publishSetVersion(t, f, set, changeText)
	_, err := f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &next, f.ownerID, false)
	require.NoError(t, err)

	namesAfter, gensAfter, _ := f.bindings(t, f.blueID, set.challenges)
	require.Equal(t, namesBefore, namesAfter)
	require.Equal(t, gensBefore, gensAfter)
	require.Equal(t, labsBefore, f.setLabs(t, set))
	require.Empty(t, f.agent.deleted)
}

// An update that changes what the Lab runs asks first, then moves the Labs to a next free generation.
func TestUpdateEventExercise_TopologyChangeNeedsConfirmationThenRecreates(t *testing.T) {
	f, set := readySetFixture(t)
	ctx := context.Background()
	next := publishSetVersion(t, f, set, changeTopology)

	_, err := f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &next, f.ownerID, false)
	require.True(t, errors.Is(err, eventExerciseModel.ErrEventExerciseStandsRunning.Err()), "got %v", err)
	_, gens, _ := f.bindings(t, f.blueID, set.challenges)
	require.Equal(t, map[int32]int{0: 3}, gens, "nothing changes without the confirmation")

	_, err = f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &next, f.ownerID, true)
	require.NoError(t, err)
	_, gens, _ = f.bindings(t, f.blueID, set.challenges)
	require.Equal(t, map[int32]int{1: 3}, gens)
}

// Labs that never reached the agent are reset in place: nothing runs, so nothing needs a confirmation, the
// generation and name stay, and the new Lab is prepared from the new version.
func TestUpdateEventExercise_LabsNotStartedAreResetInPlace(t *testing.T) {
	f := newStandFixture(t)
	set := f.attachSharedSet(t)
	ctx := context.Background()
	f.agent.beforeDeploy = func(context.Context, string, string) error { return infraModel.ErrNoClusterReservation }
	f.pass(t)
	require.Empty(t, f.agent.deployed)
	labsBefore := f.setLabs(t, set)
	require.NotZero(t, labsBefore)
	var oldIDs []uuid.UUID
	rows, err := f.db.Pool.Query(ctx, `SELECT id FROM event_team_labs WHERE event_exercise_id=$1`, set.exerciseID)
	require.NoError(t, err)
	for rows.Next() {
		var id uuid.UUID
		require.NoError(t, rows.Scan(&id))
		oldIDs = append(oldIDs, id)
	}
	rows.Close()

	next := publishSetVersion(t, f, set, changeTopology)
	_, err = f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &next, f.ownerID, false)
	require.NoError(t, err, "labs that are not started need no confirmation")
	for _, id := range oldIDs {
		require.Zero(t, f.count(t, `SELECT count(*) FROM event_team_labs WHERE id=$1`, id), "the empty lab is gone")
	}
	_, gens, _ := f.bindings(t, f.blueID, set.challenges)
	require.Equal(t, map[int32]int{0: 3}, gens, "same generation, same name")

	f.agent.beforeDeploy = nil
	f.pass(t)
	f.pass(t)
	require.Equal(t, labsBefore, f.setLabs(t, set))
	f.agent.mu.Lock()
	defer f.agent.mu.Unlock()
	require.Empty(t, f.agent.deleted)
	updated := 0
	for _, topology := range f.agent.topos {
		if topology.Devices[0].Image == "registry.test/updated:2" {
			updated++
		}
	}
	require.NotZero(t, updated, "the labs were deployed from the new version")
}

// khctf 2026-10-26: the update recreated 20 event_challenges without their group. A version whose tasks have new
// ids but the same names keeps every board row.
func TestUpdateEventExercise_ReissuedTaskIdsKeepTheBoardRows(t *testing.T) {
	f, set := readySetFixture(t)
	ctx := context.Background()
	groupID := uuid.Must(uuid.NewV7())
	_, err := f.db.Pool.Exec(ctx, `INSERT INTO event_challenge_groups(id,event_id,name,order_index,created_at) VALUES($1,$2,'Web',0,now())`, groupID, f.eventID)
	require.NoError(t, err)
	for i, challenge := range set.challenges {
		_, err = f.db.Pool.Exec(ctx, `UPDATE event_challenges SET group_id=$2, points=$3, snapshot=jsonb_build_object('name', $4::text) WHERE id=$1`, challenge, groupID, 100+int32(i)*50, "Task "+set.vars[i])
		require.NoError(t, err)
	}
	copiesBefore := f.count(t, `SELECT count(*) FROM team_challenges WHERE event_challenge_id=ANY($1)`, set.challenges)
	namesBefore, gensBefore, _ := f.bindings(t, f.blueID, set.challenges)

	next := publishSetVersion(t, f, set, func(v *exerciseModel.ExerciseVersion) {
		for i := range v.Variants[0].Tasks {
			v.Variants[0].Tasks[i].ID = uuid.Must(uuid.NewV7())
		}
		changeText(v)
	})
	_, err = f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &next, f.ownerID, false)
	require.NoError(t, err)

	for i, challenge := range set.challenges {
		var group uuid.NullUUID
		var points int32
		var published bool
		var task uuid.UUID
		require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT group_id, points, published, task_id FROM event_challenges WHERE id=$1`, challenge).Scan(&group, &points, &published, &task),
			"the board row of the task survived the update")
		require.True(t, group.Valid && group.UUID == groupID, "the group is kept")
		require.Equal(t, 100+int32(i)*50, points)
		require.True(t, published)
		require.NotEqual(t, set.tasks[i], task, "the row points at the new task")
	}
	require.Equal(t, copiesBefore, f.count(t, `SELECT count(*) FROM team_challenges WHERE event_challenge_id=ANY($1)`, set.challenges))
	namesAfter, gensAfter, _ := f.bindings(t, f.blueID, set.challenges)
	require.Equal(t, namesBefore, namesAfter)
	require.Equal(t, gensBefore, gensAfter)
	require.Empty(t, f.agent.deleted, "a re-import of the same stand recreates nothing")
}

// A recreate takes the next free generation even when the team already has a Lab of the next one.
func TestRecreateTeamStand_TakesTheNextFreeGeneration(t *testing.T) {
	f, set := readySetFixture(t)
	ctx := context.Background()
	// The first generation already exists for this team and exercise under another name.
	_, err := f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET generation=1, lab_name=lab_name||'-old' WHERE event_team_id=$1 AND event_exercise_id=$2`, f.blueID, set.exerciseID)
	require.NoError(t, err)

	_, err = f.uc.RecreateTeamStand(ctx, f.eventID, f.blueID, uuid.Nil)
	require.NoError(t, err)
	_, gens, _ := f.bindings(t, f.blueID, set.challenges)
	require.Equal(t, map[int32]int{2: 3}, gens, "generation 1 is taken: the recreate goes to 2")
	f.pass(t)
	f.pass(t)
	require.Equal(t, 2, f.count(t, `SELECT count(*) FROM event_team_labs WHERE event_team_id=$1 AND event_exercise_id=$2`, f.blueID, set.exerciseID))
}
