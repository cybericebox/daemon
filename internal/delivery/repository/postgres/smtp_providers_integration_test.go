package postgres_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func providerParams(name, host string, now time.Time, perSecond pgtype.Float8, quota pgtype.Int4) postgres.InsertPlatformSMTPProviderParams {
	return postgres.InsertPlatformSMTPProviderParams{
		ID: uuid.Must(uuid.NewV7()), Name: name, Enabled: true, Host: host, Port: 587, TlsMode: "starttls",
		MaxPerSecond: perSecond, DailyQuota: quota, UpdatedAt: now,
	}
}

func insertProvider(t *testing.T, db *testhelpers.TestDB, name, host string, now time.Time) postgres.MailSmtpConfig {
	t.Helper()
	row, err := db.Queries.InsertPlatformSMTPProvider(context.Background(), providerParams(name, host, now, pgtype.Float8{}, pgtype.Int4{}))
	require.NoError(t, err)
	return row
}

func updateParams(row postgres.MailSmtpConfig, perSecond pgtype.Float8, quota pgtype.Int4) postgres.UpdatePlatformSMTPProviderParams {
	return postgres.UpdatePlatformSMTPProviderParams{
		ID: row.ID, Name: row.Name, Enabled: row.Enabled, Host: row.Host, Port: row.Port, TlsMode: row.TlsMode,
		MaxPerSecond: perSecond, DailyQuota: quota, UpdatedAt: row.UpdatedAt,
	}
}

func day(y int, m time.Month, d int) pgtype.Date {
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}

func TestSMTPProviders_NameIsUniquePerPlatformCaseInsensitively(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	now := time.Now().UTC()
	insertProvider(t, db, "Brevo", "smtp-relay.brevo.com", now)
	_, err := db.Queries.InsertPlatformSMTPProvider(context.Background(), providerParams("brevo", "other.example", now, pgtype.Float8{}, pgtype.Int4{}))
	require.Error(t, err, "the same name in another case is the same name")
}

func TestSMTPProviders_ReorderSetsPositions(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	a := insertProvider(t, db, "a", "a.example", now)
	b := insertProvider(t, db, "b", "b.example", now)
	c := insertProvider(t, db, "c", "c.example", now)

	require.NoError(t, db.Queries.ReorderPlatformSMTPProviders(ctx, []uuid.UUID{c.ID, a.ID, b.ID}))
	list, err := db.Queries.ListPlatformSMTPProviders(ctx)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{c.ID, a.ID, b.ID}, []uuid.UUID{list[0].ID, list[1].ID, list[2].ID})

	// A new provider still goes last.
	d := insertProvider(t, db, "d", "d.example", now)
	require.Equal(t, int32(3), d.Priority)
}

func TestSMTPProviders_DailyClaimCountsPerUTCDayAndStopsAtTheLimit(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	p, err := db.Queries.InsertPlatformSMTPProvider(ctx, providerParams("lim", "lim.example", now, pgtype.Float8{}, pgtype.Int4{Int32: 2, Valid: true}))
	require.NoError(t, err)
	today := day(2026, 10, 1)
	claim := func(d pgtype.Date) (int32, error) {
		return db.Queries.ReservePlatformSMTPSend(ctx, postgres.ReservePlatformSMTPSendParams{Day: d, ID: p.ID})
	}

	n, err := claim(today)
	require.NoError(t, err)
	require.Equal(t, int32(1), n)
	n, err = claim(today)
	require.NoError(t, err)
	require.Equal(t, int32(2), n)
	_, err = claim(today)
	require.ErrorIs(t, err, pgx.ErrNoRows, "the third message of the day is over the limit")

	// A failed send gives its slot back, and a provider error is kept.
	require.NoError(t, db.Queries.ReleasePlatformSMTPSend(ctx, postgres.ReleasePlatformSMTPSendParams{
		Day: today, Error: "535 authentication failed", At: now, ID: p.ID,
	}))
	got, err := db.Queries.GetPlatformSMTPProvider(ctx, p.ID)
	require.NoError(t, err)
	require.Equal(t, int32(1), got.SentToday)
	require.Equal(t, "535 authentication failed", got.LastError)
	require.True(t, got.LastErrorAt.Valid)
	n, err = claim(today)
	require.NoError(t, err, "the released slot can be claimed again")
	require.Equal(t, int32(2), n)

	// A release without a provider error keeps the earlier one.
	require.NoError(t, db.Queries.ReleasePlatformSMTPSend(ctx, postgres.ReleasePlatformSMTPSendParams{Day: today, Error: "", At: now, ID: p.ID}))
	got, _ = db.Queries.GetPlatformSMTPProvider(ctx, p.ID)
	require.Equal(t, "535 authentication failed", got.LastError)
	require.Equal(t, int32(1), got.SentToday)

	// The next UTC day starts from zero.
	n, err = claim(day(2026, 10, 2))
	require.NoError(t, err)
	require.Equal(t, int32(1), n)

	require.NoError(t, db.Queries.MarkPlatformSMTPUsed(ctx, postgres.MarkPlatformSMTPUsedParams{At: now, ID: p.ID}))
	got, _ = db.Queries.GetPlatformSMTPProvider(ctx, p.ID)
	require.True(t, got.LastUsedAt.Valid)
}

