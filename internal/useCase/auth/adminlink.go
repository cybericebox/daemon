package auth

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/tools"
)

// AdminLinkTTL is the life of the setup link the operator command prints: short, because it is
// read off a terminal and used at once.
const AdminLinkTTL = 30 * time.Minute

var (
	// ErrAdminLinkNoSuperAdmin: SUPER_ADMIN_EMAIL is not configured, so there is nobody to issue a link for.
	ErrAdminLinkNoSuperAdmin = errors.New("SUPER_ADMIN_EMAIL is not set")
	// ErrAdminLinkAccountActive: the super admin already finished the setup; the link is for the first sign-up only.
	ErrAdminLinkAccountActive = errors.New("account is active; use password reset")
)

// AdminLink is a setup link printed for the operator and the moment it stops working.
type AdminLink struct {
	URL       string
	ExpiresAt time.Time
}

// IssueSuperAdminSetupLink is the bootstrap for a platform without a mail provider: it makes (or
// reuses) the incomplete account of the configured SUPER_ADMIN_EMAIL and returns the same single-use
// setup link the sign-up email would carry, living AdminLinkTTL. It takes no address on purpose: the
// link can be issued for the designated super admin and nobody else. Issuing replaces the account's
// earlier setup links. The link is returned to the caller only; it is never logged or stored.
func (u *AuthUseCase) IssueSuperAdminSetupLink(ctx context.Context) (AdminLink, error) {
	if u.cfg.SuperAdminEmail == "" {
		return AdminLink{}, ErrAdminLinkNoSuperAdmin
	}
	emailAddr, err := parseEmail(u.cfg.SuperAdminEmail)
	if err != nil {
		return AdminLink{}, err
	}
	user, dbErr := u.users.GetByEmail(ctx, emailAddr)
	if dbErr != nil && !repositoryTools.IsObjectNotFoundError(dbErr) {
		return AdminLink{}, model.ErrPlatform.WithError(dbErr).WithMessage("Failed to get user by email").Err()
	}
	userID := user.ID
	switch {
	case repositoryTools.IsObjectNotFoundError(dbErr):
		userID = tools.NewUUIDv7()
		if _, err = u.users.Create(ctx, userModel.NewIncompleteUser(userID, emailAddr, time.Now())); err != nil {
			return AdminLink{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create user").Err()
		}
	case user.Status == userModel.UserStatusActive:
		return AdminLink{}, ErrAdminLinkAccountActive
	}

	expiresAt := time.Now().Add(AdminLinkTTL)
	link, err := u.issueSetupLink(ctx, userID, AdminLinkTTL, "")
	if err != nil {
		return AdminLink{}, err
	}
	// The audit line names the account, never the link.
	log.Info().Str("user_id", userID.String()).Msg("Admin setup link issued for the super admin")
	return AdminLink{URL: link, ExpiresAt: expiresAt}, nil
}
