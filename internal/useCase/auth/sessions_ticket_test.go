package auth_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/session"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/secret"
)

var testLifetimes = session.Lifetimes{Idle: time.Hour, Absolute: 48 * time.Hour}

// sessionUC is a use case with a loaded runtime; the mock repository is returned for expectations.
func sessionUC(t *testing.T, rt *session.Runtime) (*auth.AuthUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo: repo, Password: password.New(password.Config{HashCost: 4}), Sessions: rt,
		Config: config.AuthConfig{SessionIdleTTL: time.Hour, SessionAbsoluteTTL: 48 * time.Hour, Hosts: testHosts("test")},
	})
	return uc, repo
}

func sealTicket(t *testing.T, rt *session.Runtime, tk session.Ticket) string {
	t.Helper()
	v, err := rt.Codec.Seal(tk)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

// OpenSession never touches the database: the mock has no expectation, so any call would fail the test.
func TestOpenSession_NoDatabaseForGarbageExpiredOrRevoked(t *testing.T) {
	rt := testSessions(t)
	uc, _ := sessionUC(t, rt)
	uid, sid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	if _, err := uc.OpenSession("garbage"); !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("garbage: want ErrAuthInvalidSession, got %v", err)
	}
	old := session.NewTicket(sid, uid, time.Now().Add(-2*time.Hour), testLifetimes)
	if _, err := uc.OpenSession(sealTicket(t, rt, old)); !errors.Is(err, authModel.ErrAuthSessionExpired.Err()) {
		t.Fatalf("expired: want ErrAuthSessionExpired, got %v", err)
	}
	live := sealTicket(t, rt, session.NewTicket(sid, uid, time.Now(), testLifetimes))
	if _, err := uc.OpenSession(live); err != nil {
		t.Fatalf("live: %v", err)
	}
	rt.Revocations.Add(sid, time.Now().Add(time.Hour))
	if _, err := uc.OpenSession(live); !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("revoked: want the same ErrAuthInvalidSession as garbage (category A), got %v", err)
	}
}

func TestOpenSession_FailsClosedWhenTheRevocationListIsNotLoaded(t *testing.T) {
	rt := session.NewRuntime(session.RuntimeConfig{Sealer: mustCipher(t), Source: emptySource{}, StaleAfter: time.Minute})
	uc, _ := sessionUC(t, rt)
	tk := sealTicket(t, rt, session.NewTicket(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), time.Now(), testLifetimes))
	if _, err := uc.OpenSession(tk); !errors.Is(err, authModel.ErrAuthSessionsUnavailable.Err()) {
		t.Fatalf("want ErrAuthSessionsUnavailable, got %v", err)
	}
	// Garbage still answers 401 first: the stale list is not an excuse to reveal anything.
	if _, err := uc.OpenSession("garbage"); !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("garbage: %v", err)
	}
}

// LoadCaller is the one query of a request: the role and status, nothing else.
func TestLoadCaller_ReadsRoleAndRefusesBlockedAndGone(t *testing.T) {
	rt := testSessions(t)
	uc, repo := sessionUC(t, rt)
	rt.Seen.SetWriter(noSeenWrites{}) // the last_seen batching has its own tests
	uid, sid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	pass := auth.SessionPass{Ticket: session.NewTicket(sid, uid, time.Now(), testLifetimes)}

	repo.EXPECT().GetUserAccess(gomock.Any(), uid).Return(postgres.GetUserAccessRow{Role: "admin", Status: "active"}, nil)
	claims, err := uc.LoadCaller(context.Background(), pass)
	if err != nil || claims.UserID != uid || claims.SessionID != sid || claims.Role != "admin" {
		t.Fatalf("claims %+v err %v", claims, err)
	}

	repo.EXPECT().GetUserAccess(gomock.Any(), uid).Return(postgres.GetUserAccessRow{Role: "user", Status: "blocked"}, nil)
	if _, err = uc.LoadCaller(context.Background(), pass); !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("blocked: want 401 ErrAuthInvalidSession, got %v", err)
	}
	repo.EXPECT().GetUserAccess(gomock.Any(), uid).Return(postgres.GetUserAccessRow{}, pgx.ErrNoRows)
	if _, err = uc.LoadCaller(context.Background(), pass); !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("gone: want 401 ErrAuthInvalidSession, got %v", err)
	}
}

func TestReissueCookie_OnlyAfterOnePercentOfTheIdleTTL(t *testing.T) {
	rt := testSessions(t)
	uc, _ := sessionUC(t, rt)
	uid, sid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	fresh := session.NewTicket(sid, uid, time.Now(), testLifetimes)
	if _, ok := uc.ReissueCookie(auth.SessionPass{Ticket: fresh}); ok {
		t.Fatal("a fresh cookie must not be re-issued")
	}
	// 1% of 1 h is 36 s.
	aged := session.NewTicket(sid, uid, time.Now().Add(-time.Minute), testLifetimes)
	value, ok := uc.ReissueCookie(auth.SessionPass{Ticket: aged})
	if !ok {
		t.Fatal("a cookie older than 1% of the idle TTL must be re-issued")
	}
	got, err := rt.Codec.Open(value, time.Now())
	if err != nil || got.SessionID != sid || got.UserID != uid || !got.SignedInAt.Equal(aged.SignedInAt.Truncate(time.Second)) {
		t.Fatalf("re-issued ticket %+v err %v", got, err)
	}
	if got.ExpiresAt.Before(time.Now().Add(59 * time.Minute)) {
		t.Fatalf("the new expiry must slide to now + idle, got %v", got.ExpiresAt)
	}
}

