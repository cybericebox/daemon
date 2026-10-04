package auth_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

func adminLinkUC(t *testing.T, superAdmin string) (*auth.AuthUseCase, *postgresMocks.MockQuerier, *fakeNotifier) {
	t.Helper()
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	allowSetupLinkIssue(repo)
	notifier := &fakeNotifier{}
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: notifier,
		Config:   config.AuthConfig{SuperAdminEmail: superAdmin, SignupSetupTokenTTL: 24 * time.Hour, Hosts: testHosts("example.test")},
	})
	return uc, repo, notifier
}

func TestIssueSuperAdminSetupLink_NoUserCreatesIt(t *testing.T) {
	uc, repo, notifier := adminLinkUC(t, " Root@B.test ")
	repo.EXPECT().GetUserByEmail(gomock.Any(), "root@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	repo.EXPECT().CreateUser(gomock.Any(), gomock.Cond(func(p postgres.CreateUserParams) bool {
		return p.Email == "root@b.test" && p.Status == string(userModel.UserStatusIncomplete)
	})).Return(postgres.User{ID: uuid.Must(uuid.NewV7())}, nil)

	before := time.Now()
	link, err := uc.IssueSuperAdminSetupLink(context.Background())
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if !strings.HasPrefix(link.URL, "https://id.example.test/setup?token=") || strings.Contains(link.URL, "return_to") {
		t.Fatalf("want the setup link of the id host, got %q", link.URL)
	}
	if d := link.ExpiresAt.Sub(before); d < auth.AdminLinkTTL || d > auth.AdminLinkTTL+time.Minute {
		t.Fatalf("want expiry in %v, got %v", auth.AdminLinkTTL, d)
	}
	if notifier.calls != 0 {
		t.Fatalf("nothing is emailed, got %d notifications", notifier.calls)
	}
}

func TestIssueSuperAdminSetupLink_IncompleteGetsNewToken(t *testing.T) {
	uc, repo, _ := adminLinkUC(t, "root@b.test")
	id := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "root@b.test").Return(postgres.User{
		ID: id, Status: string(userModel.UserStatusIncomplete),
	}, nil)
	// No CreateUser expected; the earlier links are deleted and one is stored (allowSetupLinkIssue).

	link, err := uc.IssueSuperAdminSetupLink(context.Background())
	if err != nil || !strings.Contains(link.URL, "/setup?token=") {
		t.Fatalf("want a setup link, got %q, %v", link.URL, err)
	}
}

func TestIssueSuperAdminSetupLink_ActiveRefused(t *testing.T) {
	uc, repo, _ := adminLinkUC(t, "root@b.test")
	repo.EXPECT().GetUserByEmail(gomock.Any(), "root@b.test").Return(postgres.User{
		ID: uuid.Must(uuid.NewV7()), Status: string(userModel.UserStatusActive),
	}, nil)

	link, err := uc.IssueSuperAdminSetupLink(context.Background())
	if !errors.Is(err, auth.ErrAdminLinkAccountActive) || link.URL != "" {
		t.Fatalf("want ErrAdminLinkAccountActive and no link, got %q, %v", link.URL, err)
	}
}

// The method takes no address: only the configured one is ever looked up, and without one nothing runs.
func TestIssueSuperAdminSetupLink_OnlyTheConfiguredAddress(t *testing.T) {
	uc, _, _ := adminLinkUC(t, "")
	// no repo call is expected: the strict mock fails the test on any
	if _, err := uc.IssueSuperAdminSetupLink(context.Background()); !errors.Is(err, auth.ErrAdminLinkNoSuperAdmin) {
		t.Fatalf("want ErrAdminLinkNoSuperAdmin, got %v", err)
	}
}
