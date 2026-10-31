package event_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// shownTask is what a team saw of one task on its board.
type shownTask struct {
	group *uuid.UUID
	files map[uuid.UUID]bool
}

// boardOf reads the team's board as its captain sees it.
func boardOf(
	t *testing.T,
	uc *event.EventUseCase,
	eventID, userID uuid.UUID,
) map[uuid.UUID]shownTask {
	t.Helper()
	board, err := uc.ListOwnBoard(context.Background(), eventID, userID)
	require.NoError(t, err)
	out := make(map[uuid.UUID]shownTask, len(board.Challenges))
	for _, c := range board.Challenges {
		files := map[uuid.UUID]bool{}
		for _, file := range c.Files {
			files[file.FileID] = true
		}
		out[c.EventChallengeID] = shownTask{group: c.GroupID, files: files}
	}
	return out
}

// The board invariant: a task once shown to a team never disappears, never leaves its group and never loses a file,
// whatever the organizers or the platform do next.
func TestBoardNeverTakesBackWhatATeamWasShown(t *testing.T) {
	f, set := readySetFixture(t)
	ctx := context.Background()

	var captain uuid.UUID
	require.NoError(
		t,
		f.db.Pool.QueryRow(ctx, `SELECT captain_id FROM event_teams WHERE id = $1`, f.blueID).
			Scan(&captain),
	)
	now := time.Now()
	member, ok, err := participantRepo.New(f.db.Queries).
		Upsert(ctx, participantModel.Participant{EventID: f.eventID, UserID: captain, Status: participantModel.StatusApproved, CreatedAt: now, DecidedAt: &now})
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, member.AssignTeam(f.blueID, participantModel.TeamRoleCaptain))
	_, err = participantRepo.New(f.db.Queries).AssignTeam(ctx, member)
	require.NoError(t, err)

	// two groups, a file on the first task of the set
	groupA, groupB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	for i, id := range []uuid.UUID{groupA, groupB} {
		_, err = f.db.Pool.Exec(
			ctx,
			`INSERT INTO event_challenge_groups (id, event_id, name, order_index, created_at) VALUES ($1, $2, $3, $4, now())`,
			id,
			f.eventID,
			fmt.Sprintf("Group %d", i),
			i,
		)
		require.NoError(t, err)
	}
	_, err = f.db.Pool.Exec(
		ctx,
		`UPDATE event_challenges SET group_id = $2 WHERE id = ANY($1)`,
		[]uuid.UUID{set.challenges[0], set.challenges[1]},
		groupA,
	)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(
		ctx,
		`UPDATE event_challenges SET group_id = $2 WHERE id = $1`,
		set.challenges[2],
		groupB,
	)
	require.NoError(t, err)
	file := uuid.Must(uuid.NewV7())
	withFile := publishSetVersion(t, f, set, func(v *exerciseModel.ExerciseVersion) {
		v.Variants[0].Tasks[0].Attachments = []exerciseModel.AttachmentRef{
			{FileID: file, Name: "binary"},
		}
	})
	_, err = f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &withFile, f.ownerID, false)
	require.NoError(t, err)

	f.shiftLifecycle(t, -time.Minute, time.Hour)
	f.pass(t)
	f.pass(t)
	shown := boardOf(t, f.uc, f.eventID, captain)
	for _, id := range set.challenges {
		require.Contains(t, shown, id, "the set is shown before the operations")
	}
	require.True(t, shown[set.challenges[0]].files[file])

	check := func(step string) {
		t.Helper()
		f.pass(t)
		now := boardOf(t, f.uc, f.eventID, captain)
		for id, before := range shown {
			after, still := now[id]
			require.True(t, still, "%s: task %s vanished from the board", step, id)
			require.Equal(t, before.group, after.group, "%s: task %s left its group", step, id)
			for fileID := range before.files {
				require.True(t, after.files[fileID], "%s: task %s lost file %s", step, id, fileID)
			}
		}
		for id, task := range now { // what is newly visible is kept too
			shown[id] = task
		}
	}

	textOnly := publishSetVersion(t, f, set, func(v *exerciseModel.ExerciseVersion) {
		v.Variants[0].Tasks[1].Description = []byte(`{"blocks":[{"text":"changed"}]}`)
	})
	_, err = f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &textOnly, f.ownerID, false)
	require.NoError(t, err)
	check("update to a version with a new text")

	topology := publishSetVersion(t, f, set, changeTopology)
	_, err = f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &topology, f.ownerID, true)
	require.NoError(t, err)
	check("update to a version with another topology (stands recreated)")

	reissued := publishSetVersion(t, f, set, func(v *exerciseModel.ExerciseVersion) {
		for i := range v.Variants[0].Tasks {
			v.Variants[0].Tasks[i].ID = uuid.Must(uuid.NewV7()) // a re-import issued new task ids
		}
	})
	links := f.sets.links[reissued]
	newTasks, err := exerciseRepoVersionTasks(ctx, f, reissued)
	require.NoError(t, err)
	for i := range links {
		links[i].TaskID = newTasks[i]
	}
	_, err = f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &reissued, f.ownerID, true)
	require.NoError(t, err)
	check("update to a version whose tasks got new ids")

	_, err = f.uc.RecreateTeamStand(ctx, f.eventID, f.blueID, f.ownerID)
	require.NoError(t, err)
	check("stand recreated")

	require.NoError(t, f.uc.SetEventExerciseVisibility(ctx, f.eventID, set.exerciseID, false))
	require.NoError(t, f.uc.SetEventExerciseVisibility(ctx, f.eventID, set.exerciseID, true))
	check("hidden and published again")

	synced, err := f.uc.SyncTeamCopies(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	_ = synced
	check("team copies synced")

	restarted := f.newUseCase(200) // a daemon restart: a fresh use case over the same database
	f.uc = restarted
	check("daemon restarted")
	check("another pass")
}

func exerciseRepoVersionTasks(
	ctx context.Context,
	f *standFixture,
	version uuid.UUID,
) ([]uuid.UUID, error) {
	v, err := exerciseRepo.New(f.db.Queries).GetVersion(ctx, version)
	if err != nil {
		return nil, err
	}
	var ids []uuid.UUID
	for _, task := range v.Variants[0].Tasks {
		ids = append(ids, task.ID)
	}
	return ids, nil
}
