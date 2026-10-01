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
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

func newUC(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier, *password.Client) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	pw := password.New(password.Config{HashCost: 4})
	tk := token.MustNew(token.Config{
		TokenSignature: "test-signing-key-that-is-long-enough",
	})
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:     repo,
		Token:    tk,
		Password: pw,
		Config:   config.AuthConfig{SessionIdleTTL: time.Hour, Hosts: testHosts("test")},
	})
	return uc, repo, pw
}

func TestSignIn_Success(t *testing.T) {
	uc, repo, pw := newUC(t)
	hashed, _ := pw.Hash("Secret!1")
	uid := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())

	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{
		ID: uid, Email: "a@b.test", Role: "user",
		HashedPassword: pgtype.Text{String: hashed, Valid: true},
	}, nil)
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(postgres.Session{
		ID: sid, UserID: uid, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)

	cookie, redirect, err := uc.SignIn(context.Background(), "a@b.test", "Secret!1", "", authModel.SessionMetadata{})
	if err != nil {
		t.Fatalf("SignIn: %v", err)
	}
	if cookie == "" {
		t.Fatal("expected non-empty cookie")
	}
	if redirect != "https://id.test/profile" {
		t.Fatalf("want default profile redirect, got %q", redirect)
	}
}

func TestSignIn_WrongPassword(t *testing.T) {
	uc, repo, pw := newUC(t)
	hashed, _ := pw.Hash("Secret!1")
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{
		ID: uuid.Must(uuid.NewV7()), HashedPassword: pgtype.Text{String: hashed, Valid: true},
	}, nil)

	_, _, err := uc.SignIn(context.Background(), "a@b.test", "wrong", "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthInvalidUserCredentials.Err()) {
		t.Fatalf("want ErrAuthInvalidUserCredentials, got %v", err)
	}
}

func TestSignIn_NoUser_TimingSafe(t *testing.T) {
	uc, repo, _ := newUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "missing@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	// No CreateSession expected.
	_, _, err := uc.SignIn(context.Background(), "missing@b.test", "whatever", "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthInvalidUserCredentials.Err()) {
		t.Fatalf("want ErrAuthInvalidUserCredentials, got %v", err)
	}
}

func TestValidateSessionCookie_OK(t *testing.T) {
	uc, repo, _ := newUC(t)
	uid := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	// Issue a real cookie through a sign-in-less path: generate via the token client.
	tk := token.MustNew(token.Config{
		TokenSignature: "test-signing-key-that-is-long-enough",
	})
	cookie, _ := tk.GenerateSessionCookie(sid, time.Now().Add(time.Hour))

	repo.EXPECT().GetSessionByID(gomock.Any(), sid).Return(postgres.Session{
		ID: sid, UserID: uid, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Role: "user"}, nil)

	res, err := uc.ValidateSessionCookie(context.Background(), cookie)
	if err != nil {
		t.Fatalf("ValidateSessionCookie: %v", err)
	}
	if res.Claims.UserID != uid || res.Claims.SessionID != sid || res.Claims.Role != "user" {
		t.Fatalf("unexpected claims: %+v", res.Claims)
	}
}

func TestValidateSessionCookie_Expired(t *testing.T) {
	uc, repo, _ := newUC(t)
	sid := uuid.Must(uuid.NewV7())
	tk := token.MustNew(token.Config{
		TokenSignature: "test-signing-key-that-is-long-enough",
	})
	cookie, _ := tk.GenerateSessionCookie(sid, time.Now().Add(time.Hour))
	repo.EXPECT().GetSessionByID(gomock.Any(), sid).Return(postgres.Session{
		ID: sid, ExpiresAt: time.Now().Add(-time.Minute),
	}, nil)

	_, err := uc.ValidateSessionCookie(context.Background(), cookie)
	if !errors.Is(err, authModel.ErrAuthSessionExpired.Err()) {
		t.Fatalf("want ErrAuthSessionExpired, got %v", err)
	}
}

