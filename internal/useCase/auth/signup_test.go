package auth_test

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	payloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

func newSignupUC(t *testing.T) (*auth.AuthUseCase, *postgresMocks.MockQuerier, *token.Client, *fakeNotifier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	tk := token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"})
	notifier := &fakeNotifier{}
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:     repo,
		Token:    tk,
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: notifier,
		Config:   config.AuthConfig{SessionIdleTTL: time.Hour, TemporalCodeTTL: time.Hour, Hosts: testHosts("example.test")},
		// The mail work runs after the answer in production; the tests wait for it.
		Background: func(work func()) { work() },
	})
	return uc, repo, tk, notifier
}

func TestBeginEmailRegistration_NewAccount(t *testing.T) {
	uc, repo, _, notifier := newSignupUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).Return(postgres.User{ID: uuid.Must(uuid.NewV7())}, nil)

	if err := uc.BeginEmailRegistration(context.Background(), "new@b.test", ""); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("want 1 notify, got %d", notifier.calls)
	}
}

func TestBeginEmailRegistration_ActiveExists(t *testing.T) {
	uc, repo, _, notifier := newSignupUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "a@b.test").Return(postgres.User{
		ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusActive),
	}, nil)

	if err := uc.BeginEmailRegistration(context.Background(), "a@b.test", ""); err != nil {
		t.Fatalf("want nil (account-exists email sent), got %v", err)
	}
	if notifier.calls != 1 || notifier.lastRecipientEmail != "a@b.test" {
		t.Fatalf("notify: calls=%d recipient=%q", notifier.calls, notifier.lastRecipientEmail)
	}
}

func TestBeginEmailRegistration_IncompleteReuses(t *testing.T) {
	uc, repo, _, notifier := newSignupUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "i@b.test").Return(postgres.User{
		ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusIncomplete),
	}, nil)
	// No CreateUser expected — the incomplete account is reused.

	if err := uc.BeginEmailRegistration(context.Background(), "i@b.test", ""); err != nil {
		t.Fatalf("begin: %v", err)
	}
	if notifier.calls != 1 {
		t.Fatalf("want 1 notify, got %d", notifier.calls)
	}
}

// registrationURL returns the setup link of the last continue-registration email.
func registrationURL(t *testing.T, n *fakeNotifier) string {
	t.Helper()
	p, ok := n.lastPayload.(payloads.ContinueRegistrationPayload)
	if !ok {
		t.Fatalf("want ContinueRegistrationPayload, got %T", n.lastPayload)
	}
	return p.RegistrationURL
}

func TestBeginEmailRegistration_TrustedRedirectAddsReturnTo(t *testing.T) {
	uc, repo, _, notifier := newSignupUC(t)
	repo.EXPECT().GetUserByEmail(gomock.Any(), "new@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Any()).Return(postgres.User{ID: uuid.Must(uuid.NewV7())}, nil)

	if err := uc.BeginEmailRegistration(context.Background(), "new@b.test", "https://event.example.test/e/1?x=y"); err != nil {
		t.Fatalf("begin: %v", err)
	}
	got := registrationURL(t, notifier)
	if !strings.HasPrefix(got, "https://id.example.test/setup?token=") ||
		!strings.HasSuffix(got, "&return_to="+url.QueryEscape("https://event.example.test/e/1?x=y")) {
		t.Fatalf("want setup link with escaped return_to, got %q", got)
	}
}

func TestBeginEmailRegistration_UntrustedRedirectDropped(t *testing.T) {
	for _, redirect := range []string{"", "https://evil.test/x"} {
		uc, repo, _, notifier := newSignupUC(t)
		repo.EXPECT().GetUserByEmail(gomock.Any(), "i@b.test").Return(postgres.User{
			ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusIncomplete),
		}, nil)

		if err := uc.BeginEmailRegistration(context.Background(), "i@b.test", redirect); err != nil {
			t.Fatalf("begin: %v", err)
		}
		if got := registrationURL(t, notifier); strings.Contains(got, "return_to") {
			t.Fatalf("redirect %q must not produce return_to, got %q", redirect, got)
		}
	}
}

func TestGetSetupContext_AlreadyComplete(t *testing.T) {
	uc, repo, tk, _ := newSignupUC(t)
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: string(userModel.UserStatusActive)}, nil)

	if _, err := uc.GetSetupContext(context.Background(), setupToken); !errors.Is(err, authModel.ErrSetupAlreadyComplete.Err()) {
		t.Fatalf("want ErrSetupAlreadyComplete, got %v", err)
	}
}

func TestGetSetupContext_BadToken(t *testing.T) {
	uc, _, _, _ := newSignupUC(t)
	if _, err := uc.GetSetupContext(context.Background(), "garbage"); !errors.Is(err, authModel.ErrInvalidToken.Err()) {
		t.Fatalf("want ErrInvalidToken, got %v", err)
	}
}

func TestCompleteRegistration_NoPasswordNoProvider(t *testing.T) {
	uc, repo, tk, _ := newSignupUC(t)
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: string(userModel.UserStatusIncomplete)}, nil)
	repo.EXPECT().GetUserProviders(gomock.Any(), uid).Return([]postgres.UserProvider{}, nil)

	_, _, err := uc.CompleteRegistration(context.Background(), setupToken, "Jane", "Doe", "", 1, "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrNoLoginMethod.Err()) {
		t.Fatalf("want ErrNoLoginMethod, got %v", err)
	}
}

func TestCompleteRegistration_TosNotAccepted(t *testing.T) {
	uc, repo, tk, _ := newSignupUC(t)
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: string(userModel.UserStatusIncomplete)}, nil)

	_, _, err := uc.CompleteRegistration(context.Background(), setupToken, "Jane", "Doe", "Secret!1", 0, "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrTosNotAccepted.Err()) {
		t.Fatalf("want ErrTosNotAccepted, got %v", err)
	}
}

// TestCompleteRegistration_TosBeforeLoginMethod locks the mandated guard order:
// when both ToS is missing (tosVersion=0) AND no password/provider is supplied,
// the error must be ErrTosNotAccepted, not ErrNoLoginMethod.
// This test would FAIL on the inverted (pre-Fix-1) order.
func TestCompleteRegistration_TosBeforeLoginMethod(t *testing.T) {
	uc, repo, tk, _ := newSignupUC(t)
	uid := uuid.Must(uuid.NewV7())
	setupToken, _ := tk.GenerateSetupToken(uid)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Status: string(userModel.UserStatusIncomplete)}, nil)

	// empty password (no login method) AND tosVersion=0 (ToS not accepted)
	_, _, err := uc.CompleteRegistration(context.Background(), setupToken, "Jane", "Doe", "", 0, "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrTosNotAccepted.Err()) {
		t.Fatalf("want ErrTosNotAccepted (ToS guard fires first), got %v", err)
	}
}
