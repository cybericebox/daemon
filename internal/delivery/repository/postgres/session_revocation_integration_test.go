package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/mediaRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/sessionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/session"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var revokeLifetimes = session.Lifetimes{Idle: time.Hour, Absolute: 48 * time.Hour}

func newUser(t *testing.T, db *testhelpers.TestDB) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := userRepo.New(db.Queries).Create(context.Background(), userModel.NewIncompleteUser(id, id.String()+"@example.test", time.Now())); err != nil {
		t.Fatal(err)
	}
	return id
}

func newSession(t *testing.T, repo *sessionRepo.Repository, user uuid.UUID, createdAt, lastSeen time.Time) uuid.UUID {
	t.Helper()
	s := authModel.NewSession(user, authModel.SessionMetadata{}, time.Hour, createdAt)
	s.LastSeen = lastSeen
	created, err := repo.Create(context.Background(), s)
	if err != nil {
		t.Fatal(err)
	}
	return created.ID
}

// Ending a session deletes the row and writes its revocation in one statement, with the cookie's last possible
// expiry: the smaller of sign-in + absolute TTL and last_seen + idle TTL + 1 min.
func TestRevokeSessionWritesTheRevocationWithTheCookieExpiry(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := sessionRepo.New(db.Queries)
	user := newUser(t, db)
	now := time.Now()

	busy := newSession(t, repo, user, now.Add(-time.Hour), now.Add(-time.Minute))       // idle bound wins
	ancient := newSession(t, repo, user, now.Add(-47*time.Hour), now.Add(-time.Minute)) // absolute bound wins

	rows, err := repo.Revoke(ctx, busy, revokeLifetimes)
	if err != nil || len(rows) != 1 || rows[0].SessionID != busy {
		t.Fatalf("revoke: %+v, %v", rows, err)
	}
	if want := now.Add(-time.Minute).Add(time.Hour + time.Minute); rows[0].ExpiresAt.Sub(want).Abs() > time.Second {
		t.Fatalf("idle bound: expires %v, want about %v", rows[0].ExpiresAt, want)
	}
	rows, err = repo.Revoke(ctx, ancient, revokeLifetimes)
	if err != nil || len(rows) != 1 {
		t.Fatalf("revoke: %+v, %v", rows, err)
	}
	if want := now.Add(-47 * time.Hour).Add(48 * time.Hour); rows[0].ExpiresAt.Sub(want).Abs() > time.Second {
		t.Fatalf("absolute bound: expires %v, want about %v", rows[0].ExpiresAt, want)
	}

	if _, err = repo.GetByID(ctx, busy); err == nil {
		t.Fatal("the session row must be gone")
	}
	active, err := repo.ActiveRevocations(ctx)
	if err != nil || len(active) != 2 {
		t.Fatalf("both revocations are active: %+v, %v", active, err)
	}
	// Revoking a session that is already gone writes nothing.
	if rows, err = repo.Revoke(ctx, busy, revokeLifetimes); err != nil || len(rows) != 0 {
		t.Fatalf("a second revoke must be a no-op: %+v, %v", rows, err)
	}
}

func TestRevokeVariantsEndExactlyTheirSessions(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := sessionRepo.New(db.Queries)
	mine, other := newUser(t, db), newUser(t, db)
	now := time.Now()
	a := newSession(t, repo, mine, now, now)
	b := newSession(t, repo, mine, now, now)
	c := newSession(t, repo, mine, now, now)
	foreign := newSession(t, repo, other, now, now)

	// A user cannot end another user's session.
	if rows, err := repo.RevokeForUser(ctx, foreign, mine, revokeLifetimes); err != nil || len(rows) != 0 {
		t.Fatalf("foreign: %+v, %v", rows, err)
	}
	rows, err := repo.RevokeForUserExcept(ctx, mine, a, revokeLifetimes)
	if err != nil || len(rows) != 2 {
		t.Fatalf("all but one: %+v, %v", rows, err)
	}
	if _, err = repo.GetByID(ctx, a); err != nil {
		t.Fatalf("the kept session must stay: %v", err)
	}
	_ = b
	_ = c
	rows, err = repo.RevokeAllForUser(ctx, mine, revokeLifetimes)
	if err != nil || len(rows) != 1 || rows[0].SessionID != a {
		t.Fatalf("all: %+v, %v", rows, err)
	}
	if _, err = repo.GetByID(ctx, foreign); err != nil {
		t.Fatalf("another user's session must stay: %v", err)
	}
}

