package auth

import (
	"context"
	"errors"
	"time"

	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
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
func (u *AuthUseCase) DeleteAccount(ctx context.Context, userID uuid.UUID) error {
	return u.deleteUserCascade(ctx, userID, nil)
}
