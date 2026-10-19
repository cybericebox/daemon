package loadtest_test

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/cybericebox/daemon/internal/delivery/repository/sessionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/loadtest"
	sessionPkg "github.com/cybericebox/daemon/internal/session"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/secret"
)

func TestValidateRun(t *testing.T) {
	for run, ok := range map[string]bool{"a1b2c3": true, "abc": true, "ab": false, "has-dash": false, "UPPER": false, "": false, "toolongtoolongtoolong1": false} {
		if err := loadtest.ValidateRun(run); (err == nil) != ok {
			t.Errorf("ValidateRun(%q) = %v, want ok=%v", run, err, ok)
		}
	}
}

func TestEmailCarriesTheRunMarker(t *testing.T) {
	email := loadtest.Email("run1", 7)
	if email != "lt-run1-007@loadtest.invalid" {
		t.Fatalf("email = %s", email)
	}
	if !loadtest.BelongsToRun(email, "run1") {
		t.Fatal("own address must belong to its run")
	}
	for _, other := range []string{"lt-run12-007@loadtest.invalid", "lt-run1-007@example.com", "real@loadtest.invalid", "LT-RUN2-001@loadtest.invalid"} {
		if loadtest.BelongsToRun(other, "run1") {
			t.Errorf("%s must not belong to run r1", other)
		}
	}
	if !loadtest.BelongsToRun("LT-RUN1-007@LOADTEST.INVALID", "run1") {
		t.Error("the match is case-insensitive")
	}
}

func TestWriteAndReadCredentials(t *testing.T) {
	file := loadtest.File{Run: "run1", Accounts: []loadtest.Account{{N: 1, Email: "lt-run1-001@loadtest.invalid", Password: "p", Cookie: "c"}}}

	path := filepath.Join(t.TempDir(), "r1.json")
	if err := loadtest.Write(path, nil, file); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v, want 0600", info.Mode().Perm())
	}
	back, err := loadtest.Read(path)
	if err != nil || len(back.Accounts) != 1 || back.Accounts[0].Cookie != "c" {
		t.Fatalf("read back = %+v, %v", back, err)
	}

	var stdout bytes.Buffer
	if err = loadtest.Write("-", &stdout, file); err != nil || !strings.Contains(stdout.String(), `"run": "run1"`) {
		t.Fatalf("stdout = %q, %v", stdout.String(), err)
	}
	if err = loadtest.Write("", &stdout, file); err == nil {
		t.Fatal("an empty path must be refused")
	}
}

func newService(t *testing.T, now time.Time) (*loadtest.Service, *sessionPkg.Codec, *testhelpers.TestDB) {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	cipher, err := secret.New(strings.Repeat("ab", 32))
	if err != nil {
		t.Fatal(err)
	}
	codec := sessionPkg.NewCodec(cipher)
	policy := password.ComplexityConfig{MinLength: 12, MaxLength: 72, MinCapitalLetters: 1, MinSmallLetters: 1, MinDigits: 1, MinSpecialCharacters: 1}
	pw := password.New(password.Config{HashCost: bcrypt.MinCost, Complexity: policy})
	svc := loadtest.New(userRepo.New(db.Queries), sessionRepo.New(db.Queries), codec, pw.Hash,
		loadtest.Config{Lifetimes: sessionPkg.Lifetimes{Idle: 12 * time.Hour, Absolute: 24 * time.Hour}, Complexity: policy}, nil,
	).WithClock(func() time.Time { return now })
	return svc, codec, db
}