func TestRevocationPollReadsRecentRowsAndCleanupDropsExpiredOnes(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := sessionRepo.New(db.Queries)
	user := newUser(t, db)
	now := time.Now()

	before, err := repo.DatabaseTime(ctx)
	if err != nil || before.Sub(now).Abs() > time.Minute {
		t.Fatalf("database time %v, %v", before, err)
	}
	id := newSession(t, repo, user, now, now)
	if _, err = repo.Revoke(ctx, id, revokeLifetimes); err != nil {
		t.Fatal(err)
	}
	since, err := repo.RevocationsSince(ctx, before.Add(-time.Second))
	if err != nil || len(since) != 1 || since[0].SessionID != id || since[0].UserID != user {
		t.Fatalf("since: %+v, %v", since, err)
	}
	later, err := repo.RevocationsSince(ctx, before.Add(time.Hour))
	if err != nil || len(later) != 0 {
		t.Fatalf("a watermark past the row reads nothing: %+v, %v", later, err)
	}

	// A row past its expiry is dropped by the cleanup and is not loaded on start.
	if _, err = db.Pool.Exec(ctx, `UPDATE session_revocations SET expires_at = now() - interval '1 minute'`); err != nil {
		t.Fatal(err)
	}
	if active, _ := repo.ActiveRevocations(ctx); len(active) != 0 {
		t.Fatalf("an expired revocation must not be loaded: %+v", active)
	}
	if n, err := repo.DeleteExpiredRevocations(ctx); err != nil || n != 1 {
		t.Fatalf("cleanup: %d, %v", n, err)
	}
}

func TestTouchSessionNeverMovesLastSeenBack(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := sessionRepo.New(db.Queries)
	user := newUser(t, db)
	now := time.Now().Truncate(time.Microsecond)
	id := newSession(t, repo, user, now.Add(-time.Hour), now.Add(-time.Hour))

	if _, err := repo.Touch(ctx, id, now, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	// A late write with an older time (another replica, a flush) must not move it back.
	if _, err := repo.Touch(ctx, id, now.Add(-10*time.Minute), now.Add(50*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got, err := repo.GetByID(ctx, id)
	if err != nil || !got.LastSeen.Equal(now) || !got.ExpiresAt.Equal(now.Add(time.Hour)) {
		t.Fatalf("last_seen %v expires %v, want %v / %v (%v)", got.LastSeen, got.ExpiresAt, now, now.Add(time.Hour), err)
	}
}

func TestMediaUploadAdvancesOnlyInOrderForItsOwner(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := mediaRepo.New(db.Queries)
	owner, stranger := newUser(t, db), newUser(t, db)
	now := time.Now().Truncate(time.Microsecond)
	up, err := repo.CreateUpload(ctx, mediaModel.NewUpload(uuid.Must(uuid.NewV7()), owner, "a.bin", "application/octet-stream", 10, 4, time.Hour, now))
	if err != nil || up.ChunksReceived != 0 || up.ChunkCount() != 3 {
		t.Fatalf("create: %+v, %v", up, err)
	}
	exp := now.Add(2 * time.Hour)
	if n, err := repo.AdvanceUpload(ctx, up.ID, owner, 1, exp); err != nil || n != 0 {
		t.Fatalf("chunk 1 before chunk 0: %d, %v", n, err)
	}
	if n, err := repo.AdvanceUpload(ctx, up.ID, stranger, 0, exp); err != nil || n != 0 {
		t.Fatalf("another user: %d, %v", n, err)
	}
	if n, err := repo.AdvanceUpload(ctx, up.ID, owner, 0, exp); err != nil || n != 1 {
		t.Fatalf("chunk 0: %d, %v", n, err)
	}
	if n, err := repo.AdvanceUpload(ctx, up.ID, owner, 0, exp); err != nil || n != 0 {
		t.Fatalf("a repeated chunk 0 must not count twice: %d, %v", n, err)
	}
	got, err := repo.GetUpload(ctx, up.ID)
	if err != nil || got.ChunksReceived != 1 || !got.ExpiresAt.Equal(exp) {
		t.Fatalf("get: %+v, %v", got, err)
	}
	if expired, err := repo.ListExpiredUploads(ctx, exp.Add(time.Second), 10); err != nil || len(expired) != 1 {
		t.Fatalf("expired: %+v, %v", expired, err)
	}
	if n, err := repo.DeleteUpload(ctx, up.ID); err != nil || n != 1 {
		t.Fatalf("delete: %d, %v", n, err)
	}
}
