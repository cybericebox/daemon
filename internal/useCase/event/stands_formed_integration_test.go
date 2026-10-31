package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

// With late join a stand is prepared for a formed team only: an unformed team gets no assignment and no lab, and
// it blocks nobody; the moment an organizer forms it, it gets its own wave.
func TestStandsStartForFormedTeamsAndNeverWaitForUnformedOnes(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	_, err := f.db.Pool.Exec(ctx, `UPDATE events SET join_policy = 1 WHERE id = $1`, f.eventID)
	require.NoError(t, err)
	var unformed uuid.UUID
	require.NoError(
		t,
		f.db.Pool.QueryRow(ctx, `UPDATE event_teams SET formed_at = NULL, admitted_manually = true WHERE id = $1 RETURNING id`, f.smallID).
			Scan(&unformed),
	)

	f.pass(t)
	require.Zero(
		t,
		f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id = $1`, unformed),
		"an unformed team generates nothing",
	)
	require.Zero(
		t,
		f.count(t, `SELECT count(*) FROM lab_bindings WHERE event_team_id = $1`, unformed),
	)
	require.NotZero(
		t,
		f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id = $1`, f.blueID),
		"formed teams start at once",
	)

	f.agent.setAll(f.agent.ready)
	f.pass(t)
	f.assertStatuses(t, map[string]string{"Blue": "ready", "Red": "ready"})
	f.shiftLifecycle(t, -1, 3600e9)
	f.pass(t)
	require.EqualValues(
		t,
		2,
		f.readiness(t, f.blueID, f.infraOne),
		"the unformed team did not hold the others back",
	)

	require.NoError(t, f.uc.FormManagedTeam(ctx, f.eventID, unformed, f.ownerID))
	f.pass(t)
	require.NotZero(
		t,
		f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id = $1`, unformed),
		"formed by the organizer: it gets its own wave",
	)
}

// In an event that closes joining at its start an unformed team generates nothing before the start; from the start
// it forms by itself and its stand is deployed.
func TestStandsOfALockedAtStartEventWaitForTheStartToFormTheTeams(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	_, err := f.db.Pool.Exec(ctx, `UPDATE events SET join_policy = 0 WHERE id = $1`, f.eventID)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_teams SET formed_at = NULL, admitted_manually = true WHERE id = $1`, f.smallID)
	require.NoError(t, err)

	f.pass(t)
	require.Zero(t, f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id = $1`, f.smallID), "unformed before the start")
	require.NotZero(t, f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id = $1`, f.blueID))

	f.shiftLifecycle(t, -time.Minute, time.Hour)
	f.pass(t)
	require.NotZero(t, f.count(t, `SELECT count(*) FROM team_challenges WHERE event_team_id = $1`, f.smallID), "formed by the start: its stand deploys")
}
