package auth

import (
	"context"
	"time"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// PromoteSuperAdminIfDesignated ensures the configured SuperAdminEmail account,
// if it already exists, holds the super_admin role. No-op when the email is
// unset, the account does not exist yet, or it is already super_admin. Called at
// startup after migrations; the registration path handles the not-yet-registered
// case.
func (u *AuthUseCase) PromoteSuperAdminIfDesignated(ctx context.Context) error {
	if u.cfg.SuperAdminEmail == "" {
		return nil
	}
	user, err := u.users.GetByEmail(ctx, normalizeEmail(u.cfg.SuperAdminEmail))
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to look up designated super admin").Err()
	}
	// The startup path only repairs existing, fully-registered accounts. A
	// designated email that has not finished registration is promoted by the
	// register path (CompleteRegistration), which sets active + super_admin
	// atomically — promoting an incomplete account here would crown a
	// half-registered (possibly passwordless) user.
	if user.Status != userModel.UserStatusActive {
		return nil
	}
	if user.Role == rbac.RoleSuperAdmin {
		return nil
	}
	return u.mutateUser(ctx, user.ID, func(target *userModel.User) error {
		return target.ChangeRole(rbac.RoleSuperAdmin, time.Now())
	})
}
