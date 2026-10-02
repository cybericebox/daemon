package auth

import (
	"context"
	"errors"
	"time"

	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// GetAccount returns the signed-in user's account view (profile + providers + login methods).
func (u *AuthUseCase) GetAccount(ctx context.Context, userID uuid.UUID) (*AccountInfo, error) {
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	names, err := u.users.ListProviders(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user providers").Err()
	}
	return &AccountInfo{
		FirstName:      user.FirstName,
		LastName:       user.LastName,
		Email:          user.Email,
		Picture:        user.Picture,
		EmailConfirmed: user.EmailConfirmed,
		Role:           rbac.Role(user.Role),
		Providers:      names,
		HasPassword:    user.HasPassword(),
		CreatedAt:      user.CreatedAt,
	}, nil
}

// UpdateAccountProfile updates the user's display name.
func (u *AuthUseCase) UpdateAccountProfile(ctx context.Context, userID uuid.UUID, firstName, lastName string) error {
	return u.mutateUser(ctx, userID, func(user *userModel.User) error {
		user.UpdateProfile(firstName, lastName, time.Now())
		return nil
	})
}

// errReturnedAfterWarning stops the removal of an account whose owner signed
// in after the inactivity warning; it never leaves this package.
var errReturnedAfterWarning = errors.New("account active again after the inactivity warning")

// DeleteInactiveAccount removes an account the retention job warned at
// warnedAt through the same cascade as a deletion request. It reports false
// and changes nothing when the owner has signed in since the warning.
func (u *AuthUseCase) DeleteInactiveAccount(ctx context.Context, userID uuid.UUID, warnedAt time.Time) (bool, error) {
	err := u.deleteUserCascade(ctx, userID, func(user *userModel.User) error {
		if user.SeenAfter(warnedAt) {
			return errReturnedAfterWarning
		}
		return nil
	})
	if errors.Is(err, errReturnedAfterWarning) {
		return false, nil
	}
	return err == nil, err
}

// DeleteAccount soft-deletes the user (kept for stats), then cuts their sessions
// and provider links so the freed email + identities can be reused.
//
// It is the end of the account, so the person proves they are the owner now, not only that
// somebody holds a session: the account password, or for an account without one (Google only)
// a sign-in from the last minutes.
func (u *AuthUseCase) DeleteAccount(ctx context.Context, userID uuid.UUID, currentPassword string) error {
	if err := u.reauthenticate(ctx, userID, currentPassword); err != nil {
		return err
	}
	return u.deleteUserCascade(ctx, userID, nil)
}

// recentSignInWindow is how recent the sign-in must be for an account without a password to
// confirm a sensitive action.
const recentSignInWindow = 15 * time.Minute

// reauthenticate re-confirms the owner before an action that cannot be undone or that gives the
// account away. A stolen session alone must not be enough.
func (u *AuthUseCase) reauthenticate(ctx context.Context, userID uuid.UUID, currentPassword string) error {
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	if user.HasPassword() {
		return u.checkCurrentPassword(userID, currentPassword, user.HashedPassword)
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok || claims.SessionID == uuid.Nil {
		return authModel.ErrAuthReauthRequired.Err()
	}
	session, err := u.sessions.GetByID(ctx, claims.SessionID)
	if err != nil {
		return authModel.ErrAuthReauthRequired.WithError(err).Err()
	}
	if time.Since(session.CreatedAt) > recentSignInWindow {
		return authModel.ErrAuthReauthRequired.Err()
	}
	return nil
}