func TestSMTPProviders_NoLimitIsNeverExhausted(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	p := insertProvider(t, db, "free", "free.example", time.Now().UTC())
	for range 5 {
		_, err := db.Queries.ReservePlatformSMTPSend(ctx, postgres.ReservePlatformSMTPSendParams{Day: day(2026, 10, 1), ID: p.ID})
		require.NoError(t, err)
	}
}

func TestSMTPProviders_ClaimNeverTouchesEventRows(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	eventID := mailEvent(t, db, "claimevent", now.Add(time.Hour), nil)
	ev, err := db.Queries.UpsertEventSMTPConfig(ctx, postgres.UpsertEventSMTPConfigParams{
		ID: uuid.Must(uuid.NewV7()), ScopeEventID: uuid.NullUUID{UUID: eventID, Valid: true},
		Host: "smtp.uni.edu", Port: 587, TlsMode: "starttls", UpdatedAt: now,
	})
	require.NoError(t, err)
	_, err = db.Queries.ReservePlatformSMTPSend(ctx, postgres.ReservePlatformSMTPSendParams{Day: day(2026, 10, 1), ID: ev.ID})
	require.ErrorIs(t, err, pgx.ErrNoRows)
}

// The platform SMTP row saved before providers existed becomes the first
// provider: same id (the password ciphertext is bound to it), named after its
// host, enabled, first in the order.
func TestMigration0140_ExistingPlatformRowBecomesTheFirstProvider(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	down, err := os.ReadFile("migrations/0140_smtp_providers.down.sql")
	require.NoError(t, err)
	up, err := os.ReadFile("migrations/0140_smtp_providers.up.sql")
	require.NoError(t, err)

	_, err = db.Pool.Exec(ctx, string(down))
	require.NoError(t, err, "the down migration restores the one-row schema")
	id := uuid.Must(uuid.NewV7())
	_, err = db.Pool.Exec(ctx, `INSERT INTO mail_smtp_configs (id, host, port, tls_mode, username, password_ciphertext, daily_quota)
		VALUES ($1, 'smtp-relay.brevo.com', 587, 'starttls', 'user', 'sealed', 300)`, id)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `INSERT INTO mail_smtp_configs (id, host, port, tls_mode) VALUES ($1, 'second', 587, 'tls')`, uuid.Must(uuid.NewV7()))
	require.Error(t, err, "the old schema allows one platform row")

	_, err = db.Pool.Exec(ctx, string(up))
	require.NoError(t, err)
	got, err := db.Queries.GetPlatformSMTPProvider(ctx, id)
	require.NoError(t, err)
	require.Equal(t, "smtp-relay.brevo.com", got.Name)
	require.True(t, got.Enabled)
	require.Equal(t, int32(0), got.Priority)
	require.Equal(t, "sealed", got.PasswordCiphertext)
	require.Equal(t, int32(300), got.DailyQuota.Int32)
	require.Equal(t, int32(0), got.SentToday)

	// Down again collapses the list to one row (the first by priority).
	other := insertProvider(t, db, "other", "other.example", time.Now().UTC())
	_, err = db.Pool.Exec(ctx, string(down))
	require.NoError(t, err)
	var n int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM mail_smtp_configs WHERE scope_event_id IS NULL`).Scan(&n))
	require.Equal(t, 1, n)
	var kept uuid.UUID
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT id FROM mail_smtp_configs WHERE scope_event_id IS NULL`).Scan(&kept))
	require.NotEqual(t, other.ID, kept, "the first provider is kept")
}
