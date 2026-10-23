package exercise

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"slices"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The editor compares variants by hash, never by value: an env var by ValueHash, an attachment by ContentHash.

// envValueHashPurpose derives the per-exercise HMAC key from the exercise secrets key. A hash is never a plain sha256
// of the value: a short secret (a flag) would be brute-forced from it. The key is per exercise, so the same value
// hashes differently in two tasks, and it changes when the secrets key is rotated.
const envValueHashPurpose = "exercise-env-value-hash/v1:"

// envValueHashes gives the hash of every env var value, aligned with the variants' devices and env vars
// ([variant][device][env]): HMAC-SHA256 hex under the exercise's key, of the opened value for a secret. "" when there
// is no key (EXERCISE_SECRETS_KEY unset), for a secret without a stored value, and for one that does not open.
func (u *ExerciseUseCase) envValueHashes(exerciseID uuid.UUID, variants []exerciseModel.Variant) [][][]string {
	if u.cipher == nil {
		return nil
	}
	key := u.cipher.DeriveKeys(envValueHashPurpose + exerciseID.String())[0]
	hash := func(value []byte) string {
		mac := hmac.New(sha256.New, key[:])
		mac.Write(value)
		return hex.EncodeToString(mac.Sum(nil))
	}
	out := make([][][]string, len(variants))
	for vi, variant := range variants {
		out[vi] = make([][]string, len(variant.Topology.Devices))
		for di, device := range variant.Topology.Devices {
			out[vi][di] = make([]string, len(device.EnvVars))
			for ei, env := range device.EnvVars {
				if !env.Secret {
					out[vi][di][ei] = hash([]byte(env.Value))
					continue
				}
				if env.Value == "" {
					continue
				}
				plain, err := u.cipher.DecryptWithContext(env.Value, envSecretContext(variant.ID, device.Name, env.Name))
				if err != nil {
					log.Warn().Err(err).Str("variant", variant.ID.String()).Msg("a secret env var does not open; its hash is left out")
					continue
				}
				out[vi][di][ei] = hash(plain)
			}
		}
	}
	return out
}

// attachmentHashes is the sha256 hex of every attached file's bytes, by file id (stored with the file on upload and
// import). Best effort: a failed lookup leaves the hashes out rather than failing the read.
func (u *ExerciseUseCase) attachmentHashes(ctx context.Context, variants []exerciseModel.Variant) map[uuid.UUID]string {
	ids := exerciseModel.CollectFileIDs(variants)
	slices.SortFunc(ids, func(a, b uuid.UUID) int { return slices.Compare(a[:], b[:]) })
	ids = slices.Compact(ids)
	if len(ids) == 0 {
		return nil
	}
	hashes, err := u.exercises.FileHashes(ctx, ids)
	if err != nil {
		log.Warn().Err(err).Msg("exercise attachment hashes unavailable")
		return nil
	}
	return hashes
}
