package auth_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/oauth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

// fakeOAuth is a hand double for auth.IOAuthClient returning a canned GoogleUser.
type fakeOAuth struct {
	user     *oauth.GoogleUser
	err      error
	redirect string
}

func (f *fakeOAuth) GetGoogleLoginURL(_ string) (string, string, error) {
	return "https://accounts.google/x", "state", nil
}
func (f *fakeOAuth) GetGoogleUser(_ context.Context, _, _ string) (*oauth.GoogleUser, string, error) {
	return f.user, f.redirect, f.err
}

func newGoogleUC(t *testing.T, gu *oauth.GoogleUser) (*auth.AuthUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	allowSetupLinkIssue(repo)
	uc := auth.NewAuthUseCase(auth.Dependencies{Sessions: testSessions(t),
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: &fakeNotifier{},
		OAuth:    &fakeOAuth{user: gu},
		Config:   config.AuthConfig{SessionIdleTTL: time.Hour, Hosts: testHosts("test")},
	})
	return uc, repo
}

// newGoogleUCRedirect is newGoogleUC whose OAuth state carries redirect.
func newGoogleUCRedirect(t *testing.T, gu *oauth.GoogleUser, redirect string) (*auth.AuthUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	allowSetupLinkIssue(repo)
	uc := auth.NewAuthUseCase(auth.Dependencies{Sessions: testSessions(t),
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: &fakeNotifier{},
		OAuth:    &fakeOAuth{user: gu, redirect: redirect},
		Config:   config.AuthConfig{SessionIdleTTL: time.Hour, Hosts: testHosts("test")},
	})
	return uc, repo
}

func TestGoogleAuth_LinkedSignsIn(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{GoogleID: "g-1", Email: "a@b.test", Name: "Jane"})
	repo.EXPECT().GetUserByProvider(gomock.Any(), postgres.GetUserByProviderParams{Provider: "google", ProviderUserID: "g-1"}).
		Return(postgres.User{ID: uid, Email: "a@b.test", Role: "user"}, nil)
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(postgres.Session{ID: uuid.Must(uuid.NewV7()), UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}, nil)

	cookie, redirect, err := uc.GoogleAuth(context.Background(), "code", "state", authModel.SessionMetadata{})
	if err != nil {
		t.Fatalf("GoogleAuth: %v", err)
	}
	if cookie == "" {
		t.Fatal("expected non-empty cookie")
	}
	if redirect != "https://id.test/profile" {
		t.Fatalf("want default profile redirect, got %q", redirect)
	}
}

func TestGoogleAuth_UnlinkedReturnsNotRegistered(t *testing.T) {
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{GoogleID: "g-2", Email: "new@b.test", Name: "New"})
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	// No CreateUser / CreateUserProvider expected — sign-in never creates.

	_, _, err := uc.GoogleAuth(context.Background(), "code", "state", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthGoogleNotRegistered.Err()) {
		t.Fatalf("want ErrAuthGoogleNotRegistered, got %v", err)
	}
}

// Google already linked to an ACTIVE account: registering signs the user in
// (same session path as GoogleAuth) instead of failing.
func TestBeginGoogleRegistration_LinkedActiveSignsIn(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{GoogleID: "g-1", Email: "a@b.test"})
	repo.EXPECT().GetUserByProvider(gomock.Any(), postgres.GetUserByProviderParams{Provider: "google", ProviderUserID: "g-1"}).
		Return(postgres.User{ID: uid, Email: "a@b.test", Role: "user", Status: string(userModel.UserStatusActive)}, nil)
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.CreateSessionParams) (postgres.Session, error) {
			if arg.UserID != uid {
				return postgres.Session{}, fmt.Errorf("session for wrong user: %v", arg.UserID)
			}
			return postgres.Session{ID: uuid.Must(uuid.NewV7()), UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}, nil
		})
	// No GetUserByEmail / CreateUser / CreateUserProvider — nothing is provisioned.

	res, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{})
	if err != nil {
		t.Fatalf("BeginGoogleRegistration: %v", err)
	}
	if res.SessionCookie == "" {
		t.Fatal("linked active account must get a session cookie")
	}
	if res.SetupToken != "" {
		t.Fatalf("linked active account must not get a setup token, got %q", res.SetupToken)
	}
	if res.Redirect != "https://id.test/profile" {
		t.Fatalf("want default landing redirect, got %q", res.Redirect)
	}
}

