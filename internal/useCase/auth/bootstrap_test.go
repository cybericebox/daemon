package auth_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

func newBootstrapUC(t *testing.T, email string) (*auth.AuthUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Notifier: &fakeNotifier{},
		Config:   config.AuthConfig{TemporalCodeTTL: time.Hour, SuperAdminEmail: email},
	})
	return uc, repo
}

func TestPromoteSuperAdmin_PromotesExistingNonSuperAdmin(t *testing.T) {
	uc, repo := newBootstrapUC(t, "root@b.test")
	uid := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByEmail(gomock.Any(), "root@b.test").Return(postgres.User{ID: uid, Email: "root@b.test", Role: string(rbac.RoleAdmin), Status: string(userModel.UserStatusActive)}, nil)
	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Email: "root@b.test", Role: string(rbac.RoleAdmin), Status: string(userModel.UserStatusActive)}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Role != string(rbac.RoleSuperAdmin) {
				return 0, fmt.Errorf("promotion must write super_admin, got %s", arg.Role)
			}
			return 1, nil
		})
	if err := uc.PromoteSuperAdminIfDesignated(context.Background()); err != nil {
		t.Fatalf("promote: %v", err)
	}
}

func TestPromoteSuperAdmin_NoopWhenAlreadySuperAdmin(t *testing.T) {
	uc, repo := newBootstrapUC(t, "root@b.test")
	repo.EXPECT().GetUserByEmail(gomock.Any(), "root@b.test").Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Role: string(rbac.RoleSuperAdmin), Status: string(userModel.UserStatusActive)}, nil)
	// no UpdateUserRole expected
	if err := uc.PromoteSuperAdminIfDesignated(context.Background()); err != nil {
		t.Fatalf("promote: %v", err)
	}
}

func TestPromoteSuperAdmin_NoopWhenIncomplete(t *testing.T) {
	uc, repo := newBootstrapUC(t, "root@b.test")
	// A half-registered account must not be crowned super_admin at startup —
	// the register path (CompleteRegistration) handles it atomically instead.
	repo.EXPECT().GetUserByEmail(gomock.Any(), "root@b.test").Return(postgres.User{ID: uuid.Must(uuid.NewV7()), Email: "root@b.test", Role: string(rbac.RoleUser), Status: string(userModel.UserStatusIncomplete)}, nil)
	// no UpdateUserRole expected
	if err := uc.PromoteSuperAdminIfDesignated(context.Background()); err != nil {
		t.Fatalf("promote: %v", err)
	}
}

func TestPromoteSuperAdmin_NoopWhenUserAbsent(t *testing.T) {
	uc, repo := newBootstrapUC(t, "root@b.test")
	repo.EXPECT().GetUserByEmail(gomock.Any(), "root@b.test").Return(postgres.User{}, pgx.ErrNoRows)
	if err := uc.PromoteSuperAdminIfDesignated(context.Background()); err != nil {
		t.Fatalf("promote: %v", err)
	}
}

func TestPromoteSuperAdmin_NoopWhenEmailUnset(t *testing.T) {
	uc, _ := newBootstrapUC(t, "")
	// no repo calls expected when SuperAdminEmail is empty
	if err := uc.PromoteSuperAdminIfDesignated(context.Background()); err != nil {
		t.Fatalf("promote: %v", err)
	}
}
