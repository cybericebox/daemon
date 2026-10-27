package event_test

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

func (f *standFixture) bindingState(t *testing.T, teamName string) (readiness int16, wait string, attempts int32, deployed bool) {
	t.Helper()
	var team = map[string]any{"Blue": f.blueID, "Red": f.redID}[teamName]
	require.NoError(t, f.db.Pool.QueryRow(context.Background(), `SELECT readiness, wait_reason, retry_attempts, deployed_at IS NOT NULL FROM lab_bindings WHERE event_team_id=$1 AND event_challenge_id=$2`,
		team, f.infraOne).Scan(&readiness, &wait, &attempts, &deployed))
	return
}

// khctf 2026-10-26: «Lab already exists with a different spec» failed the binding for good. A stale Lab of the same
// name is replaced and the deploy repeats, nothing fails and nobody is called.
func TestStandEngine_StaleLabOfTheSameNameIsReplacedNotFailed(t *testing.T) {
	f := newStandFixture(t)
	journal := &journalCollector{}
	errorJournal.SetReporter(journal)
	t.Cleanup(func() { errorJournal.SetReporter(nil) })
	var stale atomic.Bool
	stale.Store(true)
	f.agent.beforeDeploy = func(_ context.Context, group, lab string) error {
		if stale.Load() {
			return errors.New("create lab: already exists with a different spec")
		}
		return nil
	}

	f.pass(t)
	readiness, wait, _, deployed := f.bindingState(t, "Blue")
	require.Equal(t, int16(0), readiness, "a stale lab is no verdict about the new one")
	require.Equal(t, eventStandModel.WaitReplacingStaleLab, wait)
	require.False(t, deployed)
	require.NotEmpty(t, f.agent.deleted, "the stale lab is deleted")
	require.Empty(t, journal.kind(errorJournal.KindLabDeploy))
	require.Zero(t, f.count(t, `SELECT count(*) FROM signal_outbox WHERE signal_type='event.lab.failed'`))

	stale.Store(false)
	f.pass(t)
	readiness, wait, _, deployed = f.bindingState(t, "Blue")
	require.Equal(t, int16(0), readiness)
	require.Empty(t, wait, "the wait ends with the deploy")
	require.True(t, deployed)
}

// A Lab the agent never accepted and that failed is retried after a backoff; the organizers hear it once.
func TestStandEngine_FailedLabIsRetriedWithBackoffAndTheAlarmIsOnce(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	journal := &journalCollector{}
	errorJournal.SetReporter(journal)
	t.Cleanup(func() { errorJournal.SetReporter(nil) })
	var calls atomic.Int32
	var broken atomic.Bool
	broken.Store(true)
	f.agent.beforeDeploy = func(_ context.Context, group, lab string) error {
		if broken.Load() && strings.Contains(group, f.blueGroupSuffix(t)) {
			calls.Add(1)
			return errors.New("create lab: admission webhook refused")
		}
		return nil
	}

	f.pass(t)
	readiness, _, attempts, _ := f.bindingState(t, "Blue")
	require.Equal(t, int16(2), readiness)
	require.Zero(t, attempts)
	f.assertStatuses(t, map[string]string{"Blue": "failed"})
	require.Len(t, journal.kind(errorJournal.KindLabDeploy), 1)
	require.Equal(t, 1, f.count(t, `SELECT count(*) FROM signal_outbox WHERE signal_type='event.lab.failed' AND payload->>'team_id'=$1`, f.blueID.String()))

	// Inside the backoff nothing is tried again.
	f.pass(t)
	require.Equal(t, int32(1), calls.Load())

	// After the backoff the Lab is retried, fails again, and nobody is called a second time.
	f.ageFailure(t, 2*time.Minute)
	f.pass(t)
	readiness, _, attempts, _ = f.bindingState(t, "Blue")
	require.Equal(t, int32(2), calls.Load())
	require.Equal(t, int16(2), readiness)
	require.Equal(t, int32(1), attempts)
	f.assertStatuses(t, map[string]string{"Blue": "failed"})
	require.Len(t, journal.kind(errorJournal.KindLabDeploy), 1, "a repeated failure is not a new alarm")
	require.Equal(t, 1, f.count(t, `SELECT count(*) FROM signal_outbox WHERE signal_type='event.lab.failed' AND payload->>'team_id'=$1`, f.blueID.String()))

	// The cause passes: the next retry deploys and the stand recovers by itself.
	broken.Store(false)
	f.ageFailure(t, 10*time.Minute)
	f.pass(t)
	readiness, _, attempts, deployed := f.bindingState(t, "Blue")
	require.Equal(t, int16(0), readiness)
	require.Equal(t, int32(2), attempts)
	require.True(t, deployed)
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	f.pass(t)
	f.assertStatuses(t, map[string]string{"Blue": "ready", "Red": "ready"})
	_ = ctx
}

