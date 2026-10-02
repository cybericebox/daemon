package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/tools"
)

// BeginEmailRegistration creates (or reuses) an incomplete account and emails a
// setup link. Active accounts receive a security notification and return nil; the
// flow does not reveal whether the address already exists. A trusted redirect is
// carried on the setup link as return_to; an untrusted one is dropped.
func (u *AuthUseCase) BeginEmailRegistration(ctx context.Context, rawEmail, redirect string) error {
	emailAddr, err := parseEmail(rawEmail)
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
		if _, err := u.users.Create(ctx, userModel.NewIncompleteUser(userID, emailAddr, time.Now())); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to create user").Err()
		}
	case user.Status == userModel.UserStatusActive:
		// Do not reveal existence. Warn the real owner via a security email.
		override := userModel.User{ID: user.ID, Email: emailAddr, FirstName: user.FirstName}
		if err := u.notifier.Notify(ctx, user.ID, notificationPayloads.AccountExistsPayload{Name: user.FirstName},
			dispatchModel.WithRecipient(override)); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to send account-exists email").Err()
		}
		return nil
	default:
		// Incomplete account already exists — reuse it, re-issue the setup link.
	}

	return u.sendContinueRegistration(ctx, userID, user.FirstName, u.trustedReturnTo(redirect))
}

// sendContinueRegistration issues a setup token for an incomplete account and
// emails the setup link (with return_to when returnTo is non-empty).
func (u *AuthUseCase) sendContinueRegistration(ctx context.Context, userID uuid.UUID, name, returnTo string) error {
	setupToken, err := u.token.GenerateSetupToken(userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to generate setup token").Err()
	}

	link := u.cfg.Hosts.IDURL(fmt.Sprintf("/setup?token=%s", setupToken))
	if returnTo != "" {
		link += "&return_to=" + url.QueryEscape(returnTo)
	}
	if err = u.notifier.Notify(ctx, userID, notificationPayloads.ContinueRegistrationPayload{
		Name:            name,
		RegistrationURL: link,
	}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to send registration email").Err()
	}
	return nil
}

// GetSetupContext resolves a setup token to the registration-completion screen.
func (u *AuthUseCase) GetSetupContext(ctx context.Context, setupToken string) (*SetupContext, error) {
	userID, err := u.token.ParseSetupToken(setupToken)
	if err != nil {
		return nil, authModel.ErrInvalidToken.WithError(fmt.Errorf("setup-context: parse setup token: %w", err)).Err()
	}
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, authModel.ErrInvalidToken.WithError(errors.New("setup-context: user behind setup token no longer exists")).Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user for setup").Err()
	}
	if user.Status != userModel.UserStatusIncomplete {
		return nil, authModel.ErrSetupAlreadyComplete.Err()
	}
	hasProvider, err := u.hasLinkedProvider(ctx, userID)
	if err != nil {
		return nil, err
	}
	return &SetupContext{
		Email:       user.Email,
		FirstName:   user.FirstName,
		LastName:    user.LastName,
		HasProvider: hasProvider,
	}, nil
}

// CompleteRegistration finalizes an incomplete account: the entity enforces
// the setup invariants (CompleteSetup), the aggregate is written in ONE
// UPDATE — the old 5-statement unit of work is gone — and the user is signed
// in. The setup token is single-use (rejected once active).
func (u *AuthUseCase) CompleteRegistration(
	ctx context.Context,
	setupToken, firstName, lastName, plainPassword string,
	tosVersion int32,
	redirect string,
	meta authModel.SessionMetadata,
) (sessionCookie, code string, err error) {
	userID, err := u.token.ParseSetupToken(setupToken)
	if err != nil {
		return "", "", authModel.ErrInvalidToken.WithError(fmt.Errorf("complete-registration: parse setup token: %w", err)).Err()
	}
	var hashedPassword string
	if plainPassword != "" {
		if err = u.password.CheckPasswordComplexity(plainPassword); err != nil {
			return "", "", authModel.ErrAuthInvalidPasswordComplexity.WithError(err).Err()
		}
		if hashedPassword, err = u.password.Hash(plainPassword); err != nil {
			return "", "", model.ErrPlatform.WithError(err).WithMessage("Failed to hash password").Err()
		}
	}

	// Setup speaks the anti-enumeration ErrInvalidToken (never reveal a user
	// existed behind the token) for both a read miss and a lost-race re-read miss;
	// a row still present on the re-read yields ErrUserModified from the helper.
	// mutateUserReturning owns the optimistic lock (snapshot BEFORE the mutation),
	// so the CompleteSetup/ChangeRole touches can't leak into the guard.
	tokenUserGone := authModel.ErrInvalidToken.WithError(errors.New("complete-registration: user behind setup token no longer exists")).Err()
	user, err := u.mutateUserReturning(ctx, userID, tokenUserGone, func(user *userModel.User) error {
		// Every setup invariant (already-complete, ToS, login method) lives on the
		// entity; the provider lookup runs lazily only when no password was set.
		if err := user.CompleteSetup(firstName, lastName, hashedPassword, tosVersion, func() (bool, error) {
			return u.hasLinkedProvider(ctx, userID)
		}, time.Now()); err != nil {
			return err
		}
		if u.isDesignatedSuperAdmin(user.Email) {
			return user.ChangeRole(rbac.RoleSuperAdmin, time.Now())
		}
		return nil
	})
	if err != nil {
		return "", "", err
	}

	safeRedirect := u.resolveRedirect(redirect)
	cookie, err := u.createSession(ctx, user.ID, meta)
	if err != nil {
		return "", "", err
	}
	return cookie, safeRedirect, nil
}

// hasLinkedProvider reports whether the user has at least one linked social provider.
func (u *AuthUseCase) hasLinkedProvider(ctx context.Context, userID uuid.UUID) (bool, error) {
	providers, err := u.users.ListProviders(ctx, userID)
	if err != nil {
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to get user providers").Err()
	}
	return len(providers) > 0, nil
}
