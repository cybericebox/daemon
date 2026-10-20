package challengeAttempt

import (
	"crypto/sha256"
	"crypto/subtle"
	"strings"
)

// FlagMatches is the single flag check for every path (event, catalog test lab, participant preview): surrounding
// whitespace of the submitted answer is trimmed, then the compare is exact, case-sensitive and constant-time.
// Both sides are hashed first, so a length difference does not short-circuit the compare. An empty answer or an
// empty expected flag never matches.
func FlagMatches(expected, submitted string) bool {
	submitted = strings.TrimSpace(submitted)
	if expected == "" || submitted == "" {
		return false
	}
	e, s := sha256.Sum256([]byte(expected)), sha256.Sum256([]byte(submitted))
	return subtle.ConstantTimeCompare(e[:], s[:]) == 1
}
