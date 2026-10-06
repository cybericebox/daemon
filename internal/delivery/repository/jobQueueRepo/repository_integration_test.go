package jobQueueRepo_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/jobQueueRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestCancelEventNotificationsCancelsOnlyUnstartedNoticesOfThatEvent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	migrator, err := rivermigrate.New(riverpgxv5.New(db.Pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	event, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	insert := func(kind, state string, eventID *uuid.UUID) {
		args := `{}`
		if eventID != nil {
			args = `{"scope_event_id":"` + eventID.String() + `"}`
		}
		_, err := db.Pool.Exec(ctx, `INSERT INTO river_job (state, kind, args, max_attempts, queue, priority, finalized_at) VALUES ($1::river_job_state, $2, $3::jsonb, 3, 'default', 1, CASE WHEN $1::river_job_state IN ('completed', 'cancelled') THEN now() END)`, state, kind, args)
		require.NoError(t, err)
	}
	insert("notify", "available", &event)
	insert("notify", "retryable", &event)
	insert("notify", "scheduled", &event)
	insert("notify", "running", &event)
	insert("notify", "completed", &event)
	insert("notify", "available", &other)
	insert("notify", "available", nil)
	insert("event_mail", "available", &event)

	n, err := jobQueueRepo.New(db.Pool).CancelEventNotifications(ctx, event)
	require.NoError(t, err)
	assert.EqualValues(t, 3, n)

	var cancelled int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM river_job WHERE state = 'cancelled'`).Scan(&cancelled))
	assert.Equal(t, 3, cancelled)
}
