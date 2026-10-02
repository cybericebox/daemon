package auth_test

import (
	"context"
	"errors"
	"fmt"
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
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

func tooMany(t *testing.T, got error) {
	t.Helper()
	if !errors.Is(got, authModel.ErrAuthTooManyRequests.Err()) {
		t.Fatalf("want ErrAuthTooManyRequests, got %v", got)
	}
}

// M3: password guessing against one account is locked after 5 failures, and
// the locked attempt does not even reach the database.
func TestSignIn_AccountLockoutAfterRepeatedFailures(t *testing.T) {
	uc, repo, pw := newUC(t)
	hashed, _ := pw.Hash("Secret!1")
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{
		ID: uuid.Must(uuid.NewV7()), HashedPassword: pgtype.Text{String: hashed, Valid: true},
	}, nil).Times(5)

	for i := 0; i < 5; i++ {
		_, _, e := uc.SignIn(context.Background(), "a@b.test", "wrong", "", authModel.SessionMetadata{})
		if !errors.Is(e, authModel.ErrAuthInvalidUserCredentials.Err()) {
			t.Fatalf("attempt %d: want invalid credentials, got %v", i, e)
		}
	}
	// Even the RIGHT password is refused while locked (no oracle during the lock).
	_, _, e := uc.SignIn(context.Background(), "A@b.test ", "Secret!1", "", authModel.SessionMetadata{})
	tooMany(t, e)
}

// An unknown address is throttled exactly like a known one (no enumeration).
func TestSignIn_UnknownAccountIsThrottledToo(t *testing.T) {
	uc, repo, _ := newUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "ghost@b.test").Return(postgres.User{}, pgx.ErrNoRows).Times(5)
	for i := 0; i < 5; i++ {
		_, _, _ = uc.SignIn(context.Background(), "ghost@b.test", "wrong", "", authModel.SessionMetadata{})
	}
	_, _, e := uc.SignIn(context.Background(), "ghost@b.test", "wrong", "", authModel.SessionMetadata{})
	tooMany(t, e)
}

func TestSignIn_SuccessClearsAccountFailures(t *testing.T) {
	uc, repo, pw := newUC(t)
	hashed, _ := pw.Hash("Secret!1")
	uid := uuid.Must(uuid.NewV7())
	row := postgres.User{ID: uid, Role: "user", HashedPassword: pgtype.Text{String: hashed, Valid: true}}
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(row, nil).AnyTimes()
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
		Return(postgres.Session{ID: uuid.Must(uuid.NewV7()), UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}, nil).AnyTimes()

	for round := 0; round < 3; round++ {
		for i := 0; i < 4; i++ {
			_, _, _ = uc.SignIn(context.Background(), "a@b.test", "wrong", "", authModel.SessionMetadata{})
		}
		if _, _, e := uc.SignIn(context.Background(), "a@b.test", "Secret!1", "", authModel.SessionMetadata{}); e != nil {
			t.Fatalf("round %d: success after 4 failures must pass: %v", round, e)
		}
	}
}

// A client address that fails over and over across accounts is locked, but
// failures are the only thing counted: a lab of successful sign-ins is free.
// Nothing is keyed on the client address: a whole computer lab signs in from one
// router, so failures of many different accounts from one address never lock it.
func TestSignIn_ClientAddressIsNeverLimited(t *testing.T) {
	uc, repo, _ := newUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows).AnyTimes()
	meta := authModel.SessionMetadata{IP: "203.0.113.7"}
	for i := 0; i < 200; i++ {
		_, _, e := uc.SignIn(context.Background(), fmt.Sprintf("u%d@b.test", i), "wrong", "", meta)
		if !errors.Is(e, authModel.ErrAuthInvalidUserCredentials.Err()) {
			t.Fatalf("attempt %d: want plain invalid credentials, got %v", i, e)
		}
	}
}

