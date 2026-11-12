package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The submission report names the participant whose first correct attempt solved the task, with the wrong
// attempts before it; the moderators team is never listed.
func TestEventAnalytics_Submissions(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventAnalyticsRepo.New(db.Queries)
	f := anSeed(t, db, "ansubmit")
	other := mustSeedUser(t, db, "ansubmit-other@test.test")

	anAttempt(t, db, f, f.team, f.teamChallenge, false, anStart.Add(10*time.Minute))
	anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(20*time.Minute))
	anAttempt(
		t,
		db,
		f,
		f.team,
		f.teamChallenge,
		true,
		anStart.Add(30*time.Minute),
	) // a later repeat is not the first
	anAttempt(t, db, f, f.moderators, f.modChallenge, true, anStart.Add(5*time.Minute))
	rtExec(
		t,
		db,
		`INSERT INTO team_challenge_solves (team_challenge_id, solved_at) VALUES ($1, $2)`,
		f.teamChallenge,
		anStart.Add(20*time.Minute),
	)
	rtExec(
		t,
		db,
		`UPDATE challenge_attempts SET user_id = $2 WHERE team_challenge_id = $1 AND received_at = $3`,
		f.teamChallenge,
		other,
		anStart.Add(10*time.Minute),
	)
	rtExec(
		t,
		db,
		`INSERT INTO event_activity (event_id, team_id, user_id, kind, subject_id, at) SELECT $1, $2, $3, 'task_opened', tc.event_challenge_id, $4 FROM team_challenges tc WHERE tc.id = $5`,
		f.event,
		f.team,
		f.user,
		anStart.Add(5*time.Minute),
		f.teamChallenge,
	)

	rows, err := repo.Submissions(ctx, f.event)
	require.NoError(t, err)
	require.Len(t, rows, 1, "one solved task of a visible team")
	row := rows[0]
	require.Equal(t, f.user, row.UserID)
	require.Equal(t, "Blue", row.TeamName)
	require.WithinDuration(t, anStart.Add(20*time.Minute), row.SubmittedAt, time.Second)
	require.WithinDuration(t, anStart, row.EventStartAt, time.Second)
	require.NotNil(t, row.FirstOpenAt)
	require.WithinDuration(t, anStart.Add(5*time.Minute), *row.FirstOpenAt, time.Second)
	require.EqualValues(
		t,
		0,
		row.ParticipantWrong,
		"the earlier wrong attempt was another participant's",
	)
	require.EqualValues(t, 1, row.TeamWrong)
}
