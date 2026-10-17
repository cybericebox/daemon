package exercise

import (
	"fmt"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// secretKey scopes a secret env-var to its variant: device and env-var names
// are only unique WITHIN a variant (multiple variants of the same exercise
// commonly reuse names like "web"/"DB_PASS"), so the variant's identity MUST
// be part of the key. Without it, variant A's submitted plaintext and variant
// B's blank "keep" for the same names collide in the kept/submitted maps —
// B silently inherits A's stored ciphertext and then gets it re-encrypted
// (double encryption, silent corruption) because the flat key was also
// present in the submitted set. Keyed by variant ID (not slice position) so
// reordering variants between the merge source and the incoming save cannot
// swap which secret follows which variant — callers must normalize variant
// ids (normalizeContentIDs) before computing any of these keys.
func secretKey(variantID uuid.UUID, device, env string) string {
	return fmt.Sprintf("%s\x00%s\x00%s", variantID, device, env)
}

// positionalSecretKey is the TRANSITIONAL key for merge-source variants that
// predate Variant.ID (stored with ID == uuid.Nil). The "pos:" prefix keeps it
// in a key space distinct from secretKey's UUID-string form, so both kinds
// can share one map without collisions.
func positionalSecretKey(vi int, device, env string) string {
	return fmt.Sprintf("pos:%d\x00%s\x00%s", vi, device, env)
}

// mergeKeptSecrets fills empty incoming secret values with the ciphertext
// already stored in the current draft, matched by (variant id, device name,
// var name) — "blank secret field" in the admin UI means "keep the existing
// value" for THAT variant only. A variant whose id is not present in current
// (a brand-new variant) simply has nothing to merge — its blank secrets stay
// blank, which is correct.
//
// TRANSITIONAL fallback (remove once no stored version lacks variant ids):
// versions persisted BEFORE Variant.ID existed unmarshal with ID == uuid.Nil,
// while incoming id-less variants receive FRESH UUIDv7s from
// normalizeContentIDs before this merge runs — pure ID keying can therefore
// never match a legacy source, and every blank-keep would silently store "".
// Legacy (nil-ID) source variants are keyed by slice position instead, and an
// incoming variant whose ID lookup misses falls back to its own position.
// Post-migration data (all source variants carry ids) never populates the
// positional keys, so the fallback is inert there and reorder safety holds;
// against a legacy source, positional matching IS the pre-change behavior.
func mergeKeptSecrets(incoming, current []exerciseModel.Variant) {
	kept := map[string]string{}
	for vi, v := range current {
		for _, d := range v.Topology.Devices {
			for _, ev := range d.EnvVars {
				if !ev.Secret || ev.Value == "" {
					continue
				}
				if v.ID == uuid.Nil {
					kept[positionalSecretKey(vi, d.Name, ev.Name)] = ev.Value
				} else {
					kept[secretKey(v.ID, d.Name, ev.Name)] = ev.Value
				}
			}
		}
	}
	for vi := range incoming {
		for di := range incoming[vi].Topology.Devices {
			d := &incoming[vi].Topology.Devices[di]
			for ei := range d.EnvVars {
				ev := &d.EnvVars[ei]
				if !ev.Secret || ev.Value != "" {
					continue
				}
				if val, ok := kept[secretKey(incoming[vi].ID, d.Name, ev.Name)]; ok {
					ev.Value = val
				} else if val, ok = kept[positionalSecretKey(vi, d.Name, ev.Name)]; ok {
					ev.Value = val
				}
			}
		}
	}
}

// submittedSecretKeys records which secret values arrived non-empty in the
// request BEFORE the merge, so encryptSecrets can tell fresh plaintext from
// merged ciphertext (mergeKeptSecrets only fills what was blank, so anything
// not in this set but now non-empty came from the stored draft, not the
// caller, and must not be re-encrypted). Keyed by (variant id, device, env) —
// see secretKey.
func submittedSecretKeys(variants []exerciseModel.Variant) map[string]bool {
	out := map[string]bool{}
	for _, v := range variants {
		for _, d := range v.Topology.Devices {
			for _, ev := range d.EnvVars {
				if ev.Secret && ev.Value != "" {
					out[secretKey(v.ID, d.Name, ev.Name)] = true
				}
			}
		}
	}
	return out
}

// secretSources maps a variant's index to the variant whose secrets it takes: the base for a linked variant,
// else the variant it was copied from (copyOf, aligned with variants by index) when that is another variant.
func secretSources(variants []exerciseModel.Variant, copyOf []uuid.NullUUID) map[int]uuid.UUID {
	sources := make(map[int]uuid.UUID)
	for i, variant := range variants {
		if variant.IsLinked() {
			sources[i] = variant.LinkedTo.UUID
		} else if i < len(copyOf) && copyOf[i].Valid && copyOf[i].UUID != variant.ID {
			sources[i] = copyOf[i].UUID
		}
	}
	return sources
}

// inheritSecrets runs after mergeKeptSecrets and before encryptSecrets. A secret is sealed to its variant id,
// so a target cannot reuse its source's ciphertext: a target secret still blank takes the source's value of
// the same (device, env) as plaintext and is marked submitted, so encryptSecrets seals it under the target's
// id. A linked variant's topology is the base's, so its secret always equals the base's: a stored value of
// its own is kept only while it opens to the base's value (no re-encryption churn on every save), else
// replaced, and it is cleared when the base has none. Sources resolve depth-first, before the variants that
// use them (a variant linked to a new copy, a copy of a copy in any order); a copy cycle stops at the
// variant already being resolved. A source that is not in variants is skipped.
func (u *ExerciseUseCase) inheritSecrets(variants []exerciseModel.Variant, sources map[int]uuid.UUID, submitted map[string]bool) error {
	positionByID := make(map[uuid.UUID]int, len(variants))
	for i := len(variants) - 1; i >= 0; i-- {
		positionByID[variants[i].ID] = i
	}
	resolved := make(map[int]bool, len(variants))
	visiting := make(map[int]bool)
	var resolve func(target int) error
	resolve = func(target int) error {
		if resolved[target] || visiting[target] {
			return nil
		}
		visiting[target] = true
		defer delete(visiting, target)
		if sourceID, ok := sources[target]; ok {
			if source, found := positionByID[sourceID]; found && source != target {
				if err := resolve(source); err != nil {
					return err
				}
				if err := u.inheritVariantSecrets(&variants[target], variants[source], variants[target].IsLinked(), submitted); err != nil {
					return err
				}
			}
		}
		resolved[target] = true
		return nil
	}
	for target := range variants {
		if err := resolve(target); err != nil {
			return err
		}
	}
	return nil
}

func (u *ExerciseUseCase) inheritVariantSecrets(target *exerciseModel.Variant, source exerciseModel.Variant, linked bool, submitted map[string]bool) error {
	for di := range target.Topology.Devices {
		device := &target.Topology.Devices[di]
		for ei := range device.EnvVars {
			env := &device.EnvVars[ei]
			if !env.Secret {
				continue
			}
			targetKey := secretKey(target.ID, device.Name, env.Name)
			if env.Value != "" && (!linked || submitted[targetKey]) {
				continue
			}
			sourceValue := sourceSecret(source, device.Name, env.Name)
			if sourceValue == "" {
				if linked {
					env.Value = ""
				}
				continue
			}
			plain, err := u.secretPlaintext(source.ID, device.Name, env.Name, sourceValue, submitted)
			if err != nil {
				return err
			}
			if env.Value != "" && u.cipher != nil {
				own, openErr := u.cipher.DecryptWithContext(env.Value, envSecretContext(target.ID, device.Name, env.Name))
				if openErr == nil && string(own) == plain {
					continue
				}
				if openErr != nil {
					log.Debug().Err(openErr).Str("variant", target.ID.String()).Str("device", device.Name).Str("env", env.Name).
						Msg("Linked variant secret does not open; replaced with the base value")
				}
			}
			env.Value = plain
			submitted[targetKey] = true
		}
	}
	return nil
}

// sourceSecret is the source variant's secret value of (device, env), "" when it has none.
func sourceSecret(source exerciseModel.Variant, deviceName, envName string) string {
	for _, device := range source.Topology.Devices {
		if device.Name != deviceName {
			continue
		}
		for _, env := range device.EnvVars {
			if env.Secret && env.Name == envName {
				return env.Value
			}
		}
	}
	return ""
}

// secretPlaintext opens a variant's secret value after the merge: a value submitted in this save is
// plaintext, any other is the stored ciphertext sealed to that variant.
func (u *ExerciseUseCase) secretPlaintext(variantID uuid.UUID, deviceName, envName, value string, submitted map[string]bool) (string, error) {
	if submitted[secretKey(variantID, deviceName, envName)] {
		return value, nil
	}
	if u.cipher == nil {
		return "", exerciseModel.ErrSecretsNotConfigured.Err()
	}
	plain, err := u.cipher.DecryptWithContext(value, envSecretContext(variantID, deviceName, envName))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to decrypt the secret env var of the source variant").Err()
	}
	return string(plain), nil
}

