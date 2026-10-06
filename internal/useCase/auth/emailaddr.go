package auth

import (
	"net/mail"

	authModel "github.com/cybericebox/daemon/internal/model/auth"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// maxEmailLength is the RFC 5321 limit for a mailbox address.
const maxEmailLength = 254

// normalizeEmail is the one canonical spelling of an address: users.email is a
// case-sensitive unique column, so "Alice@x" and "alice@x" must never be two
// accounts (and an invitation must find the account that registered).
func normalizeEmail(raw string) string { return userModel.NormalizeEmail(raw) }

// parseEmail normalizes raw and rejects anything that is not a bare mailbox
// address (display names, groups, comments, control bytes, over-long input).
func parseEmail(raw string) (string, error) {
	email := normalizeEmail(raw)
	if email == "" || len(email) > maxEmailLength {
		return "", authModel.ErrAuthInvalidEmail.Err()
	}
	address, err := mail.ParseAddress(email)
	if err != nil || address.Address != email {
		return "", authModel.ErrAuthInvalidEmail.Err()
	}
	return email, nil
}

// isDesignatedSuperAdmin reports whether email is the configured
// SUPER_ADMIN_EMAIL, compared on the normalized spelling.
func (u *AuthUseCase) isDesignatedSuperAdmin(email string) bool {
	designated := normalizeEmail(u.cfg.SuperAdminEmail)
	return designated != "" && normalizeEmail(email) == designated
}