func TestSignIn_ThrottleCarriesRetryAfter(t *testing.T) {
	uc, repo, _ := newUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "ghost@b.test").Return(postgres.User{}, pgx.ErrNoRows).Times(5)
	for i := 0; i < 5; i++ {
		_, _, _ = uc.SignIn(context.Background(), "ghost@b.test", "wrong", "", authModel.SessionMetadata{})
	}
	_, _, e := uc.SignIn(context.Background(), "ghost@b.test", "wrong", "", authModel.SessionMetadata{})
	var ec interface{ StatusCode() err.StatusCode }
	if !errors.As(e, &ec) {
		t.Fatalf("not a status error: %T", e)
	}
	if secs, ok := ec.StatusCode().Details()[err.DetailRetryAfterSeconds].(int64); !ok || secs < 1 || secs > 60 {
		t.Fatalf("Retry-After seconds missing or off: %v", ec.StatusCode().Details())
	}
}

// M3: the old-password check of a signed-in user (stolen session) is not a free oracle.
func TestSetAccountPassword_GuessLockout(t *testing.T) {
	uc, repo, pw, _ := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	hashed, _ := pw.Hash("Correct!1")
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, HashedPassword: pgtype.Text{String: hashed, Valid: true}}, nil).Times(6)
	for i := 0; i < 5; i++ {
		if e := uc.SetAccountPassword(context.Background(), uid, "Wrong!1", "New!1pass"); !errors.Is(e, authModel.ErrAuthInvalidOldPassword.Err()) {
			t.Fatalf("guess %d: %v", i, e)
		}
	}
	tooMany(t, uc.SetAccountPassword(context.Background(), uid, "Correct!1", "New!1pass"))
}

// M3: a mailbox cannot be bombed through forgot-password; the answer stays neutral.
func TestForgotPassword_RecipientCooldownIsSilent(t *testing.T) {
	uc, repo, _, notifier := newPwUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{ID: uid, Status: "active"}, nil).Times(3)
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, nil).Times(1)
	for i := 0; i < 3; i++ {
		if e := uc.ForgotPassword(context.Background(), "A@b.test"); e != nil {
			t.Fatalf("request %d must look like success, got %v", i, e)
		}
	}
	if notifier.calls != 1 {
		t.Fatalf("want exactly 1 mail inside the cooldown, got %d", notifier.calls)
	}
}

func TestBeginEmailRegistration_RecipientCooldownIsSilent(t *testing.T) {
	uc, repo, _, notifier := newSignupUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "v@b.test").Return(postgres.User{
		ID: uuid.Must(uuid.NewV7()), Status: "incomplete",
	}, nil).Times(3)
	for i := 0; i < 3; i++ {
		if e := uc.BeginEmailRegistration(context.Background(), "V@b.test", ""); e != nil {
			t.Fatalf("request %d must look like success, got %v", i, e)
		}
	}
	if notifier.calls != 1 {
		t.Fatalf("want 1 setup mail inside the cooldown, got %d", notifier.calls)
	}
}

// The account-exists notice is a mail too.
func TestBeginEmailRegistration_AccountExistsNoticeIsLimited(t *testing.T) {
	uc, repo, _, notifier := newSignupUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{
		ID: uuid.Must(uuid.NewV7()), Status: "active",
	}, nil).Times(3)
	for i := 0; i < 3; i++ {
		if e := uc.BeginEmailRegistration(context.Background(), "a@b.test", ""); e != nil {
			t.Fatalf("request %d: %v", i, e)
		}
	}
	if notifier.calls != 1 {
		t.Fatalf("want 1 notice, got %d", notifier.calls)
	}
}