// envSecretContext binds the ciphertext of a secret env var to its variant, device and name (the same identity
// the draft merge keeps a secret by): a value copied to another variable does not open there.
func envSecretContext(variantID uuid.UUID, device, name string) []byte {
	return []byte("exercise-env:" + variantID.String() + ":" + device + ":" + name)
}

// encryptSecrets encrypts every secret env-var value that the caller actually
// submitted as non-empty plaintext (tracked via rawSubmitted). Values merged
// in from the stored draft are already ciphertext and are skipped.
func (u *ExerciseUseCase) encryptSecrets(variants []exerciseModel.Variant, rawSubmitted map[string]bool) error {
	for vi := range variants {
		for di := range variants[vi].Topology.Devices {
			d := &variants[vi].Topology.Devices[di]
			for ei := range d.EnvVars {
				ev := &d.EnvVars[ei]
				if !ev.Secret || ev.Value == "" || !rawSubmitted[secretKey(variants[vi].ID, d.Name, ev.Name)] {
					continue
				}
				if u.cipher == nil {
					return exerciseModel.ErrSecretsNotConfigured.Err()
				}
				ct, err := u.cipher.EncryptWithContext([]byte(ev.Value), envSecretContext(variants[vi].ID, d.Name, ev.Name))
				if err != nil {
					return model.ErrPlatform.WithError(err).WithMessage("Failed to encrypt secret env var").Err()
				}
				ev.Value = ct
			}
		}
	}
	return nil
}

