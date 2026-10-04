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
	// Whether the address is new, half-registered or taken decides how much work follows; all of it
	// runs after the answer, so the answer's timing says nothing about the account.
	returnTo := u.trustedReturnTo(redirect)
	exists := !repositoryTools.IsObjectNotFoundError(dbErr)
	u.runInBackground("sign-up", func(ctx context.Context) error {
		return u.registerOrNotify(ctx, emailAddr, returnTo, user, exists)
	})
	return nil
}

// registerOrNotify is the work behind BeginEmailRegistration for one address.
func (u *AuthUseCase) registerOrNotify(ctx context.Context, emailAddr, returnTo string, user userModel.User, exists bool) error {
	userID := user.ID
	switch {
	case !exists:
		userID = tools.NewUUIDv7()
		if _, err := u.users.Create(ctx, userModel.NewIncompleteUser(userID, emailAddr, time.Now())); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to create user").Err()
		}
	case user.Status == userModel.UserStatusActive:
		// Do not reveal existence. Warn the real owner via a security email.
		if !u.mailAllowed(mailKindSignUp, emailAddr) {
			return nil
		}
		override := userModel.User{ID: user.ID, Email: emailAddr, FirstName: user.FirstName}
		if err := u.notifier.Notify(ctx, user.ID, notificationPayloads.AccountExistsPayload{Name: user.FirstName},
			dispatchModel.WithRecipient(override)); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to send account-exists email").Err()
		}
		return nil
	default:
		// Incomplete account already exists — reuse it, re-issue the setup link.
	}

	return u.sendContinueRegistration(ctx, userID, emailAddr, user.FirstName, returnTo)
}

// issueSetupLink issues a single-use setup token for an incomplete account living ttl (<= 0: the
// default) and returns the setup link (with return_to when returnTo is non-empty). It sends nothing.
func (u *AuthUseCase) issueSetupLink(ctx context.Context, userID uuid.UUID, ttl time.Duration, returnTo string) (string, error) {
	setupToken, err := u.setupTokens.GenerateSetupToken(ctx, userID, ttl)
	if err != nil {
		return "", err
	}
	link := u.cfg.Hosts.IDURL(fmt.Sprintf("/setup?token=%s", setupToken))
	if returnTo != "" {
		link += "&return_to=" + url.QueryEscape(returnTo)
	}
	return link, nil
}

// sendContinueRegistration issues a setup token for an incomplete account and
// emails the setup link (with return_to when returnTo is non-empty).
func (u *AuthUseCase) sendContinueRegistration(ctx context.Context, userID uuid.UUID, emailAddr, name, returnTo string) error {
	// Over the per-recipient quota the answer stays the neutral success of the
	// callers (sign-up and forgot-password never reveal an account).
	if !u.mailAllowed(mailKindSignUp, emailAddr) {
		return nil
	}
	link, err := u.issueSetupLink(ctx, userID, u.cfg.SignupSetupTokenTTL, returnTo)
	if err != nil {
		return err
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
	userID, err := u.setupTokens.Verify(ctx, setupToken)
	if err != nil {
		return nil, err
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
	userID, err := u.setupTokens.Verify(ctx, setupToken)
	if err != nil {
		return "", "", err
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

	// The link is spent: a second use (or a stolen copy) finds nothing.
	if err = u.setupTokens.Revoke(ctx, user.ID); err != nil {
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