func TestRequestEmailChange_RecipientAndUserLimits(t *testing.T) {
	uc, repo, notifier := newEmailUC(t)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(accountWithPassword(t, uid), nil).AnyTimes()
	repo.EXPECT().GetUserByEmail(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows).AnyTimes()
	repo.EXPECT().CreateTemporalCode(gomock.Any(), gomock.Any()).Return(postgres.TemporalCode{}, nil).AnyTimes()

	if e := uc.RequestEmailChange(context.Background(), uid, "victim@b.test", "Correct!1"); e != nil {
		t.Fatalf("first: %v", e)
	}
	// the same victim address again: cooldown
	tooMany(t, uc.RequestEmailChange(context.Background(), uid, "victim@b.test", "Correct!1"))
	// many different addresses: capped per account
	sent := 1
	for i := 0; i < 10; i++ {
		if e := uc.RequestEmailChange(context.Background(), uid, fmt.Sprintf("x%d@b.test", i), "Correct!1"); e == nil {
			sent++
		}
	}
	// 5 requests per hour count against the account, a cooldown-refused one included.
	if sent != 4 || notifier.calls != 4 {
		t.Fatalf("one account is capped at 5 requests per hour (4 mails here), got %d (%d notified)", sent, notifier.calls)
	}
}

// Invitations sent by an admin are never limited: the same address may be invited
// again at once, and every call sends its mail.
func TestInviteUser_IsNotRateLimited(t *testing.T) {
	uc, repo, notifier := newInviteUC(t)
	incomplete := postgres.User{ID: uuid.Must(uuid.NewV7()), Email: "new@b.test", Status: "incomplete"}
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(incomplete, nil).AnyTimes()
	repo.EXPECT().DeleteUserProviders(gomock.Any(), gomock.Any()).Return(int64(0), nil).AnyTimes()
	repo.EXPECT().GetUserByID(gomock.Any(), incomplete.ID).Return(incomplete, nil).AnyTimes()
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil).AnyTimes()
	for i := 0; i < 10; i++ {
		if e := uc.InviteUser(inviteCtx(rbac.RoleAdmin), "NEW@b.test", rbac.RoleUser, "", ""); e != nil {
			t.Fatalf("invite %d: %v", i, e)
		}
	}
	if notifier.calls != 10 {
		t.Fatalf("want 10 invitation mails, got %d", notifier.calls)
	}
}

// The oldest sessions beyond SESSION_MAX_PER_USER are ended when a new one is created.
func TestSignIn_EvictsTheOldestSessionsOverTheCap(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	pw := password.New(password.Config{HashCost: 4})
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo: repo, Token: token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}), Password: pw,
		Config: config.AuthConfig{SessionIdleTTL: time.Hour, SessionMaxPerUser: 2, Hosts: testHosts("test")},
	})
	hashed, _ := pw.Hash("Secret!1")
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{ID: uid, Role: "user", HashedPassword: pgtype.Text{String: hashed, Valid: true}}, nil)
	now := time.Now()
	fresh, mid, old1, old2 := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(postgres.Session{ID: fresh, UserID: uid, CreatedAt: now, ExpiresAt: now.Add(time.Hour)}, nil)
	repo.EXPECT().GetSessionsByUser(gomock.Any(), uid).Return([]postgres.Session{
		{ID: fresh, UserID: uid, CreatedAt: now},
		{ID: old1, UserID: uid, CreatedAt: now.Add(-3 * time.Hour)},
		{ID: mid, UserID: uid, CreatedAt: now.Add(-time.Hour)},
		{ID: old2, UserID: uid, CreatedAt: now.Add(-5 * time.Hour)},
	}, nil)
	var evicted []uuid.UUID
	repo.EXPECT().DeleteUserSession(gomock.Any(), gomock.Any()).Times(2).DoAndReturn(func(_ context.Context, p postgres.DeleteUserSessionParams) (int64, error) {
		evicted = append(evicted, p.ID)
		return 1, nil
	})
	if _, _, err := uc.SignIn(context.Background(), "a@b.test", "Secret!1", "", authModel.SessionMetadata{}); err != nil {
		t.Fatal(err)
	}
	if len(evicted) != 2 || evicted[0] != old2 || evicted[1] != old1 {
		t.Fatalf("the two oldest sessions must go, oldest first: %v", evicted)
	}
}