// decryptExportedSecrets converts this installation's at-rest ciphertext into
// plaintext only in the in-memory export copy. The caller immediately places
// that copy into a password-encrypted archive; the database snapshot is never
// modified. A protected export is consequently portable across installations
// with different EXERCISE_SECRETS_KEY values.
func (u *ExerciseUseCase) decryptExportedSecrets(versions []exerciseModel.ExerciseVersion) error {
	for vi := range versions {
		for variantIndex := range versions[vi].Variants {
			for deviceIndex := range versions[vi].Variants[variantIndex].Topology.Devices {
				device := &versions[vi].Variants[variantIndex].Topology.Devices[deviceIndex]
				for envIndex := range device.EnvVars {
					env := &device.EnvVars[envIndex]
					if !env.Secret || env.Value == "" {
						continue
					}
					if u.cipher == nil {
						return exerciseModel.ErrSecretsNotConfigured.Err()
					}
					plain, err := u.cipher.DecryptWithContext(env.Value, envSecretContext(versions[vi].Variants[variantIndex].ID, device.Name, env.Name))
					if err != nil {
						return model.ErrPlatform.WithError(err).WithMessage("Failed to decrypt secret env var for export").Err()
					}
					env.Value = string(plain)
				}
			}
		}
	}
	return nil
}

// encryptImportedSecrets is the inverse of decryptExportedSecrets. It runs
// before imported snapshots are persisted, so plaintext exists only in the
// password-protected archive and the short-lived import copy.
func (u *ExerciseUseCase) encryptImportedSecrets(versions []exerciseModel.ExerciseVersion) error {
	for vi := range versions {
		for variantIndex := range versions[vi].Variants {
			for deviceIndex := range versions[vi].Variants[variantIndex].Topology.Devices {
				device := &versions[vi].Variants[variantIndex].Topology.Devices[deviceIndex]
				for envIndex := range device.EnvVars {
					env := &device.EnvVars[envIndex]
					if !env.Secret || env.Value == "" {
						continue
					}
					if u.cipher == nil {
						return exerciseModel.ErrSecretsNotConfigured.Err()
					}
					ciphertext, err := u.cipher.EncryptWithContext([]byte(env.Value), envSecretContext(versions[vi].Variants[variantIndex].ID, device.Name, env.Name))
					if err != nil {
						return model.ErrPlatform.WithError(err).WithMessage("Failed to encrypt imported secret env var").Err()
					}
					env.Value = ciphertext
				}
			}
		}
	}
	return nil
}

// maskSecrets blanks secret values on a DEEP COPY for read models — callers'
// stored variants (and the cached draft used by mergeKeptSecrets) must never
// be mutated by a read path.
func maskSecrets(variants []exerciseModel.Variant) []exerciseModel.Variant {
	out := make([]exerciseModel.Variant, len(variants))
	copy(out, variants)
	for vi := range out {
		devices := make([]exerciseModel.Device, len(out[vi].Topology.Devices))
		copy(devices, out[vi].Topology.Devices)
		for di := range devices {
			envs := make([]exerciseModel.EnvVar, len(devices[di].EnvVars))
			copy(envs, devices[di].EnvVars)
			for ei := range envs {
				if envs[ei].Secret {
					envs[ei].Value = ""
				}
			}
			devices[di].EnvVars = envs
		}
		out[vi].Topology.Devices = devices
	}
	return out
}
