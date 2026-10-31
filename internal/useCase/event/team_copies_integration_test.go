package event_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// A file added to the pinned task after the teams' copies were issued: the organizer sees which copies differ and
// one call brings them up to date without touching the flag or the variant.
func TestTeamCopiesShowAndSyncAnAttachmentAddedToThePinnedTask(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	set := f.setOf(t, f.staticChallenge)

	view, err := f.uc.GetTeamCopies(ctx, f.eventID, set)
	require.NoError(t, err)
	require.Positive(t, view.Total)
	require.Zero(t, view.Outdated, "copies issued from the pinned version do not differ")

	fileID := uuid.Must(uuid.NewV7())
	_, err = f.db.Pool.Exec(ctx, `UPDATE exercise_versions v SET variants = jsonb_set(v.variants, '{0,tasks,0,attachments}',
		jsonb_build_array(jsonb_build_object('file_id', $2::text, 'name', 'brief.bin')))
		FROM event_exercises ee WHERE ee.id = $1 AND v.id = ee.exercise_version_id`, set, fileID.String())
	require.NoError(t, err)
	var flagBefore, variantBefore string
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT expected_flag, variant_index::text FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, f.blueID, f.staticChallenge).Scan(&flagBefore, &variantBefore))

	view, err = f.uc.GetTeamCopies(ctx, f.eventID, set)
	require.NoError(t, err)
	require.Equal(t, 3, view.Outdated, "blue, red and the moderators team hold a copy without the file")
	names := map[string]bool{}
	for _, item := range view.Items {
		require.Equal(t, []string{event.CopyDiffAttachments}, item.Differs)
		require.NotEmpty(t, item.TeamName)
		require.NotEmpty(t, item.ChallengeName)
		names[item.TeamName] = true
	}
	require.Len(t, names, 3)

	synced, err := f.uc.SyncTeamCopies(ctx, f.eventID, set)
	require.NoError(t, err)
	require.Equal(t, 3, synced.Outdated)

	view, err = f.uc.GetTeamCopies(ctx, f.eventID, set)
	require.NoError(t, err)
	require.Zero(t, view.Outdated)
	var snapshotFile string
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT snapshot->'attachments'->0->>'file_id' FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, f.blueID, f.staticChallenge).Scan(&snapshotFile))
	require.Equal(t, fileID.String(), snapshotFile, "the copy has the file now")
	var flagAfter, variantAfter string
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT expected_flag, variant_index::text FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, f.blueID, f.staticChallenge).Scan(&flagAfter, &variantAfter))
	require.Equal(t, [2]string{flagBefore, variantBefore}, [2]string{flagAfter, variantAfter}, "flag and variant stay")
	require.Equal(t, 1, f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2 AND content_updated_at IS NOT NULL`, f.blueID, f.staticChallenge), "the board marks the copy updated")

	again, err := f.uc.SyncTeamCopies(ctx, f.eventID, set)
	require.NoError(t, err)
	require.Zero(t, again.Outdated, "nothing left to sync")
}

func TestSyncTeamCopiesLeavesSolvedCopiesAlone(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	set := f.setOf(t, f.staticChallenge)
	_, err := f.db.Pool.Exec(ctx, `UPDATE exercise_versions v SET variants = jsonb_set(v.variants, '{0,tasks,0,attachments}',
		jsonb_build_array(jsonb_build_object('file_id', $2::text, 'name', 'brief.bin')))
		FROM event_exercises ee WHERE ee.id = $1 AND v.id = ee.exercise_version_id`, set, uuid.Must(uuid.NewV7()).String())
	require.NoError(t, err)
	var copyID uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT id FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, f.redID, f.staticChallenge).Scan(&copyID))
	_, err = f.db.Pool.Exec(ctx, `INSERT INTO team_challenge_solves (team_challenge_id, solved_at) VALUES ($1, now())`, copyID)
	require.NoError(t, err)

	synced, err := f.uc.SyncTeamCopies(ctx, f.eventID, set)

	require.NoError(t, err)
	require.Equal(t, 2, synced.Outdated, "blue and the moderators team are synced, red solved it")
	view, err := f.uc.GetTeamCopies(ctx, f.eventID, set)
	require.NoError(t, err)
	require.Equal(t, 1, view.Outdated)
	require.True(t, view.Items[0].Solved, "the solved copy is still reported, with its flag")
}

// Updating a published set to a version whose task got a file reaches the copies already issued to the teams, and
// the copies then no longer differ.
func TestUpdateEventExerciseDeliversANewAttachmentToIssuedTeamCopies(t *testing.T) {
	f, set := readySetFixture(t)
	ctx := context.Background()
	fileID := uuid.Must(uuid.NewV7())
	next := publishSetVersion(t, f, set, func(v *exerciseModel.ExerciseVersion) {
		for i := range v.Variants[0].Tasks {
			v.Variants[0].Tasks[i].Attachments = []exerciseModel.AttachmentRef{{FileID: fileID, Name: "binary"}}
		}
	})
	before, err := f.uc.GetTeamCopies(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	require.Zero(t, before.Outdated)

	_, err = f.uc.UpdateEventExercise(ctx, f.eventID, set.exerciseID, &next, f.ownerID, false)
	require.NoError(t, err)

	var withFile int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenges tc JOIN event_challenges ec ON ec.id = tc.event_challenge_id
		WHERE ec.event_exercise_id = $1 AND tc.snapshot->'attachments'->0->>'file_id' = $2`, set.exerciseID, fileID.String()).Scan(&withFile))
	var total int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenges tc JOIN event_challenges ec ON ec.id = tc.event_challenge_id
		WHERE ec.event_exercise_id = $1`, set.exerciseID).Scan(&total))
	require.Positive(t, total)
	require.Equal(t, total, withFile, "every issued copy has the file")
	after, err := f.uc.GetTeamCopies(ctx, f.eventID, set.exerciseID)
	require.NoError(t, err)
	require.Zero(t, after.Outdated)
}
