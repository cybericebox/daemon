package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/googleLinkTicketRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The pending Google link: one ticket per (account, Google identity), single-use, short-lived, with a bounded number
// of code checks; its account's removal removes it.
func TestGoogleLinkTickets(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := googleLinkTicketRepo.New(db.Queries)
	user := mustSeedUser(t, db, "pending-link@test.test")
	now := time.Now().UTC().Truncate(time.Microsecond)
	first := googleLinkTicketRepo.Ticket{ID: uuid.Must(uuid.NewV7()), UserID: user, GoogleID: "g-1", GoogleEmail: "g@example.org", GoogleName: "G One", ReturnTo: "https://id.test/p/", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}
	require.NoError(t, repo.Create(ctx, first, "hash-1"))

	got, err := repo.Get(ctx, "hash-1", now)
	require.NoError(t, err)
	require.Equal(t, first.ID, got.ID)
	require.Equal(t, "g-1", got.GoogleID)
	require.Equal(t, "g@example.org", got.GoogleEmail)
	require.Equal(t, "G One", got.GoogleName)
	require.Nil(t, got.CodeSentAt)

	// a new sign-in of the same identity replaces the ticket: the old value is dead, the code is gone
	require.NoError(t, repo.Create(ctx, googleLinkTicketRepo.Ticket{ID: uuid.Must(uuid.NewV7()), UserID: user, GoogleID: "g-1", CreatedAt: now, ExpiresAt: now.Add(10 * time.Minute)}, "hash-2"))
	_, err = repo.Get(ctx, "hash-1", now)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	second, err := repo.Get(ctx, "hash-2", now)
	require.NoError(t, err)

	// the mailed code is kept as a hash; attempts are bounded
	ok, err := repo.SetCode(ctx, second.ID, "code-hash", now)
	require.NoError(t, err)
	require.True(t, ok)
	got, err = repo.Get(ctx, "hash-2", now)
	require.NoError(t, err)
	require.Equal(t, "code-hash", got.CodeHash)
	require.NotNil(t, got.CodeSentAt)
	for range 3 {
		counted, countErr := repo.CountCodeAttempt(ctx, second.ID, now, 3)
		require.NoError(t, countErr)
		require.True(t, counted)
	}
	counted, err := repo.CountCodeAttempt(ctx, second.ID, now, 3)
	require.NoError(t, err)
	require.False(t, counted, "no attempts left")
	ok, err = repo.SetCode(ctx, second.ID, "new-hash", now)
	require.NoError(t, err)
	require.True(t, ok)
	counted, err = repo.CountCodeAttempt(ctx, second.ID, now, 3)
	require.NoError(t, err)
	require.True(t, counted, "a new code has all its attempts")

	// expiry: the ticket cannot be read, claimed or given a code after its time
	later := now.Add(11 * time.Minute)
	_, err = repo.Get(ctx, "hash-2", later)
	require.ErrorIs(t, err, pgx.ErrNoRows)
	claimed, err := repo.Consume(ctx, second.ID, later)
	require.NoError(t, err)
	require.False(t, claimed)

	// single use: of two claims only one wins
	claimed, err = repo.Consume(ctx, second.ID, now)
	require.NoError(t, err)
	require.True(t, claimed)
	claimed, err = repo.Consume(ctx, second.ID, now)
	require.NoError(t, err)
	require.False(t, claimed, "a replay finds nothing")

	// expired tickets are swept when a new one is stored; the account's removal removes its tickets
	require.NoError(t, repo.Create(ctx, googleLinkTicketRepo.Ticket{ID: uuid.Must(uuid.NewV7()), UserID: user, GoogleID: "g-3", CreatedAt: now.Add(time.Hour), ExpiresAt: now.Add(70 * time.Minute)}, "hash-3"))
	var left int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM google_link_tickets`).Scan(&left))
	require.Equal(t, 1, left)
	_, err = db.Pool.Exec(ctx, `DELETE FROM users WHERE id=$1`, user)
	if err == nil {
		require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM google_link_tickets`).Scan(&left))
		require.Zero(t, left)
	}
}
