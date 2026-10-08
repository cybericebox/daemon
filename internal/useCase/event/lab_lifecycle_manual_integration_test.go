package event_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func (a *standAgent) LabLifecycleCapabilities(context.Context, string) (infraModel.LifecycleCapabilities, bool) {
	return infraModel.LifecycleCapabilities{PerLabStop: true, RequiredSnapshot: true, ConfirmedRuntime: true, RetainedRestart: true, FullGroupStop: true}, true
}
func TestManualStopOwnershipModeRevisionReplayAndProgress(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	f.agent.setAll(f.agent.ready)
	f.shiftLifecycle(t, -time.Minute, time.Hour)
	f.pass(t)
	var user uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT user_id FROM event_participants WHERE team_id=$1 AND status=2 LIMIT 1`, f.blueID).Scan(&user))
	lab, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.blueID, f.infraOne)
	require.NoError(t, err)
	input := eventUseCase.ManualLabInput{ExpectedRevision: lab.Revision, IdempotencyKey: uuid.Must(uuid.NewV7())}
	_, err = f.uc.StopOwnLab(ctx, f.eventID, user, lab.ID, input)
	require.Error(t, err, "all_ready must deny manual controls")
	cfg, err := eventConfigRepo.New(f.db.Queries).Get(ctx, f.eventID)
	require.NoError(t, err)
	before := cfg.UpdatedAt
	cfg.TaskRevealMode = eventConfigModel.RevealAsReady
	ok, err := eventConfigRepo.New(f.db.Queries).Update(ctx, cfg, before)
	require.NoError(t, err)
	require.True(t, ok)
	var progress int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenges WHERE event_team_id=$1`, f.blueID).Scan(&progress))
	view, err := f.uc.StopOwnLab(ctx, f.eventID, user, lab.ID, input)
	require.NoError(t, err)
	require.True(t, view.LogicalClosed)
	require.Equal(t, "closed", view.RuntimeState)
	replay, err := f.uc.StopOwnLab(ctx, f.eventID, user, lab.ID, input)
	require.NoError(t, err)
	require.Equal(t, view.Revision, replay.Revision)
	input.IdempotencyKey = uuid.Must(uuid.NewV7())
	_, err = f.uc.StopOwnLab(ctx, f.eventID, user, lab.ID, input)
	require.Error(t, err, "stale revision must conflict")
	foreign, err := eventLabRepo.New(f.db.Queries).GetForChallenge(ctx, f.redID, f.infraOne)
	require.NoError(t, err)
	input.ExpectedRevision = foreign.Revision
	_, err = f.uc.StopOwnLab(ctx, f.eventID, user, foreign.ID, input)
	require.Error(t, err)
	var remaining int
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT count(*) FROM team_challenges WHERE event_team_id=$1`, f.blueID).Scan(&remaining))
	require.Equal(t, progress, remaining)
	require.Empty(t, f.agent.stopCalls, "manual route transaction must not issue RPC")
}

func TestFutureStagePinProtectsThirtyMinuteArtifactThroughFourHourNeed(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	rows, err := f.db.Queries.ListEventLabAllocations(ctx, f.eventID)
	require.NoError(t, err)
	require.NotEmpty(t, rows)
	labID := rows[0].ID
	now := time.Now().UTC()
	stageID := uuid.Must(uuid.NewV7())
	_, err = f.db.Pool.Exec(ctx, `INSERT INTO event_stages(id,event_id,name,opens_at,closes_at,returnable,lab_retention_minutes,created_at,updated_at) VALUES($1,$2,'Future dependency',$3,$4,false,30,$5,$5)`, stageID, f.eventID, now.Add(4*time.Hour), now.Add(5*time.Hour), now)
	require.NoError(t, err)
	_, err = f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET retention_minutes=30,retention_until=$2 WHERE id=$1`, labID, now.Add(30*time.Minute))
	require.NoError(t, err)
	f.uc.SetLifecycleControls(true)
	require.NoError(t, f.uc.SelectRetainedLabsForStage(ctx, f.eventID, stageID, []uuid.UUID{labID}))
	var until time.Time
	require.NoError(t, f.db.Pool.QueryRow(ctx, `SELECT needed_until FROM event_lab_retention_pins WHERE lab_id=$1 AND stage_id=$2`, labID, stageID).Scan(&until))
	require.WithinDuration(t, now.Add(5*time.Hour+30*time.Minute), until, time.Microsecond)
	current, err := eventLabRepo.New(f.db.Queries).Get(ctx, labID)
	require.NoError(t, err)
	require.NotNil(t, current.ProtectedUntil)
	require.Equal(t, until, *current.ProtectedUntil)
	_, err = f.db.Pool.Exec(ctx, `DELETE FROM events WHERE id=$1`, f.eventID)
	require.NoError(t, err)
	current, err = eventLabRepo.New(f.db.Queries).Get(ctx, labID)
	require.NoError(t, err)
	require.Equal(t, until, *current.ProtectedUntil, "scope deletion must preserve retained identity/history")
}
