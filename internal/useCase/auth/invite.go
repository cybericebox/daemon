package auth

import (
	"context"
	"errors"
	"fmt"
	"net/mail"
	"strings"
	"time"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/tools"
)

// InviteUser invites an email under a role (account-first). The created/updated
// account is email-confirmed (authorized by an admin); the invitee finishes via
// the normal setup link. Requires users.invite and authority to assign the role.
func (u *AuthUseCase) InviteUser(ctx context.Context, emailAddr string, role rbac.Role, firstName, lastName string) error {
	if err := u.assertCanInvite(ctx, role); err != nil {
		return err
	}
	if err := userModel.ValidName(firstName, lastName); err != nil {
		return err
	}

	emailAddr, err := parseEmail(emailAddr)
	if err != nil {
		return err
	}
	user, dbErr := u.users.GetByEmail(ctx, emailAddr)
	if dbErr != nil && !repositoryTools.IsObjectNotFoundError(dbErr) {
		return model.ErrPlatform.WithError(dbErr).WithMessage("Failed to get user by email").Err()
	}

	userID := user.ID
	switch {
	case repositoryTools.IsObjectNotFoundError(dbErr):
		userID = tools.NewUUIDv7()
		if _, err := u.users.Create(ctx, userModel.NewInvitedUser(userID, emailAddr, firstName, lastName, role, time.Now())); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to create user").Err()
		}
	case user.Status == userModel.UserStatusIncomplete:
		// Incomplete account exists — re-invite: update role, re-send link.
		// A provider bound while the account was still unclaimed was bound by
		// whoever got there first, not by the invitee: drop it, so the invited
		// role (possibly admin) can only be claimed through the invitation link.
		if _, err := u.users.DeleteProviders(ctx, userID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to reset provider links").Err()
		}
		if err := u.mutateUser(ctx, userID, func(user *userModel.User) error {
			return user.ChangeRole(role, time.Now())
		}); err != nil {
			return err
		}
	default:
		// active, blocked, or any other status — cannot invite over it.
		return userModel.ErrUserExists.WithError(errors.New("invite: account already exists in a non-incomplete status")).Err()
	}

	// A new invitation link replaces the previous one of the account.
	setupToken, err := u.setupTokens.GenerateSetupToken(ctx, userID, 0)
	if err != nil {
		return err
	}

	override := userModel.User{ID: userID, Email: emailAddr, FirstName: firstName}
	if err = u.notifier.Notify(ctx, userID, notificationPayloads.UserInvitationPayload{
		InviteURL: u.cfg.Hosts.IDURL(fmt.Sprintf("/setup?token=%s", setupToken)),
	}, dispatchModel.WithRecipient(override)); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to send invitation email").Err()
	}
	// A (re)sent link restarts the 30-day clock of the unconfirmed account.
	if _, err = u.users.MarkInvitationSent(ctx, userID, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record invitation delivery").Err()
	}
	return nil
}

// InviteUsers bulk-invites under one role; validates authority once, dedups, and
// collects a per-email outcome so one bad address does not abort the batch.
func (u *AuthUseCase) InviteUsers(ctx context.Context, role rbac.Role, emails []string) ([]InviteResult, error) {
	if err := u.assertCanInvite(ctx, role); err != nil {
		return nil, err
	}
	results := make([]InviteResult, 0, len(emails))
	seen := make(map[string]struct{}, len(emails))
	for _, emailAddr := range emails {
		if _, dup := seen[emailAddr]; dup {
			continue
		}
		seen[emailAddr] = struct{}{}
		results = append(results, InviteResult{Email: emailAddr, Err: u.InviteUser(ctx, emailAddr, role, "", "")})
	}
	return results, nil
}

// assertCanInvite checks role validity and the subset rule for the role being
// granted (the users.invite permission itself is enforced on the route).
func (u *AuthUseCase) assertCanInvite(ctx context.Context, role rbac.Role) error {
	if !rbac.ValidRole(string(role)) {
		return authModel.ErrInvalidRole.Err()
	}
	caller, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok {
		return authModel.ErrAuthInvalidSession.Err()
	}
	// An invitation creates the account at that role: a raise like any other.
	if !rbac.CanSetRole(caller.Role, "", role) {
		return authModel.ErrCannotAssignRole.Err()
	}
	return nil
}

// Platform invitation outcome codes: the client shows its own text for each.
const (
	InviteCodeEmailInvalid  = "email_invalid"
	InviteCodeRoleInvalid   = "role_invalid"
	InviteCodeRoleForbidden = "role_forbidden"
	InviteCodeUserExists    = "user_exists"
	InviteCodeFailed        = "failed"
)

// maxInviteEntries bounds one platform invitation batch.
const maxInviteEntries = 200

// InviteEntry is one invitation line (dialog chip or CSV row). An empty Role
// means the default user role.
type InviteEntry struct {
	Email     string
	FirstName string
	LastName  string
	Role      rbac.Role
}

// InviteEntryResult: Code is empty on success.
type InviteEntryResult struct {
	Email string
	Role  rbac.Role
	Code  string
}

// InviteEntries invites each line under its own role. Every line is checked
// on its own (address, role, the caller's authority to grant it), so one bad
// line does not stop the rest. Addresses repeat-free, case-insensitively.
func (u *AuthUseCase) InviteEntries(ctx context.Context, entries []InviteEntry) ([]InviteEntryResult, error) {
	caller, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok {
		return nil, authModel.ErrAuthInvalidSession.Err()
	}
	if len(entries) == 0 || len(entries) > maxInviteEntries {
		return nil, authModel.ErrInviteBatchSize.Err()
	}
	results := make([]InviteEntryResult, 0, len(entries))
	seen := make(map[string]struct{}, len(entries))
	for _, entry := range entries {
		emailAddr := normalizeEmail(entry.Email)
		if _, dup := seen[emailAddr]; dup {
			continue
		}
		seen[emailAddr] = struct{}{}
		role := entry.Role
		if role == "" {
			role = rbac.RoleUser
		}
		result := InviteEntryResult{Email: emailAddr, Role: role}
		address, parseErr := mail.ParseAddress(emailAddr)
		switch {
		case parseErr != nil || address.Address != emailAddr || len(emailAddr) > 254:
			result.Code = InviteCodeEmailInvalid
		case !rbac.ValidRole(string(role)):
			result.Code = InviteCodeRoleInvalid
		case !rbac.CanSetRole(caller.Role, "", role):
			result.Code = InviteCodeRoleForbidden
		default:
			if err := u.InviteUser(ctx, emailAddr, role, strings.TrimSpace(entry.FirstName), strings.TrimSpace(entry.LastName)); err != nil {
				result.Code = InviteCodeFailed
				if errors.Is(err, userModel.ErrUserExists.Err()) {
					result.Code = InviteCodeUserExists
				}
			}
		}
		results = append(results, result)
	}
	return results, nil
}