// An agent that does not answer is no verdict about the Lab: it waits until the deploy window is over.
func TestStandEngine_AgentThatDoesNotAnswerKeepsTheLabWaiting(t *testing.T) {
	f := newStandFixture(t)
	var down atomic.Bool
	down.Store(true)
	f.agent.beforeDeploy = func(_ context.Context, group, lab string) error {
		if down.Load() {
			return status.Error(codes.Unavailable, "connection refused")
		}
		return nil
	}
	f.pass(t)
	readiness, wait, _, _ := f.bindingState(t, "Blue")
	require.Equal(t, int16(0), readiness)
	require.Equal(t, eventStandModel.WaitAgentUnavailable, wait)

	// Past the deploy window it becomes a failure.
	_, err := f.db.Pool.Exec(context.Background(), `UPDATE lab_bindings SET wait_since = now() - interval '25 minutes' WHERE wait_reason=$1`, eventStandModel.WaitAgentUnavailable)
	require.NoError(t, err)
	f.pass(t)
	readiness, _, _, _ = f.bindingState(t, "Blue")
	require.Equal(t, int16(2), readiness)
	f.assertStatuses(t, map[string]string{"Blue": "failed"})
}

// A lab that waits for something for long (no room in the cluster reservation) reaches the organizers like a
// failure, once, and the stand recovers when the wait ends.
func TestStandEngine_LongWaitRaisesTheAlarmOnce(t *testing.T) {
	f := newStandFixture(t)
	var blocked atomic.Bool
	blocked.Store(true)
	f.agent.beforeDeploy = func(_ context.Context, group, lab string) error {
		if blocked.Load() {
			return infraModel.ErrNoClusterReservation
		}
		return nil
	}
	f.pass(t)
	_, wait, _, _ := f.bindingState(t, "Blue")
	require.Equal(t, eventStandModel.WaitClusterReservation, wait)
	f.assertStatuses(t, map[string]string{"Blue": "creating"})
	require.Zero(t, f.count(t, `SELECT count(*) FROM signal_outbox WHERE signal_type='event.lab.failed'`), "a short wait is not an alarm")

	_, err := f.db.Pool.Exec(context.Background(), `UPDATE lab_bindings SET wait_since = now() - interval '4 minutes' WHERE wait_reason=$1`, eventStandModel.WaitClusterReservation)
	require.NoError(t, err)
	f.pass(t)
	f.pass(t)
	f.assertStatuses(t, map[string]string{"Blue": "failed", "Red": "failed"})
	require.Equal(t, 1, f.count(t, `SELECT count(*) FROM signal_outbox WHERE signal_type='event.lab.failed' AND payload->>'team_id'=$1`, f.blueID.String()))

	blocked.Store(false)
	f.pass(t)
	f.assertStatuses(t, map[string]string{"Blue": "creating"})
	f.agent.setAll(f.agent.ready)
	f.pass(t)
	f.pass(t)
	f.assertStatuses(t, map[string]string{"Blue": "ready"})
}

func (f *standFixture) ageFailure(t *testing.T, by time.Duration) {
	t.Helper()
	_, err := f.db.Pool.Exec(context.Background(), `UPDATE lab_bindings SET failed_at = failed_at - make_interval(secs => $1) WHERE readiness = 2`, by.Seconds())
	require.NoError(t, err)
}

// blueGroupSuffix is the part of Blue's LabGroup name that identifies its deploys.
func (f *standFixture) blueGroupSuffix(t *testing.T) string {
	t.Helper()
	var group string
	require.NoError(t, f.db.Pool.QueryRow(context.Background(), `SELECT lab_group_name FROM lab_bindings WHERE event_team_id=$1 LIMIT 1`, f.blueID).Scan(&group))
	return group
}
