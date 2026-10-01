package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// RequestEmailChange issues a one-time code bound to the user + new address and
// emails a confirmation link to the NEW address (via the notifier override).
func (u *AuthUseCase) RequestEmailChange(ctx context.Context, userID uuid.UUID, newEmail string) error {
	// Reject if a (non-deleted) account already uses the new address.
	if _, err := u.users.GetByEmail(ctx, newEmail); err == nil {
		return userModel.ErrUserExists.WithError(errors.New("email-change-request: new address already in use")).Err()
	} else if !repositoryTools.IsObjectNotFoundError(err) {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check email availability").Err()
	}

	current, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}

	code, err := u.createTemporalCode(ctx, temporalCodeModel.EmailChangeCodeType,
		temporalCodeModel.TemporalEmailChangeCodeData{UserID: userID, Email: newEmail})
	if err != nil {
		return err
	}
	bsCode := bsEncode(code)

	// Override the recipient with a copy of the user carrying the NEW email,
	// keeping the real id so in-app (if it fired) still resolves.
	override := userModel.User{ID: userID, Email: newEmail, FirstName: current.FirstName}

	if err = u.notifier.Notify(ctx, userID, notificationPayloads.EmailConfirmationPayload{
		ConfirmURL: u.cfg.Hosts.IDURL(fmt.Sprintf("/confirm-email?token=%s", bsCode)),
		Name:       current.FirstName,
	}, dispatchModel.WithRecipient(override)); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to send email change confirmation").Err()
	}
	return nil
}

// ConfirmEmailChange consumes an email-change code and switches the account email.
func (u *AuthUseCase) ConfirmEmailChange(ctx context.Context, bsCode string) error {
	code, decErr := base64.StdEncoding.DecodeString(bsDecode(bsCode))
	if decErr != nil {
		return temporalCodeModel.ErrTemporalCodeInvalidCode.WithError(fmt.Errorf("email-change: base64 decode: %w", decErr)).Err()
	}
	raw, err := u.consumeTemporalCode(ctx, string(code), temporalCodeModel.EmailChangeCodeType)
	if err != nil {
		return err
	}
	var data temporalCodeModel.TemporalEmailChangeCodeData
	if err = json.Unmarshal(raw, &data); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to unmarshal temporal code data").Err()
	}

	// Re-check uniqueness at confirm time.
	claimant, cErr := u.users.GetByEmail(ctx, data.Email)
	if cErr == nil && claimant.ID != data.UserID {
		return userModel.ErrUserExists.WithError(errors.New("email-change-confirm: address claimed by another account meanwhile")).Err()
	} else if cErr != nil && !repositoryTools.IsObjectNotFoundError(cErr) {
		return model.ErrPlatform.WithError(cErr).WithMessage("Failed to check email availability").Err()
	}

	// One aggregate write replaces the old email+confirmed statement pair.
	return u.mutateUser(ctx, data.UserID, func(user *userModel.User) error {
		user.ChangeEmail(data.Email, time.Now())
		user.ConfirmEmail(time.Now())
		return nil
	})
}