// Google already linked to an INCOMPLETE account: resume setup.
func TestBeginGoogleRegistration_LinkedIncompleteResumesSetup(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{GoogleID: "g-1", Email: "a@b.test"})
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).
		Return(postgres.User{ID: uid, Email: "a@b.test", Role: "user", Status: string(userModel.UserStatusIncomplete)}, nil)
	// No CreateSession — setup is not finished.

	res, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{})
	if err != nil {
		t.Fatalf("BeginGoogleRegistration: %v", err)
	}
	if res.SetupToken == "" || res.SessionCookie != "" {
		t.Fatalf("want setup token only, got %+v", res)
	}
}

func TestBeginGoogleRegistration_NewEmailCreatesIncomplete(t *testing.T) {
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{GoogleID: "g-2", Email: "new@b.test", Name: "New"})
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).Return(postgres.User{ID: uuid.Must(uuid.NewV7())}, nil)
	repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)

	res, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{})
	if err != nil || res.SetupToken == "" || res.SessionCookie != "" {
		t.Fatalf("expected setup token only, got res=%+v err=%v", res, err)
	}
}

func TestBeginGoogleRegistration_NewEmailSplitsFirstLastName(t *testing.T) {
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{
		GoogleID: "g-5", Email: "vp@b.test",
		Name: "Volodymyr Porokhniak", GivenName: "Volodymyr", FamilyName: "Porokhniak",
	})
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "vp@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.CreateUserParams) (postgres.User, error) {
			if arg.FirstName != "Volodymyr" || arg.LastName != "Porokhniak" {
				return postgres.User{}, fmt.Errorf("want first/last split, got %q/%q", arg.FirstName, arg.LastName)
			}
			return postgres.User{ID: arg.ID}, nil
		})
	repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)

	if _, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{}); err != nil {
		t.Fatalf("BeginGoogleRegistration: %v", err)
	}
}

// The return_to embedded in the OAuth state survives registration.
func TestBeginGoogleRegistration_NewEmailCarriesReturnTo(t *testing.T) {
	uc, repo := newGoogleUCRedirect(t, &oauth.GoogleUser{GoogleID: "g-6", Email: "rt@b.test"}, "https://event.test/e/1")
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "rt@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).Return(postgres.User{ID: uuid.Must(uuid.NewV7())}, nil)
	repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)

	res, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{})
	if err != nil {
		t.Fatalf("BeginGoogleRegistration: %v", err)
	}
	if res.SetupToken == "" || res.ReturnTo != "https://event.test/e/1" {
		t.Fatalf("want setup token + return_to, got %+v", res)
	}
}

func TestBeginGoogleRegistration_LinkedActiveLandsOnReturnTo(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo := newGoogleUCRedirect(t, &oauth.GoogleUser{GoogleID: "g-1", Email: "a@b.test"}, "https://event.test/e/1")
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).
		Return(postgres.User{ID: uid, Role: "user", Status: string(userModel.UserStatusActive)}, nil)
	repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
		Return(postgres.Session{ID: uuid.Must(uuid.NewV7()), UserID: uid, ExpiresAt: time.Now().Add(time.Hour)}, nil)

	res, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{})
	if err != nil {
		t.Fatalf("BeginGoogleRegistration: %v", err)
	}
	if res.SessionCookie == "" || res.Redirect != "https://event.test/e/1" {
		t.Fatalf("want session + return_to landing, got %+v", res)
	}
}

func TestLinkGoogleToSetupFromOAuth_ReturnsStateReturnTo(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, repo := newGoogleUCRedirect(t, &oauth.GoogleUser{GoogleID: "g-7", Email: "s@b.test"}, "https://event.test/e/1")
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	setupToken := liveSetupLink(t, repo, tk, uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Email: "s@b.test", Status: string(userModel.UserStatusIncomplete)}, nil).AnyTimes()
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)

	returnTo, err := uc.LinkGoogleToSetupFromOAuth(context.Background(), setupToken, "code", "state")
	if err != nil {
		t.Fatalf("LinkGoogleToSetupFromOAuth: %v", err)
	}
	if returnTo != "https://event.test/e/1" {
		t.Fatalf("want state return_to, got %q", returnTo)
	}
}

func TestBeginGoogleRegistration_ActiveEmailBlocks(t *testing.T) {
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{GoogleID: "g-3", Email: "active@b.test"})
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "active@b.test").Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusActive)}, nil)

	if _, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{}); !errors.Is(err, authModel.ErrAuthAccountExistsSignIn.Err()) {
		t.Fatalf("want ErrAuthAccountExistsSignIn, got %v", err)
	}
}

