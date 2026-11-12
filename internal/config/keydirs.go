package config

import (
	"fmt"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/pkg/secret"
)

// ResolveKeyDirs reads the key directories (SESSION_ENCRYPTION_KEY_DIR, PLATFORM_SECRETS_KEY_DIR, VPN_SECRETS_KEY_DIR,
// EXERCISE_SECRETS_KEY_DIR) and puts the keyring each holds where the single-variable form of the same key is read, so
// everything downstream sees one form. A directory wins over the variable; both set is allowed (a warning says which
// is used) so a rollout can move from one to the other. An unreadable or ambiguous directory is an error: there is no
// fallback to the variable, which could be stale.
func (c *Config) ResolveKeyDirs() error {
	for _, k := range []struct {
		name, dir string
		key       *string
	}{
		{"SESSION_ENCRYPTION_KEY", c.Auth.SessionEncryptionKeyDir, &c.Auth.SessionEncryptionKey},
		{"PLATFORM_SECRETS_KEY", c.Platform.SecretsKeyDir, &c.Platform.SecretsKey},
		{"VPN_SECRETS_KEY", c.VPN.SecretsKeyDir, &c.VPN.SecretsKey},
		{"EXERCISE_SECRETS_KEY", c.Exercise.SecretsKeyDir, &c.Exercise.SecretsKey},
	} {
		if k.dir == "" {
			continue
		}
		spec, err := secret.SpecFromDir(k.dir)
		if err != nil {
			return fmt.Errorf("%s_DIR: %w", k.name, err)
		}
		if *k.key != "" {
			log.Warn().Msgf("%s and %s_DIR are both set: the directory is used, the variable is ignored", k.name, k.name)
		}
		*k.key = spec
	}
	return nil
}
