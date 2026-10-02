package userModel

import "strings"

// NormalizeEmail is the one canonical spelling of an address, used on EVERY input (sign-up, invite,
// Google, email change, event invitations, lookups) and by the user repository on every read and
// write: trimmed and lower-cased. users.email is a plain unique column, so two addresses that
// differ only in case are one account.
func NormalizeEmail(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}
