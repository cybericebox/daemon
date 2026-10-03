package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// RequestEmailChange issues a one-time code bound to the user + new address and
// emails a confirmation link to the NEW address (via the notifier override).
//
// The account password is required: an email change is the step before a
// password reset, so a stolen session alone must not be able to take it.
func (u *AuthUseCase) RequestEmailChange(ctx context.Context, userID uuid.UUID, rawEmail, currentPassword string) error {
	newEmail, err := parseEmail(rawEmail)
	if err != nil {
		return err
	}
	current, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	if !current.HasPassword() {
		return authModel.ErrAuthPasswordRequired.Err()
	}
	if err = u.checkCurrentPassword(userID, currentPassword, current.HashedPassword); err != nil {
		return err
	}
	// Mail-bombing guard: one account can only trigger so many confirmation
	// mails, and one address only so many of them.
	if ok, wait := u.limits.emailChangeUsers.Allow(userID.String()); !ok {
		return tooManyRequests(wait)
	}
	if !u.mailAllowed(mailKindEmailChange, newEmail) {
		return tooManyRequests(u.limits.mailGap)
	}

	// Reject if a (non-deleted) account already uses the new address.
	if _, err := u.users.GetByEmail(ctx, newEmail); err == nil {
		return userModel.ErrUserExists.WithError(errors.New("email-change-request: new address already in use")).Err()
	} else if !repositoryTools.IsObjectNotFoundError(err) {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check email availability").Err()
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
	var oldEmail, name string
	if err = u.mutateUser(ctx, data.UserID, func(user *userModel.User) error {
		oldEmail, name = user.Email, user.FirstName
		user.ChangeEmail(data.Email, time.Now())
		user.ConfirmEmail(time.Now())
		return nil
	}); err != nil {
		return err
	}
	// The change is done: pending recovery and email-change codes issued before it are dead.
	if err = u.revokeCodes(ctx, data.UserID, temporalCodeModel.PasswordResettingCodeType, temporalCodeModel.EmailChangeCodeType); err != nil {
		return err
	}
	// A Google identity vouched for the OLD address: it no longer proves anything about the account,
	// so the link goes (the account has a password: the request needed it).
	if _, err = u.users.DeleteProviders(ctx, data.UserID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to unlink the providers").Err()
	}
	// The address is the recovery channel of the account: every session ends,
	// the owner signs in again (this link works without a session).
	if err = u.revokeSessions(ctx, data.UserID, uuid.Nil); err != nil {
		return err
	}
	u.noticeOldEmail(ctx, data.UserID, oldEmail, name, data.Email)
	return nil
}

// noticeOldEmail tells the address the account just left what happened (the owner who did not make
// the change still gets a way back: a password reset). The change is done; a failed mail is logged.
func (u *AuthUseCase) noticeOldEmail(ctx context.Context, userID uuid.UUID, oldEmail, name, newEmail string) {
	if oldEmail == "" || oldEmail == newEmail {
		return
	}
	override := userModel.User{ID: userID, Email: oldEmail, FirstName: name}
	if err := u.notifier.Notify(ctx, userID, notificationPayloads.EmailChangedPayload{
		Name: name, NewEmail: newEmail, ResetURL: u.cfg.Hosts.IDURL("/forgot-password"),
	}, dispatchModel.WithRecipient(override)); err != nil {
		log.Error().Err(err).Str("user_id", userID.String()).Msg("Failed to notify the old address of an email change")
	}
}
