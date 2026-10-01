package teamChallengeModel

import (
	"crypto/sha256"
	"encoding/binary"

	"github.com/gofrs/uuid"
)

// SelectVariant deterministically maps a team and an event-exercise revision
// to one variant. Persisting the chosen index in TeamChallenge makes the
// decision durable even if the selection policy later evolves.
func SelectVariant(teamID, eventExerciseID uuid.UUID, variantCount int) (int32, error) {
	if teamID == uuid.Nil || eventExerciseID == uuid.Nil || variantCount < 1 {
		return 0, ErrTeamChallengeInvalid.Err()
	}
	sum := sha256.Sum256(append(teamID.Bytes(), eventExerciseID.Bytes()...))
	return int32(binary.BigEndian.Uint32(sum[:4]) % uint32(variantCount)), nil
}
