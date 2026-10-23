package exercise

import (
	"context"
	"slices"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The editor compares variants without values: a secret by its ValueGroup, an attachment by its ContentHash (a plain
// env var by the value itself, which the editor reads anyway).
//
// Why no hash of a secret's value: an author who may save the draft but not read its secrets could store guesses
// (as plain values or new secrets) and compare their hashes with a stored secret's, many guesses per save. Who reads
// a secret's plaintext today:
//   - the draft and version reads never: they are masked (maskSecrets, secret.go:386);
//   - an export with secrets ships them decrypted in the password-protected zip (decryptExportedSecrets,
//     secret.go:319, from planExport, archive.go:578), but only for exercises.export or an owner/manager of the
//     task's event (AuthorizeExport, access.go:397); a catalog author with exercises.write alone may not export;
//   - a test lab gets them decrypted into the containers (decryptTopologySecrets, deploy.go:280 and :1306); the
//     author who deploys it controls the image, so may read them there where test labs are allowed to them.
// Not every reader of the draft can read the plaintext, so the groups are made from where a value came from,
// never from what it is (EnvVar.Origin): a typed value always starts a group of its own, so equality with a guess
// is never revealed.

// secretValueGroups gives every stored secret of the variants a group, aligned with the devices and env vars
// ([variant][device][env]; 0 for a plain var or a secret without a stored value). Secrets in one group hold the same
// value: one origin (kept, inherited by a linked variant, copied, or equal values of one import). A secret stored
// before origins existed is grouped with the other such secrets by its opened value (none of them can be a value
// typed since); one that does not open is a group of its own. Groups are numbered 1, 2, … in order of first
// appearance and mean nothing outside the one read.
func (u *ExerciseUseCase) secretValueGroups(variants []exerciseModel.Variant) [][][]int {
	keys := map[string]int{}
	group := func(key string) int {
		if id, ok := keys[key]; ok {
			return id
		}
		keys[key] = len(keys) + 1
		return keys[key]
	}
	out := make([][][]int, len(variants))
	for vi, variant := range variants {
		out[vi] = make([][]int, len(variant.Topology.Devices))
		for di, device := range variant.Topology.Devices {
			out[vi][di] = make([]int, len(device.EnvVars))
			for ei, env := range device.EnvVars {
				if !env.Secret || env.Value == "" {
					continue
				}
				key := "origin:" + env.Origin
				if env.Origin == "" {
					key = "unknown:" + freshID().String()
					if u.cipher != nil {
						if plain, err := u.cipher.DecryptWithContext(env.Value, envSecretContext(variant.ID, device.Name, env.Name)); err == nil {
							key = "legacy:" + string(plain)
						}
					}
				}
				out[vi][di][ei] = group(key)
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