func TestValidateSessionCookie_BlockedUser_ReturnsErrAccountBlocked(t *testing.T) {
	uc, repo, _ := newUC(t)
	uid := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	tk := token.MustNew(token.Config{
		TokenSignature: "test-signing-key-that-is-long-enough",
	})
	cookie, _ := tk.GenerateSessionCookie(sid, time.Now().Add(time.Hour))

	repo.EXPECT().GetSessionByID(gomock.Any(), sid).Return(postgres.Session{
		ID: sid, UserID: uid, ExpiresAt: time.Now().Add(time.Hour),
	}, nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{
		ID: uid, Role: "user", Status: "blocked",
	}, nil)

	_, err := uc.ValidateSessionCookie(context.Background(), cookie)
	if !errors.Is(err, authModel.ErrAuthAccountBlocked.Err()) {
		t.Fatalf("want ErrAuthAccountBlocked, got %v", err)
	}
}

func TestRevokeSession_NotFound(t *testing.T) {
	uc, repo, _ := newUC(t)
	uid := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	repo.EXPECT().DeleteUserSession(gomock.Any(), postgres.DeleteUserSessionParams{ID: sid, UserID: uid}).
		Return(int64(0), nil)
	if err := uc.RevokeSession(context.Background(), uid, sid); !errors.Is(err, authModel.ErrAuthSessionNotFound.Err()) {
		t.Fatalf("want ErrAuthSessionNotFound, got %v", err)
	}
}

func TestRevokeOtherSessions_NilGuard(t *testing.T) {
	uc, _, _ := newUC(t)
	if err := uc.RevokeOtherSessions(context.Background(), uuid.Must(uuid.NewV7()), uuid.Nil); err == nil {
		t.Fatal("expected error when current session id is nil")
	}
}

func TestListSessions_FlagsCurrent(t *testing.T) {
	uc, repo, _ := newUC(t)
	uid := uuid.Must(uuid.NewV7())
	cur := uuid.Must(uuid.NewV7())
	other := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetSessionsByUser(gomock.Any(), uid).Return([]postgres.Session{
		{ID: cur, UserID: uid, Metadata: []byte(`{"ip":"1.1.1.1","user_agent":"x"}`)},
		{ID: other, UserID: uid, Metadata: []byte(`{}`)},
	}, nil)
	out, err := uc.ListSessions(context.Background(), uid, cur)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if len(out) != 2 || !out[0].IsCurrent || out[1].IsCurrent {
		t.Fatalf("current-session flag wrong: %+v", out)
	}
	if out[0].IP != "1.1.1.1" {
		t.Fatalf("metadata not parsed: %+v", out[0])
	}
}

// A deleted session row during cookie validation is an authentication
// failure (401 → client redirects to sign-in), not a 404 — and it must be
// indistinguishable from an unparseable cookie (category A): a distinct
// code/message would confirm to the caller that the session once existed.
func TestValidateSessionCookie_DeletedSession_IndistinguishableInvalidSession(t *testing.T) {
	uc, repo, _ := newUC(t)
	sid := uuid.Must(uuid.NewV7())
	tk := token.MustNew(token.Config{
		TokenSignature: "test-signing-key-that-is-long-enough",
	})
	cookie, _ := tk.GenerateSessionCookie(sid, time.Now().Add(time.Hour))

	repo.EXPECT().GetSessionByID(gomock.Any(), sid).Return(postgres.Session{}, pgx.ErrNoRows)

	_, err := uc.ValidateSessionCookie(context.Background(), cookie)
	if !errors.Is(err, authModel.ErrAuthInvalidSession.Err()) {
		t.Fatalf("want ErrAuthInvalidSession (401, category A), got %v", err)
	}
	if errors.Is(err, authModel.ErrAuthSessionNotFound.Err()) {
		t.Fatal("must not be the 404 ErrAuthSessionNotFound")
	}
}
