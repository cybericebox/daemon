package event_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// The lab_lifecycle pass saw «get lab group ... not found» in the same second the group was being created and
// failed (max_attempts 1, alerted). Not known yet to the agent is a waiting state for a lab without identity.
func TestLabLifecycle_LabNotYetKnownToTheAgentIsWaitingNotAnError(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.pass(t)
	f.uc.SetLifecycleControls(true)

	f.agent.statusErr = fmt.Errorf("get lab group %q: %w", "e-x", infraModel.ErrAgentObjectNotFound)
	require.NoError(t, f.uc.ReconcilePendingLabLifecycles(ctx), "a lab being created is waiting")

	_, err := f.db.Pool.Exec(ctx, `UPDATE event_team_labs SET next_attempt_at=now()-interval '1 second'`)
	require.NoError(t, err)
	// A real fault is still an error.
	f.agent.statusErr = errors.New("agent unreachable")
	require.Error(t, f.uc.ReconcilePendingLabLifecycles(ctx))
}