func TestLinkGoogleToSetup_BadToken(t *testing.T) {
	uc, _ := newGoogleUC(t, nil)
	if err := uc.LinkGoogleToSetup(context.Background(), "garbage", "g-1", "a@b.test"); !errors.Is(err, authModel.ErrInvalidToken.Err()) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestLinkGoogleToSetup_DifferentUser(t *testing.T) {
	uc, repo := newGoogleUC(t, nil)
	uid := uuid.Must(uuid.NewV7())
	other := uuid.Must(uuid.NewV7())
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	setupToken := liveSetupLink(t, repo, tk, uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Email: "a@b.test", Status: string(userModel.UserStatusIncomplete)}, nil)
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{ID: other}, nil)

	if err := uc.LinkGoogleToSetup(context.Background(), setupToken, "g-1", "a@b.test"); !errors.Is(err, userModel.ErrUserExists.Err()) {
		t.Fatalf("want ErrUserExists, got %v", err)
	}
}

// TestBeginGoogleRegistration_IncompleteEmailLinks covers Branch 4 of BeginGoogleRegistration:
// an incomplete account already owns the email → link provider + confirm email + backfill name
// + return a setup token. No CreateUser call is expected.
func TestBeginGoogleRegistration_IncompleteEmailLinks(t *testing.T) {
	existingID := uuid.Must(uuid.NewV7())
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{GoogleID: "g-4", Email: "incomplete@b.test", Name: "Alice Smith"})

	// Provider not yet linked to anyone.
	repo.EXPECT().GetUserByProvider(gomock.Any(), postgres.GetUserByProviderParams{
		Provider: "google", ProviderUserID: "g-4",
	}).Return(postgres.User{}, pgx.ErrNoRows)

	// Existing incomplete account found by email (no name set, so backfill will trigger).
	repo.EXPECT().GetUserByEmail(gomock.Any(), "incomplete@b.test").
		Return(postgres.User{ID: existingID, Status: string(userModel.UserStatusIncomplete), FirstName: "", LastName: ""}, nil)

	// Provider link created for the existing user.
	repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)

	// One aggregate write: email confirmed + name backfilled.
	repo.EXPECT().GetUserByID(gomock.Any(), existingID).
		Return(postgres.User{ID: existingID, Status: string(userModel.UserStatusIncomplete)}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if !arg.EmailConfirmed || arg.FirstName != "Alice" || arg.LastName != "Smith" {
				return 0, fmt.Errorf("link must confirm email and backfill name: %+v", arg)
			}
			return 1, nil
		})

	res, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{})
	if err != nil {
		t.Fatalf("BeginGoogleRegistration Branch 4: unexpected error: %v", err)
	}
	if res.SetupToken == "" {
		t.Fatal("BeginGoogleRegistration Branch 4: expected non-empty setup token, got empty string")
	}
}

// TestGetGoogleLoginURL_Unconfigured reproduces the Go nil-interface gotcha described in
// the auth Slice 3 review finding. When a typed-nil *oauth.Client is assigned to the
// IOAuthClient interface field in Dependencies, the interface itself is non-nil (type set,
// value nil). NewAuthUseCase must detect this via reflection and treat it as "unconfigured",
// so GetGoogleLoginURL returns an error instead of panicking on a nil-pointer deref.
func TestGetGoogleLoginURL_Unconfigured(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	var nilClient *oauth.Client // typed nil — wraps nil in the interface
	uc := auth.NewAuthUseCase(auth.Dependencies{Sessions: testSessions(t),
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: &fakeNotifier{},
		OAuth:    nilClient, // non-nil interface wrapping nil concrete pointer
		Config:   config.AuthConfig{},
	})
	if _, _, err := uc.GetGoogleLoginURL(""); err == nil {
		t.Fatal("expected an error when OAuth is unconfigured (typed-nil interface), got nil")
	}
}

func TestUnlinkGoogle_LastMethodBlocked(t *testing.T) {
	uc, repo := newGoogleUC(t, nil)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, HashedPassword: hashedPassword(t, "Correct!1")}, nil)
	repo.EXPECT().CountUserLoginMethods(gomock.Any(), uid).Return(int64(1), nil)
	if err := uc.UnlinkGoogle(context.Background(), uid, "Correct!1"); !errors.Is(err, authModel.ErrNoLoginMethod.Err()) {
		t.Fatalf("want ErrNoLoginMethod, got %v", err)
	}
}

func TestUnlinkGoogle_Success(t *testing.T) {
	uc, repo := newGoogleUC(t, nil)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, HashedPassword: hashedPassword(t, "Correct!1")}, nil)
	repo.EXPECT().CountUserLoginMethods(gomock.Any(), uid).Return(int64(2), nil)
	repo.EXPECT().DeleteUserProvider(gomock.Any(), postgres.DeleteUserProviderParams{UserID: uid, Provider: "google"}).Return(int64(1), nil)
	if err := uc.UnlinkGoogle(context.Background(), uid, "Correct!1"); err != nil {
		t.Fatalf("UnlinkGoogle: %v", err)
	}
}