func countUsers(t *testing.T, db *testhelpers.TestDB, where string) int {
	t.Helper()
	var n int
	if err := db.Pool.QueryRow(context.Background(), `SELECT count(*) FROM users WHERE `+where).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// TestCreateThenDeleteTouchesOnlyTheRun drives the command against a real database: accounts are
// confirmed, can sign in with their password, carry a cookie that opens, and delete removes the
// run and nothing else.
func TestCreateThenDeleteTouchesOnlyTheRun(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	svc, codec, db := newService(t, now)
	ctx := context.Background()

	if _, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, first_name, last_name, role, status, email_confirmed, last_seen, created_at)
		VALUES (gen_random_uuid(), 'real.person@example.com', 'Real', 'Person', 'user', 'active', true, now(), now())`); err != nil {
		t.Fatal(err)
	}

	other, err := svc.Create(ctx, "other", 2)
	if err != nil {
		t.Fatal(err)
	}
	file, err := svc.Create(ctx, "run1", 5)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if len(file.Accounts) != 5 || file.CookieName != "__Host-session" || !file.CookieExpiresAt.Equal(now.Add(12*time.Hour)) {
		t.Fatalf("file = %+v", file)
	}
	if n := countUsers(t, db, `email LIKE 'lt-run1-%@loadtest.invalid' AND status = 'active' AND email_confirmed AND role = 'user'`); n != 5 {
		t.Fatalf("active confirmed users = %d, want 5", n)
	}

	seen := map[string]bool{}
	for _, account := range file.Accounts {
		if seen[account.Password] {
			t.Fatal("passwords must be distinct")
		}
		seen[account.Password] = true
		if err = pwCheck(db, account.Email, account.Password); err != nil {
			t.Fatalf("password of %s: %v", account.Email, err)
		}
		ticket, openErr := codec.Open(account.Cookie, now.Add(time.Minute))
		if openErr != nil || ticket.UserID != account.UserID {
			t.Fatalf("cookie of %s: %+v %v", account.Email, ticket, openErr)
		}
		var sessions int
		if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE id = $1 AND user_id = $2`, ticket.SessionID, account.UserID).Scan(&sessions); err != nil || sessions != 1 {
			t.Fatalf("session row of %s: %d %v", account.Email, sessions, err)
		}
	}

	if _, err = svc.Create(ctx, "run1", 1); err == nil {
		t.Fatal("a second create for the same run must be refused")
	}

	dry, err := svc.Delete(ctx, "run1", true)
	if err != nil || dry.Deleted != 5 {
		t.Fatalf("dry run = %+v %v", dry, err)
	}
	if n := countUsers(t, db, `email LIKE 'lt-run1-%' AND status = 'active'`); n != 5 {
		t.Fatalf("a dry run must change nothing, active = %d", n)
	}

	report, err := svc.Delete(ctx, "run1", false)
	if err != nil || report.Deleted != 5 || len(report.Kept) != 0 {
		t.Fatalf("delete = %+v %v", report, err)
	}
	if n := countUsers(t, db, `email LIKE 'lt-run1-%'`); n != 0 {
		t.Fatalf("addresses of the run left: %d", n)
	}
	if n := countUsers(t, db, `status = 'deleted' AND deleted_at IS NOT NULL`); n != 5 {
		t.Fatalf("soft-deleted = %d, want 5", n)
	}
	for _, account := range file.Accounts {
		var revoked int
		ticket, _ := codec.Open(account.Cookie, now)
		if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM session_revocations WHERE session_id = $1`, ticket.SessionID).Scan(&revoked); err != nil || revoked != 1 {
			t.Fatalf("revocation of %s: %d %v", account.Email, revoked, err)
		}
	}
	if n := countUsers(t, db, `email = 'real.person@example.com' AND status = 'active'`); n != 1 {
		t.Fatal("an unrelated account must survive")
	}
	if n := countUsers(t, db, `email LIKE 'lt-other-%' AND status = 'active'`); n != len(other.Accounts) {
		t.Fatalf("another run must survive, active = %d", n)
	}

	again, err := svc.Delete(ctx, "run1", false)
	if err != nil || again.Deleted != 0 {
		t.Fatalf("a second delete must be a no-op, got %+v %v", again, err)
	}
	// The address is free again: the run id can be reused.
	if _, err = svc.Create(ctx, "run1", 1); err != nil {
		t.Fatalf("re-create after delete: %v", err)
	}
}

func pwCheck(db *testhelpers.TestDB, email, plain string) error {
	var hash string
	if err := db.Pool.QueryRow(context.Background(), `SELECT hashed_password FROM users WHERE email = $1`, email).Scan(&hash); err != nil {
		return err
	}
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(plain))
}

func TestCreateRejectsBadInput(t *testing.T) {
	svc := loadtest.New(nil, nil, nil, nil, loadtest.Config{}, nil)
	ctx := context.Background()
	for _, tc := range []struct {
		run   string
		count int
	}{{"Bad-Run", 3}, {"ok1", 0}, {"ok1", loadtest.MaxUsers + 1}} {
		if _, err := svc.Create(ctx, tc.run, tc.count); err == nil {
			t.Errorf("Create(%q, %d) must fail", tc.run, tc.count)
		}
	}
}
