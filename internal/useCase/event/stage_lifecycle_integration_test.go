package event_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestStageClosureRecordsStopWithoutDeletingProgressOrWorkloads(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	rows, err := f.db.Queries.ListEventLabAllocations(ctx, f.eventID)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	now := time.Now().UTC()
	stageID := uuid.Must(uuid.NewV7())
	_, err = f.db.Pool.Exec(ctx, `INSERT INTO event_stages(id,event_id,name,opens_at,closes_at,returnable,created_at,updated_at) VALUES($1,$2,'Closed stage',$3,$4,true,$5,$5)`, stageID, f.eventID, now.Add(-2*time.Hour), now.Add(-time.Hour), now)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_exercises SET stage_id=$2 WHERE id=$1`, rows[0].EventExerciseID, stageID)
	require.NoError(t, err)
	// This synthetic current-stage fixture declares its original runtime stage;
	// a raw set move alone cannot relabel an already prepared whole-event copy.
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET runtime_stage_id=$2,runtime_stage_known=true WHERE event_exercise_id=$1`, rows[0].EventExerciseID, stageID)
	require.NoError(t, err)
	var before int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenges WHERE event_id=$1`, f.eventID).Scan(&before))
	f.uc.SetLifecycleControls(true)
	require.NoError(t, f.uc.ReconcileStageLabLifecycle(ctx, f.eventID, now))
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, rows[0].ID)
	require.NoError(t, err)
	require.Equal(t, "Stopped", current.DesiredState)
	require.Equal(t, "stage", current.CloseReason)
	require.NotEqual(t, "Deleted", current.ActualState)
	require.Empty(t, f.agent.deleted)
	require.Empty(t, f.agent.destroyed)
	var after int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenges WHERE event_id=$1`, f.eventID).Scan(&after))
	require.Equal(t, before, after)
}
