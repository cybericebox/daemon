package config

import (
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/pkg/secret"
)

// Secrets guard modes.
const (
	SecretsGuardWarn   = "warn"
	SecretsGuardRefuse = "refuse"
	SecretsGuardOff    = "off"
)

// Seal modes: which key seals the new values of a purpose when it has both a derived and an explicit key.
const (
	SealDerived  = "derived"
	SealExplicit = "explicit"
)

// Validate refuses an unknown SECRETS_GUARD mode.
func (k KeysConfig) Validate() error {
	switch k.SecretsGuard {
	case SecretsGuardWarn, SecretsGuardRefuse, SecretsGuardOff:
		return nil
	}
	return fmt.Errorf("SECRETS_GUARD must be %s, %s or %s", SecretsGuardWarn, SecretsGuardRefuse, SecretsGuardOff)
}

// keyPurpose is one key of the platform: where its explicit form is read from and which purpose key derives from the master.
type keyPurpose struct {
	name    string // the variable that holds the explicit key
	purpose secret.Purpose
	dir     string
	seal    string
	key     *string
}

func (c *Config) keyPurposes() []keyPurpose {
	return []keyPurpose{
		{"SESSION_ENCRYPTION_KEY", secret.PurposeSession, c.Auth.SessionEncryptionKeyDir, c.Auth.SessionEncryptionSeal, &c.Auth.SessionEncryptionKey},
		{"PLATFORM_SECRETS_KEY", secret.PurposePlatform, c.Platform.SecretsKeyDir, c.Platform.SecretsSeal, &c.Platform.SecretsKey},
		{"VPN_SECRETS_KEY", secret.PurposeVPN, c.VPN.SecretsKeyDir, c.VPN.SecretsSeal, &c.VPN.SecretsKey},
		{"EXERCISE_SECRETS_KEY", secret.PurposeExercise, c.Exercise.SecretsKeyDir, c.Exercise.SecretsSeal, &c.Exercise.SecretsKey},
	}
}

// ResolveKeys settles the keyring of each purpose and puts it where the single-variable form of that key is read, so
// everything downstream (secret.New, the render signer, the loaders of cmd/seed and cmd/loadtest-users, which read the
// configuration the same way) sees one form.
//
// A fresh install sets one thing: the master key (SECRETS_MASTER_KEY, or a directory SECRETS_MASTER_KEY_DIR with one
// <id>.key file per master version and a CURRENT file). The key of every purpose is derived from it (secret.Resolve).
// The older per-purpose keys (…_SECRETS_KEY / …_KEY_DIR) stay readable: they are added to the ring of their purpose as old
// keys, so data sealed before the master key opens, or, with …_SEAL=explicit, as the key that keeps sealing. Without a
// master key nothing changes: each purpose has its own key, from its directory or its variable.
//
// A directory wins over the variable of the same key; both set is allowed (a warning says which is used) so a rollout can
// move from one to the other. An unreadable or ambiguous directory, an unknown seal mode, or a ring with a duplicate id is
// an error: there is no fallback, which could be stale.
func (c *Config) ResolveKeys() error {
	master := c.Keys.SecretsMasterKey
	if c.Keys.SecretsMasterKeyDir != "" {
		spec, err := secret.SpecFromDir(c.Keys.SecretsMasterKeyDir)
		if err != nil {
			return fmt.Errorf("SECRETS_MASTER_KEY_DIR: %w", err)
		}
		if master != "" {
			log.Warn().Msg("SECRETS_MASTER_KEY and SECRETS_MASTER_KEY_DIR are both set: the directory is used, the variable is ignored")
		}
		master = spec
	}
	// Resolve every purpose before changing anything, so a failure leaves the configuration as it was.
	resolved := make([]string, 0, 4)
	purposes := c.keyPurposes()
	for _, k := range purposes {
		explicit := *k.key
		if k.dir != "" {
			spec, err := secret.SpecFromDir(k.dir)
			if err != nil {
				return fmt.Errorf("%s_DIR: %w", k.name, err)
			}
			if explicit != "" {
				log.Warn().Msgf("%s and %s_DIR are both set: the directory is used, the variable is ignored", k.name, k.name)
			}
			explicit = spec
		}
		var sealExplicit bool
		switch k.seal {
		case "", SealDerived:
		case SealExplicit:
			sealExplicit = true
		default:
			return fmt.Errorf("the seal mode of %s must be %s or %s", k.name, SealDerived, SealExplicit)
		}
		spec, err := secret.Resolve(master, explicit, k.purpose, sealExplicit)
		if err != nil {
			return fmt.Errorf("%s: %w", k.name, err)
		}
		resolved = append(resolved, spec)
	}
	for i, k := range purposes {
		*k.key = resolved[i]
	}
	return nil
}
