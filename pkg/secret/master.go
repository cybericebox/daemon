package secret

import (
	"crypto/hkdf"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
)

// Purpose names what a derived key seals. Every purpose has its own key, derived from the master key, so the ciphertext of
// one purpose cannot be opened with the key of another and each can later move to its own service with its own key.
type Purpose string

const (
	PurposePlatform Purpose = "platform" // SMTP passwords, agent keys, registry tokens, queued payloads, archive passwords
	PurposeVPN      Purpose = "vpn"      // stored VPN client configs
	PurposeExercise Purpose = "exercise" // secret env vars of exercises
	PurposeSession  Purpose = "session"  // the session cookie, and the seed of the event-render signing keys
)

// derivedPrefix starts the id of a derived key: "d-" and the id of the master version it was derived from. The id is what a
// ciphertext carries, so a stored value says both that its key was derived and from which master version.
const derivedPrefix = "d-"

// maxMasterIDLen keeps "d-"+id inside the 16 characters of a key id.
const maxMasterIDLen = 16 - len(derivedPrefix)

// kdfLabelPrefix and the purpose make the fixed, versioned HKDF "info" of a derivation:
//
//	cybericebox/secrets/v1/platform
//	cybericebox/secrets/v1/vpn
//	cybericebox/secrets/v1/exercise
//	cybericebox/secrets/v1/session
//
// The label is part of the stored data's meaning: changing it makes every derived value unreadable. A new scheme gets "v2"
// and a new key id prefix, never an edit of this one.
const kdfLabelPrefix = "cybericebox/secrets/v1/"

// KDFLabel is the HKDF info of a purpose.
func KDFLabel(p Purpose) string { return kdfLabelPrefix + string(p) }

// IsDerivedID reports whether a key id names a derived key, and the id of the master version it was derived from.
func IsDerivedID(id string) (masterID string, derived bool) {
	masterID, derived = strings.CutPrefix(id, derivedPrefix)
	return masterID, derived && masterID != ""
}

// Resolve builds the keyring of one purpose, in the form New accepts, from the master key and the key the purpose may
// also have of its own.
//
//   - masterSpec is the master key: one 64-hex-char key, or a keyring "id:hex,id:hex" of master versions, the first being
//     current. Empty means no master key: explicit is returned unchanged (the older configuration, one key per purpose).
//   - The purpose key of each master version is HKDF-SHA256(master, salt none, info KDFLabel(purpose)), 32 bytes, and has
//     the id "d-<master id>". The key of the current master version comes first and seals.
//   - explicit is a key (or keyring) the purpose had before the master key existed, or one that is meant to replace the
//     derived key. By default it only opens (old values keep working and re-encryption moves them to the derived key). With
//     sealExplicit its first key seals instead and the derived keys only open.
func Resolve(masterSpec, explicit string, purpose Purpose, sealExplicit bool) (string, error) {
	if strings.TrimSpace(masterSpec) == "" {
		if sealExplicit {
			return "", fmt.Errorf("secret: an explicit key can only be set to seal when there is a master key")
		}
		return explicit, nil
	}
	master, err := New(masterSpec)
	if err != nil {
		return "", fmt.Errorf("secret: master key: %w", err)
	}
	defer master.Destroy()
	derived := make([]string, len(master.keys))
	for i, k := range master.keys {
		if len(k.id) > maxMasterIDLen {
			return "", fmt.Errorf("secret: master key id %q is longer than %d characters", k.id, maxMasterIDLen)
		}
		dk, err := hkdf.Key(sha256.New, k.key[:], nil, KDFLabel(purpose), 32)
		if err != nil {
			return "", err
		}
		derived[i] = derivedPrefix + k.id + ":" + hex.EncodeToString(dk)
	}
	if strings.TrimSpace(explicit) == "" {
		if sealExplicit {
			return "", fmt.Errorf("secret: %s is set to seal with its explicit key but has none", purpose)
		}
		return strings.Join(derived, ","), nil
	}
	own, err := New(explicit)
	if err != nil {
		return "", fmt.Errorf("secret: %s key: %w", purpose, err)
	}
	defer own.Destroy()
	explicitEntries := make([]string, len(own.keys))
	for i, k := range own.keys {
		if _, isDerived := IsDerivedID(k.id); isDerived {
			return "", fmt.Errorf("secret: the id %q of an explicit %s key is reserved for derived keys", k.id, purpose)
		}
		explicitEntries[i] = k.id + ":" + hex.EncodeToString(k.key[:])
	}
	entries := append(derived, explicitEntries...)
	if sealExplicit {
		entries = append(explicitEntries, derived...)
	}
	spec := strings.Join(entries, ",")
	if _, err := New(spec); err != nil { // duplicate ids
		return "", fmt.Errorf("secret: %s keys: %w", purpose, err)
	}
	return spec, nil
}
