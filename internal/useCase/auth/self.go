package auth

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// GetSelfProfile returns the signed-in user's lightweight profile.
func (u *AuthUseCase) GetSelfProfile(ctx context.Context, userID uuid.UUID) (*UserInfo, error) {
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	return &UserInfo{
		ID:        user.ID,
		FirstName: user.FirstName,
		LastName:  user.LastName,
		Picture:   user.Picture,
		Email:     user.Email,
		Role:      rbac.Role(user.Role),
		LastSeen:  user.LastSeen,
		CreatedAt: user.CreatedAt,
	}, nil
}