// TestLinkGoogleProvider_SameUserIsNoOp covers the idempotency path inside linkGoogleProvider:
// when GetUserByProvider returns a record already belonging to the same user, the function
// must return nil without calling CreateUserProvider. Tested indirectly via LinkGoogleToSetup.
func TestLinkGoogleProvider_SameUserIsNoOp(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})

	uc, repo := newGoogleUC(t, nil)
	setupToken := liveSetupLink(t, repo, tk, uid)

	// User exists and is incomplete.
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Email: "a@b.test", Status: string(userModel.UserStatusIncomplete)}, nil)

	// GetUserByProvider returns the SAME user → idempotent no-op.
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).
		Return(postgres.User{ID: uid}, nil)

	// CreateUserProvider must NOT be called — gomock will fail the test if it is.

	if err := uc.LinkGoogleToSetup(context.Background(), setupToken, "g-same", "a@b.test"); err != nil {
		t.Fatalf("same-user idempotency: expected nil, got %v", err)
	}
}

// M2: GET /auth/google/setup?token=<attacker token> makes the VICTIM's Google
// identity reach LinkGoogleToSetup for the attacker's account. The identity
// must not be bound unless its verified email is the account's own address.
func TestLinkGoogleToSetup_ForeignGoogleEmailRefused(t *testing.T) {
	uc, repo := newGoogleUC(t, nil)
	uid := uuid.Must(uuid.NewV7())
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	setupToken := liveSetupLink(t, repo, tk, uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Email: "attacker@evil.test", Status: string(userModel.UserStatusIncomplete)}, nil)
	// No GetUserByProvider / CreateUserProvider: nothing may be linked.

	err := uc.LinkGoogleToSetup(context.Background(), setupToken, "victim-google-id", "victim@b.test")
	if !errors.Is(err, authModel.ErrAuthGoogleEmailMismatch.Err()) {
		t.Fatalf("want ErrAuthGoogleEmailMismatch, got %v", err)
	}
}

func TestLinkGoogleToSetup_EmailMatchIsCaseInsensitive(t *testing.T) {
	uc, repo := newGoogleUC(t, nil)
	uid := uuid.Must(uuid.NewV7())
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	setupToken := liveSetupLink(t, repo, tk, uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).
		Return(postgres.User{ID: uid, Email: "alice@b.test", Status: string(userModel.UserStatusIncomplete)}, nil)
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)

	if err := uc.LinkGoogleToSetup(context.Background(), setupToken, "g-1", "Alice@B.test"); err != nil {
		t.Fatalf("LinkGoogleToSetup: %v", err)
	}
}

// H1: an unverified Google email surfaces as a client error, never as a
// registration step.
func TestBeginGoogleRegistration_UnverifiedEmailRefused(t *testing.T) {
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := auth.NewAuthUseCase(auth.Dependencies{Sessions: testSessions(t),
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: &fakeNotifier{},
		OAuth:    &fakeOAuth{err: oauth.ErrGoogleEmailNotVerified},
		Config:   config.AuthConfig{SessionIdleTTL: time.Hour, Hosts: testHosts("test")},
	})
	_, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthGoogleEmailNotVerified.Err()) {
		t.Fatalf("want ErrAuthGoogleEmailNotVerified, got %v", err)
	}
}

// A mixed-case Google address finds the (lower-cased) account instead of
// creating a duplicate one.
func TestBeginGoogleRegistration_EmailIsNormalized(t *testing.T) {
	uc, repo := newGoogleUC(t, &oauth.GoogleUser{GoogleID: "g-9", Email: " Alice@B.test "})
	repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "alice@b.test").
		Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusActive)}, nil)
	if _, err := uc.BeginGoogleRegistration(context.Background(), "code", "state", authModel.SessionMetadata{}); !errors.Is(err, authModel.ErrAuthAccountExistsSignIn.Err()) {
		t.Fatalf("want ErrAuthAccountExistsSignIn, got %v", err)
	}
}

// L7: giving away the Google login needs the password too.
func TestUnlinkGoogle_WrongPasswordRefused(t *testing.T) {
	uc, repo := newGoogleUC(t, nil)
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, HashedPassword: hashedPassword(t, "Correct!1")}, nil)
	if err := uc.UnlinkGoogle(context.Background(), uid, "Wrong!1"); !errors.Is(err, authModel.ErrAuthInvalidOldPassword.Err()) {
		t.Fatalf("want ErrAuthInvalidOldPassword, got %v", err)
	}
}
