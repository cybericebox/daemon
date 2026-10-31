package event_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/useCase/event"
)

// A schedule change records who moved it, when, and from what to what; an edit that bypasses the application is
// recorded too and says so.
func TestScheduleChangesRecordWhoMovedTheStartAndHandEdits(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	f.shiftLifecycle(t, 30*time.Minute, 3*time.Hour)
	_, err := f.db.Pool.Exec(
		ctx,
		`UPDATE events SET available_from = publish_at - interval '1 hour'`,
	)
	require.NoError(t, err)
	before, err := f.uc.GetEventLifecycle(ctx, f.eventID)
	require.NoError(t, err)
	changes, err := f.uc.ListEventScheduleChanges(ctx, f.eventID)
	require.NoError(t, err)
	baseline := len(changes) // the fixture itself moves the schedule by hand

	newStart := before.StartAt.Add(20 * time.Minute)
	finish := before.FinishAt
	_, err = f.uc.UpdateEventLifecycle(ctx, f.eventID, event.UpdateLifecycleInput{
		JoinPolicy: before.JoinPolicy,
		PublishAt:  before.PublishAt,
		StartAt:    newStart,
		FinishAt:   finish,
		WithdrawAt: before.WithdrawAt,
	}, f.ownerID)
	require.NoError(t, err)

	changes, err = f.uc.ListEventScheduleChanges(ctx, f.eventID)
	require.NoError(t, err)
	require.Len(t, changes, baseline+1)
	moved := changes[0]
	require.True(t, moved.ByApplication)
	require.NotNil(t, moved.ChangedBy)
	require.Equal(t, f.ownerID, *moved.ChangedBy)
	require.Equal(t, "owner@stands.test", moved.ChangedByName)
	require.WithinDuration(t, time.Now(), moved.ChangedAt, time.Minute)
	require.WithinDuration(t, before.StartAt, moved.Before.StartAt, time.Second)
	require.WithinDuration(t, newStart, moved.After.StartAt, time.Second)

	// a hand edit that does not stamp the event
	_, err = f.db.Pool.Exec(
		ctx,
		`UPDATE events SET start_at = start_at + interval '5 minutes' WHERE id = $1`,
		f.eventID,
	)
	require.NoError(t, err)
	changes, err = f.uc.ListEventScheduleChanges(ctx, f.eventID)
	require.NoError(t, err)
	require.Len(t, changes, baseline+2)
	require.False(t, changes[0].ByApplication, "the application did not stamp the event")
	require.NotEmpty(t, changes[0].DBRole)
	require.WithinDuration(t, newStart.Add(5*time.Minute), changes[0].After.StartAt, time.Second)

	// a change that touches no schedule column records nothing
	_, err = f.db.Pool.Exec(ctx, `UPDATE events SET name = 'Renamed' WHERE id = $1`, f.eventID)
	require.NoError(t, err)
	changes, err = f.uc.ListEventScheduleChanges(ctx, f.eventID)
	require.NoError(t, err)
	require.Len(t, changes, baseline+2)
}
