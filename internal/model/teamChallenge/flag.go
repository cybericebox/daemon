package teamChallengeModel

import (
	"crypto/rand"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

// GenerateFlag produces a non-predictable per-team value for catalog tasks
// that deliberately have no fixed candidate flag.
func GenerateFlag() (string, error) {
	return flagpattern.Resolve(nil, 20, rand.Reader)
}

// ResolveExpectedFlag applies the catalog flag policy at materialization time:
// absent candidates generate a secret, one candidate stays fixed, and multiple
// candidates are selected with cryptographically secure randomness.
func ResolveExpectedFlag(candidates []string) (string, error) {
	return flagpattern.Resolve(candidates, 20, rand.Reader)
}
