package teamChallengeModel

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"math/big"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

// flagSlotLabel separates the flag-slot keys from every other use of the
// server secret.
const flagSlotLabel = "cib-flag-slot"

// GenerateFlag produces a non-predictable per-team value for catalog tasks
// that deliberately have no fixed candidate flag.
func GenerateFlag() (string, error) {
	return flagpattern.Resolve(nil, 20, rand.Reader)
}

// ResolveExpectedFlag applies the catalog flag policy with a random slot:
// absent candidates generate a secret, one candidate stays fixed, and multiple
// candidates are selected with cryptographically secure randomness.
func ResolveExpectedFlag(candidates []string) (string, error) {
	return flagpattern.Resolve(candidates, 20, rand.Reader)
}

// FlagSlotKey is the permutation key of one task variant of an event exercise:
// HMAC-SHA256(secret, "cib-flag-slot" || event exercise id (16 bytes) ||
// subtask id (16 bytes) || variant index (4 bytes, big-endian)). It is derived
// on demand and never stored.
func FlagSlotKey(secret []byte, eventExerciseID, taskID uuid.UUID, variantIndex int32) []byte {
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(flagSlotLabel))
	mac.Write(eventExerciseID.Bytes())
	mac.Write(taskID.Bytes())
	mac.Write(binary.BigEndian.AppendUint32(nil, uint32(variantIndex)))
	return mac.Sum(nil)
}

// ResolveTeamFlag is the expected flag of an event team: with candidates the
// value of the team's slot under the task key (slots 0..N-1 are distinct);
// without candidates the full random mode, unchanged.
func ResolveTeamFlag(candidates []string, randomBytes int, slot uint64, key []byte) (string, error) {
	if len(candidates) == 0 {
		return flagpattern.Resolve(nil, randomBytes, rand.Reader)
	}
	return flagpattern.ResolveAt(candidates, new(big.Int).SetUint64(slot), key)
}
