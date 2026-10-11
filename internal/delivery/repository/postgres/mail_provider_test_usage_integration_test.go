package postgres_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/mailRepo"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestCreateProviderImportsSuccessfulUnsavedTestsOnlyOnce(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	owner := mailUser(t, db, "owner@example.org")
	other := mailUser(t, db, "other@example.org")
	key := fmt.Sprintf("%x", sha256.Sum256([]byte("smtp.a\x00587\x00starttls\x00user")))
	add := func(user uuid.UUID, status, connection string, at time.Time) {
		id := uuid.Must(uuid.NewV7())
		_, err := db.Pool.Exec(ctx, `INSERT INTO notification_dispatches (id, notification_type, recipient_user_id, status, created_at)
			VALUES ($1, 'smtp_test', $2, 'done', $3)`, id, user, at)
		require.NoError(t, err)
		_, err = db.Pool.Exec(ctx, `INSERT INTO notification_dispatch_targets (dispatch_id, channel, status, smtp_connection_key)
			VALUES ($1, 'email', $2, $3)`, id, status, connection)
		require.NoError(t, err)
	}
	add(owner, "done", key, now)
	add(owner, "done", key, now)
	add(owner, "error", key, now)
	add(other, "done", key, now)
	add(owner, "done", "another-account", now)
	add(owner, "done", key, now.Add(-24*time.Hour))
	cfg := mailModel.SMTPConfig{ID: uuid.Must(uuid.NewV7()), Name: "First", Enabled: true,
		Host: "smtp.a", Port: 587, TLSMode: mailModel.TLSStartTLS, Username: "user", UpdatedBy: &owner, UpdatedAt: now}
	r := mailRepo.New(db.Queries)
	created, err := r.CreateProvider(ctx, cfg)
	require.NoError(t, err)
	require.Equal(t, 2, created.Usage.SentOn(now))
	require.NotNil(t, created.Usage.LastUsedAt)
	cfg.ID, cfg.Name = uuid.Must(uuid.NewV7()), "Second"
	second, err := r.CreateProvider(ctx, cfg)
	require.NoError(t, err)
	require.Zero(t, second.Usage.SentOn(now), "an already imported test must never count again")
}
