package auth

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	"github.com/cybericebox/daemon/internal/model/rbac"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// ForgotPassword issues a single-use reset code and emails a reset link. It is
// silent when the address is unknown (no account-existence leak). An
// incomplete account gets the continue-registration email instead.
func (u *AuthUseCase) ForgotPassword(ctx context.Context, emailAddr string) error {
	user, err := u.users.GetByEmail(ctx, normalizeEmail(emailAddr))
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user by email").Err()
	}
	// Everything after the lookup runs in the background: an unknown address returns at once, so a
	// known one must not take visibly longer (nor fail visibly when the mail queue does).
	u.runInBackground("forgot-password", func(ctx context.Context) error { return u.sendPasswordReset(ctx, user) })
	return nil
}

// sendPasswordReset mails the reset link of an existing account (or, for one that never finished
// registering, the continue-registration link: there is no password to reset).
func (u *AuthUseCase) sendPasswordReset(ctx context.Context, user userModel.User) error {
	if user.IsIncomplete() {
		return u.sendContinueRegistration(ctx, user.ID, user.Email, user.FirstName, "")
	}
	// Mail-bombing guard: over the quota nothing is sent.
	if !u.mailAllowed(mailKindReset, user.Email) {
		return nil
	}

	code, err := u.createTemporalCode(ctx, temporalCodeModel.PasswordResettingCodeType,
		temporalCodeModel.TemporalPasswordResettingCodeData{UserID: user.ID})
	if err != nil {
		return err
	}
	bsCode := bsEncode(code)

	if err = u.notifier.Notify(ctx, user.ID, notificationPayloads.PasswordResetPayload{
		ResetURL: u.cfg.Hosts.IDURL(fmt.Sprintf("/reset-password?token=%s", bsCode)),
		Name:     user.FirstName,
	}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to send password reset email").Err()
	}
	return nil
}

// ResetPassword consumes a reset code and sets a new password.
func (u *AuthUseCase) ResetPassword(ctx context.Context, bsCode, newPassword string) error {
	code, decErr := base64.StdEncoding.DecodeString(bsDecode(bsCode))
	if decErr != nil {
		return temporalCodeModel.ErrTemporalCodeInvalidCode.WithError(fmt.Errorf("password-reset: base64 decode: %w", decErr)).Err()
	}

	raw, err := u.consumeTemporalCode(ctx, string(code), temporalCodeModel.PasswordResettingCodeType)
	if err != nil {
		return err
	}
	var data temporalCodeModel.TemporalPasswordResettingCodeData
	if err = json.Unmarshal(raw, &data); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to unmarshal temporal code data").Err()
	}
	if err = u.applyNewPassword(ctx, data.UserID, newPassword); err != nil {
		return err
	}
	// The other reset links still sitting in the mailbox are dead too: one link, one reset.
	if _, err = u.codes.DeleteForUser(ctx, temporalCodeModel.PasswordResettingCodeType, data.UserID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to revoke the other reset codes").Err()
	}
	// Whoever held a session when the password was lost (a stolen cookie is the
	// usual reason to reset) must not keep it.
	return u.revokeSessions(ctx, data.UserID, uuid.Nil)
}

// SetAccountPassword sets the authenticated user's password. With an existing
// password it verifies oldPassword (change); with none it sets the first
// password and ignores oldPassword.
func (u *AuthUseCase) SetAccountPassword(ctx context.Context, userID uuid.UUID, oldPassword, newPassword string) error {
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	if user.HasPassword() {
		if err = u.checkCurrentPassword(userID, oldPassword, user.HashedPassword); err != nil {
			return err
		}
	}
	if err = u.applyNewPassword(ctx, userID, newPassword); err != nil {
		return err
	}
	// Every other device signs in again with the new password; this one stays.
	var keep uuid.UUID
	if claims, ok := rbac.CurrentUserSessionFromContext(ctx); ok && claims.UserID == userID {
		keep = claims.SessionID
	}
	return u.revokeSessions(ctx, userID, keep)
}

// checkCurrentPassword re-checks the password of a signed-in user for a
// sensitive action. Wrong guesses lock the user's checks (a stolen session
// must not be a free oracle for the password), a right one clears them.
func (u *AuthUseCase) checkCurrentPassword(userID uuid.UUID, plain, hashed string) error {
	key := userID.String()
	if wait := u.limits.passwordGuess.Locked(key); wait > 0 {
		return tooManyRequests(wait)
	}
	matches, err := u.password.Matches(plain, hashed)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check password").Err()
	}
	if !matches {
		u.limits.passwordGuess.Fail(key)
		return authModel.ErrAuthInvalidOldPassword.Err()
	}
	u.limits.passwordGuess.Reset(key)
	return nil
}

// revokeSessions ends the user's sessions after a credential change: all of
// them, or all but keep when it is set.
func (u *AuthUseCase) revokeSessions(ctx context.Context, userID, keep uuid.UUID) error {
	var err error
	if keep == uuid.Nil {
		_, err = u.revokeAll(ctx, userID)
	} else {
		_, err = u.revokeAllExcept(ctx, userID, keep)
	}
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to revoke sessions").Err()
	}
	return nil
}

// applyNewPassword validates complexity, hashes and persists newPassword.
func (u *AuthUseCase) applyNewPassword(ctx context.Context, userID uuid.UUID, newPassword string) error {
	if err := u.password.CheckPasswordComplexity(newPassword); err != nil {
		return authModel.ErrAuthInvalidPasswordComplexity.WithError(err).Err()
	}
	hashedPassword, err := u.password.Hash(newPassword)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to hash password").Err()
	}
	return u.mutateUser(ctx, userID, func(user *userModel.User) error {
		user.SetPassword(hashedPassword, time.Now())
		return nil
	})
}

// bsEncode base64-encodes a raw code and strips '=' padding for URL embedding.
func bsEncode(code string) string {
	enc := base64.StdEncoding.EncodeToString([]byte(code))
	for len(enc) > 0 && enc[len(enc)-1] == '=' {
		enc = enc[:len(enc)-1]
	}
	return enc
}

// bsDecode restores '=' padding stripped by bsEncode so StdEncoding can decode.
func bsDecode(bs string) string {
	if m := len(bs) % 4; m != 0 {
		bs += "===="[m:]
	}
	return bs
}
