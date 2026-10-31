package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/waitDetailRepo"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
)

// The need and the room of a Lab that waits for the reservation reach the stand counters, so the reason the
// organizers read says "needs X, has Y".
func TestStandCounters_StuckDetailCarriesTheNeedAndTheRoom(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	var bindingID uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(ctx, `UPDATE lab_bindings SET readiness=0, deployed_at=NULL, wait_reason='not_enough_reserved', wait_since=now()-interval '10 minutes'
		WHERE event_team_id=$1 AND event_challenge_id=$2 RETURNING id`, f.blueID, f.infraOne).Scan(&bindingID))
	store := waitDetailRepo.New(f.db.Pool)
	require.NoError(t, store.Set(ctx, bindingID, eventStandModel.WaitDetail{NeedCPUMillicores: 1848, NeedMemoryBytes: 7 << 30, HaveCPUMillicores: 1352, HaveMemoryBytes: 5 << 30}, time.Now()))

	read := func() postgres.ListStandTeamsRow {
		rows, err := f.db.Queries.ListStandTeams(ctx, postgres.ListStandTeamsParams{EventID: f.eventID, NotDueExerciseIds: []uuid.UUID{}, StuckBefore: time.Now().Add(-3 * time.Minute)})
		require.NoError(t, err)
		for _, row := range rows {
			if row.ID == f.blueID {
				return row
			}
		}
		t.Fatal("blue has no stand row")
		return postgres.ListStandTeamsRow{}
	}
	row := read()
	assert.Equal(t, "not_enough_reserved", row.StuckReason)
	assert.Contains(t, eventStandModel.WaitDetailText(row.StuckDetail), "needs 1848m CPU and 7168 MiB, has 1352m CPU and 5120 MiB")

	require.NoError(t, store.Clear(ctx, bindingID))
	assert.Empty(t, read().StuckDetail)
}
