package auth

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/oauth"
	"github.com/cybericebox/daemon/pkg/tools"
)

// GetGoogleLoginURL returns the Google OAuth2 consent URL and the state token.
// redirect is embedded (signed) in the state so it survives the round-trip to
// Google without relying on a cookie.
func (u *AuthUseCase) GetGoogleLoginURL(redirect string) (loginURL, state string, err error) {
	if !u.oauthConfigured {
		return "", "", model.ErrPlatform.WithMessage("OAuth is not configured").Err()
	}
	loginURL, state, err = u.oauth.GetGoogleLoginURL(redirect)
	if err != nil {
		return "", "", model.ErrPlatform.WithError(err).WithMessage("Failed to get google login url").Err()
	}
	return loginURL, state, nil
}

// resolveGoogleUser validates the OAuth callback and returns the verified
// Google profile plus the redirect embedded in the state at start time.
func (u *AuthUseCase) resolveGoogleUser(ctx context.Context, code, state string) (*oauth.GoogleUser, string, error) {
	if !u.oauthConfigured {
		return nil, "", model.ErrPlatform.WithMessage("OAuth is not configured").Err()
	}
	googleUser, redirect, err := u.oauth.GetGoogleUser(ctx, code, state)
	if err != nil {
		if errors.Is(err, oauth.ErrGoogleEmailNotVerified) {
			return nil, "", authModel.ErrAuthGoogleEmailNotVerified.WithError(err).Err()
		}
		return nil, "", model.ErrPlatform.WithError(err).WithMessage("Failed to get google user").Err()
	}
	return googleUser, redirect, nil
}

// GoogleAuth signs in the account linked to the Google identity. It NEVER creates an
// account or a provider link — an unlinked identity is told to register. safeRedirect
// is populated even on ErrAuthGoogleNotRegistered so the caller's error page can
// still carry a return_to.
func (u *AuthUseCase) GoogleAuth(
	ctx context.Context,
	code, state string,
	meta authModel.SessionMetadata,
) (sessionCookie, safeRedirect string, err error) {
	googleUser, stateRedirect, err := u.resolveGoogleUser(ctx, code, state)
	if err != nil {
		return "", "", err
	}
	safeRedirect = u.resolveRedirect(stateRedirect)

	user, err := u.users.GetByProvider(ctx, userModel.GoogleProvider, googleUser.GoogleID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return "", safeRedirect, authModel.ErrAuthGoogleNotRegistered.Err()
		}
		return "", safeRedirect, model.ErrPlatform.WithError(err).WithMessage("Failed to get user by provider").Err()
	}
	if err = refuseBlocked(&user); err != nil {
		return "", safeRedirect, err
	}

	cookie, err := u.createSession(ctx, user.ID, meta)
	if err != nil {
		return "", safeRedirect, err
	}
	return cookie, safeRedirect, nil
}