func TestSignIn_CookieIsATicketWithTheSessionAndTheAbsoluteCap(t *testing.T) {
	rt := testSessions(t)
	uc, repo := sessionUC(t, rt)
	pw := password.New(password.Config{HashCost: 4})
	hashed, _ := pw.Hash("Secret!1")
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{ID: uid, Role: "user", HashedPassword: pgtype.Text{String: hashed, Valid: true}}, nil)
	var sessionID uuid.UUID
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateSessionParams) (postgres.Session, error) {
		sessionID = p.ID
		return postgres.Session{ID: p.ID, UserID: uid, CreatedAt: p.CreatedAt, ExpiresAt: p.ExpiresAt}, nil
	})
	cookie, _, err := uc.SignIn(context.Background(), "a@b.test", "Secret!1", "", authModel.SessionMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	tk, err := rt.Codec.Open(cookie, time.Now())
	if err != nil || tk.SessionID != sessionID || tk.UserID != uid {
		t.Fatalf("ticket %+v err %v", tk, err)
	}
	if d := tk.ExpiresAt.Sub(tk.SignedInAt); d != time.Hour {
		t.Fatalf("expiry = sign-in + idle, got %v", d)
	}
}

// Every end of a session writes a revocation, and this replica sees it at once.
func TestSignOut_RevokesAtOnceOnThisReplica(t *testing.T) {
	rt := testSessions(t)
	uc, repo := sessionUC(t, rt)
	sid, uid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	expires := time.Now().Add(time.Hour)
	repo.EXPECT().RevokeSession(gomock.Any(), gomock.Any()).Return([]postgres.RevokeSessionRow{{SessionID: sid, ExpiresAt: expires}}, nil)
	if err := uc.SignOut(context.Background(), sid); err != nil {
		t.Fatal(err)
	}
	cookie := sealTicket(t, rt, session.NewTicket(sid, uid, time.Now(), testLifetimes))
	if _, err := uc.OpenSession(cookie); !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("the signed-out cookie must be refused at once, got %v", err)
	}
}

func TestRevokeSessionPassesTheTTLsToTheRevocationExpiry(t *testing.T) {
	rt := testSessions(t)
	uc, repo := sessionUC(t, rt)
	sid, uid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	repo.EXPECT().RevokeUserSession(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.RevokeUserSessionParams) ([]postgres.RevokeUserSessionRow, error) {
		if p.ID != sid || p.OwnerID != uid || p.IdleSecs != 3600 || p.AbsoluteSecs != 48*3600 {
			t.Fatalf("params %+v", p)
		}
		return []postgres.RevokeUserSessionRow{{SessionID: sid, ExpiresAt: time.Now().Add(time.Hour)}}, nil
	})
	if err := uc.RevokeSession(context.Background(), uid, sid); err != nil {
		t.Fatal(err)
	}
}

func mustCipher(t *testing.T) *secret.Cipher {
	t.Helper()
	c, err := secret.New("000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// revokedRows are the rows a revoking statement reports back (n sessions ended).
func revokedRows(n int) []postgres.RevokeUserSessionsRow {
	out := make([]postgres.RevokeUserSessionsRow, n)
	for i := range out {
		out[i] = postgres.RevokeUserSessionsRow{SessionID: uuid.Must(uuid.NewV7()), ExpiresAt: time.Now().Add(time.Hour)}
	}
	return out
}

func revokedExceptRows(n int) []postgres.RevokeUserSessionsExceptRow {
	out := make([]postgres.RevokeUserSessionsExceptRow, n)
	for i := range out {
		out[i] = postgres.RevokeUserSessionsExceptRow{SessionID: uuid.Must(uuid.NewV7()), ExpiresAt: time.Now().Add(time.Hour)}
	}
	return out
}

type noSeenWrites struct{}

func (noSeenWrites) WriteSeen(context.Context, uuid.UUID, uuid.UUID, time.Time) error { return nil }

// WriteSeen moves the session and the user forward, never backward (the SQL takes the greatest value).
func TestWriteSeen_TouchesSessionAndUser(t *testing.T) {
	rt := testSessions(t)
	uc, repo := sessionUC(t, rt)
	sid, uid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	repo.EXPECT().TouchSession(gomock.Any(), postgres.TouchSessionParams{ID: sid, SeenAt: at, ExpiresAt: at.Add(time.Hour)}).Return(int64(1), nil)
	repo.EXPECT().UpdateUserLastSeen(gomock.Any(), postgres.UpdateUserLastSeenParams{ID: uid, SeenAt: at}).Return(int64(1), nil)
	if err := uc.WriteSeen(context.Background(), sid, uid, at); err != nil {
		t.Fatal(err)
	}
}