// BeginGoogleRegistration provisions or links an incomplete account from a verified
// Google profile. Exactly one of the result's SetupToken / SessionCookie is set:
// a Google identity already linked to a finished account signs that account in
// (the user is already registered), everything else continues to setup.
func (u *AuthUseCase) BeginGoogleRegistration(
	ctx context.Context,
	code, state string,
	meta authModel.SessionMetadata,
) (GoogleRegistrationResult, error) {
	googleUser, stateRedirect, err := u.resolveGoogleUser(ctx, code, state)
	if err != nil {
		return GoogleRegistrationResult{}, err
	}
	returnTo := u.trustedReturnTo(stateRedirect)
	googleUser.Email = normalizeEmail(googleUser.Email)

	// 1. Provider already linked → sign in (finished account) or resume setup.
	linked, provErr := u.users.GetByProvider(ctx, userModel.GoogleProvider, googleUser.GoogleID)
	if provErr != nil && !repositoryTools.IsObjectNotFoundError(provErr) {
		return GoogleRegistrationResult{}, model.ErrPlatform.WithError(provErr).WithMessage("Failed to get user by provider").Err()
	}
	if provErr == nil {
		if linked.IsIncomplete() {
			return u.setupResult(ctx, linked.ID, returnTo)
		}
		if err = refuseBlocked(&linked); err != nil {
			return GoogleRegistrationResult{}, err
		}
		// Same session path as GoogleAuth's sign-in.
		cookie, err := u.createSession(ctx, linked.ID, meta)
		if err != nil {
			return GoogleRegistrationResult{}, err
		}
		return GoogleRegistrationResult{SessionCookie: cookie, Redirect: u.resolveRedirect(stateRedirect)}, nil
	}

	existing, dbErr := u.users.GetByEmail(ctx, googleUser.Email)
	if dbErr != nil && !repositoryTools.IsObjectNotFoundError(dbErr) {
		return GoogleRegistrationResult{}, model.ErrPlatform.WithError(dbErr).WithMessage("Failed to get user by email").Err()
	}

	switch {
	case repositoryTools.IsObjectNotFoundError(dbErr):
		// 2. brand-new account — create incomplete + link Google.
		userID := tools.NewUUIDv7()
		// Picture stays empty: re-hosted to our storage below, never hot-linked.
		firstName, lastName := googleUser.FirstLastName()
		if _, err = u.users.Create(ctx, userModel.NewGoogleUser(userID, googleUser.Email, firstName, lastName, time.Now())); err != nil {
			return GoogleRegistrationResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create user").Err()
		}
		if err = u.users.LinkProvider(ctx, tools.NewUUIDv7(), userID, userModel.GoogleProvider, googleUser.GoogleID); err != nil {
			if ce, ok := repositoryTools.UniqueViolationError(err, userModel.ErrUserExists); ok {
				return GoogleRegistrationResult{}, ce.Err()
			}
			return GoogleRegistrationResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create user provider").Err()
		}
		u.adoptProviderAvatar(ctx, userID, googleUser.Picture)
		return u.setupResult(ctx, userID, returnTo)

	case existing.Status == userModel.UserStatusActive:
		// 3. an active account already owns this email — block (link from profile instead).
		return GoogleRegistrationResult{}, authModel.ErrAuthAccountExistsSignIn.Err()

	default:
		// 4. incomplete account exists — link Google, confirm email, backfill name.
		// The profile's email is verified by Google (rejected upstream otherwise)
		// and is the very address this account was found by, so the mailbox
		// owner is the one linking.
		if err = u.users.LinkProvider(ctx, tools.NewUUIDv7(), existing.ID, userModel.GoogleProvider, googleUser.GoogleID); err != nil {
			if ce, ok := repositoryTools.UniqueViolationError(err, userModel.ErrUserExists); ok {
				return GoogleRegistrationResult{}, ce.Err()
			}
			return GoogleRegistrationResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create user provider").Err()
		}
		// One aggregate write: provider-verified email confirmation plus the
		// adopted display name (when the account had none).
		if err = u.mutateUser(ctx, existing.ID, func(user *userModel.User) error {
			user.ConfirmEmail(time.Now())
			firstName, lastName := googleUser.FirstLastName()
			if user.FirstName == "" && user.LastName == "" && (firstName != "" || lastName != "") {
				user.UpdateProfile(firstName, lastName, time.Now())
			}
			return nil
		}); err != nil {
			return GoogleRegistrationResult{}, err
		}
		if existing.Picture == "" {
			u.adoptProviderAvatar(ctx, existing.ID, googleUser.Picture)
		}
		return u.setupResult(ctx, existing.ID, returnTo)
	}
}

// linkGoogleProvider links googleProviderID to userID. Idempotent if already linked
// to this same user; returns ErrUserExists if linked to a different account.
func (u *AuthUseCase) linkGoogleProvider(ctx context.Context, userID uuid.UUID, googleProviderID string) error {
	existing, provErr := u.users.GetByProvider(ctx, userModel.GoogleProvider, googleProviderID)
	if provErr != nil {
		if !repositoryTools.IsObjectNotFoundError(provErr) {
			return model.ErrPlatform.WithError(provErr).WithMessage("Failed to get user by provider").Err()
		}
		if err := u.users.LinkProvider(ctx, tools.NewUUIDv7(), userID, userModel.GoogleProvider, googleProviderID); err != nil {
			if ce, ok := repositoryTools.UniqueViolationError(err, userModel.ErrUserExists); ok {
				return ce.Err()
			}
			return model.ErrPlatform.WithError(err).WithMessage("Failed to create user provider").Err()
		}
		return nil
	}
	if existing.ID != userID {
		return userModel.ErrUserExists.WithError(errors.New("google-link: provider already linked to a different account")).Err()
	}
	return nil
}

// LinkGoogleToSetup links a Google provider to an incomplete account identified by a
// setup token. Status stays incomplete; the flip to active happens in CompleteRegistration.
//
// googleEmail is the Google-verified address of the identity: it must be the
// account's own address, otherwise anyone could bind THEIR Google identity (or,
// via a forced GET, a victim's) to an account set up by someone else.
func (u *AuthUseCase) LinkGoogleToSetup(ctx context.Context, setupToken, googleProviderID, googleEmail string) error {
	userID, err := u.setupTokens.Verify(ctx, setupToken)
	if err != nil {
		return err
	}
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return authModel.ErrInvalidToken.WithError(errors.New("google-link-setup: user behind setup token no longer exists")).Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user for setup").Err()
	}
	if user.Status != userModel.UserStatusIncomplete {
		return authModel.ErrSetupAlreadyComplete.Err()
	}
	if normalizeEmail(googleEmail) != normalizeEmail(user.Email) {
		return authModel.ErrAuthGoogleEmailMismatch.WithError(errors.New("google-link-setup: google email differs from the account email")).Err()
	}
	return u.linkGoogleProvider(ctx, userID, googleProviderID)
}

// LinkGoogleToSetupFromOAuth resolves a Google OAuth callback and links the provider
// to the incomplete account identified by setupToken. returnTo is the trusted
// return_to carried in the OAuth state; it is returned even when linking fails
// (so the caller can keep it on the setup page), "" when the state is invalid.
func (u *AuthUseCase) LinkGoogleToSetupFromOAuth(ctx context.Context, setupToken, code, state string) (returnTo string, err error) {
	googleUser, stateRedirect, err := u.resolveGoogleUser(ctx, code, state)
	if err != nil {
		return "", err
	}
	returnTo = u.trustedReturnTo(stateRedirect)
	return returnTo, u.LinkGoogleToSetup(ctx, setupToken, googleUser.GoogleID, googleUser.Email)
}

// LinkGoogleToAccountFromOAuth links a Google identity to the signed-in user.
// The OAuth callback runs outside the auth middleware, so identity comes from
// the session cookie. Rejects if the Google account is already linked elsewhere.
func (u *AuthUseCase) LinkGoogleToAccountFromOAuth(ctx context.Context, sessionCookieValue, code, state string) error {
	res, err := u.ValidateSessionCookie(ctx, sessionCookieValue)
	if err != nil {
		return err
	}
	googleUser, _, err := u.resolveGoogleUser(ctx, code, state)
	if err != nil {
		return err
	}
	return u.linkGoogleProvider(ctx, res.Claims.UserID, googleUser.GoogleID)
}

// UnlinkGoogle removes the user's Google link, refusing if it is the last login
// method (lockout guard). affected==0 (already unlinked) is a no-op.
func (u *AuthUseCase) UnlinkGoogle(ctx context.Context, userID uuid.UUID, currentPassword string) error {
	if err := u.reauthenticate(ctx, userID, currentPassword); err != nil {
		return err
	}
	methods, err := u.users.CountLoginMethods(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to count login methods").Err()
	}
	if methods <= 1 {
		return authModel.ErrNoLoginMethod.WithError(errors.New("google-unlink: would leave account without any login method")).Err()
	}
	if _, err = u.users.UnlinkProvider(ctx, userID, userModel.GoogleProvider); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to unlink google").Err()
	}
	return nil
}

// issueSetupToken generates a fresh setup token for userID.
// setupResult wraps a freshly issued setup token as a registration result.
func (u *AuthUseCase) setupResult(ctx context.Context, userID uuid.UUID, returnTo string) (GoogleRegistrationResult, error) {
	tok, err := u.issueSetupToken(ctx, userID)
	if err != nil {
		return GoogleRegistrationResult{}, err
	}
	return GoogleRegistrationResult{SetupToken: tok, ReturnTo: returnTo}, nil
}

func (u *AuthUseCase) issueSetupToken(ctx context.Context, userID uuid.UUID) (string, error) {
	// Someone who came through Google signs up themselves: the short lifetime.
	return u.setupTokens.GenerateSetupToken(ctx, userID, u.cfg.SignupSetupTokenTTL)
}
