package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/delivery/repository/secretKeysRepo"
	"github.com/cybericebox/daemon/internal/useCase/secretkeys"
	"github.com/cybericebox/daemon/pkg/secret"
)

// secretChecker is what the guard needs of the scan.
type secretChecker interface {
	Check(ctx context.Context, open bool) (secretkeys.Report, error)
}

// ErrSecretKeysLost is what start-up returns, in the refuse mode, when stored ciphertext cannot be opened.
var ErrSecretKeysLost = errors.New("stored ciphertext names a key that is not loaded")

// guardSecrets is the start-up guard against a lost key. It compares the key id every stored ciphertext names with the
// loaded keyrings (no value is opened, so it is cheap and cannot misread a value). In "warn" it logs an error per
// affected column and goes on; in "refuse" it also returns ErrSecretKeysLost so the daemon does not start; "off" skips it.
// A scan that itself fails (the database is read through the same pool as the rest of start-up) is logged and does not
// stop the start in warn mode.
func guardSecrets(ctx context.Context, mode string, checker secretChecker) error {
	if mode == config.SecretsGuardOff {
		return nil
	}
	rep, err := checker.Check(ctx, false)
	if err != nil {
		if mode == config.SecretsGuardRefuse {
			return fmt.Errorf("secrets guard: %w", err)
		}
		log.Error().Err(err).Msg("Secrets guard: the stored ciphertext could not be checked")
		return nil
	}
	if rep.Problems() == 0 {
		return nil
	}
	for _, col := range rep.Columns {
		if col.Problems == 0 {
			continue
		}
		for _, p := range col.ProblemRefs {
			log.Error().Str("column", col.Name).Str("kind", p.Kind).Str("key_id", p.KeyID).Str("row", p.Ref).
				Msg("Secrets guard: a stored value cannot be opened with the loaded keys")
		}
		log.Error().Str("column", col.Name).Int("values", col.Problems).
			Msg("Secrets guard: values of this column cannot be opened; load the missing key (run `daemon keys status` for the full list)")
	}
	if mode == config.SecretsGuardRefuse {
		return fmt.Errorf("%w: %d value(s); run `daemon keys status`, or set SECRETS_GUARD=warn to start anyway", ErrSecretKeysLost, rep.Problems())
	}
	return nil
}

func newSecretKeys(repo *repository.Repository, c secretkeys.Ciphers) *secretkeys.UseCase {
	return secretkeys.New(secretKeysRepo.New(repo.Pool()), c)
}

// RunKeysStatus is `daemon keys status`: it opens every stored keyed value with its row context and prints, per table
// column, how many were sealed by each key, how many are in the older form without a key id and which cannot be opened. It
// reads only. The returned count is the number of values that cannot be opened (the command exits 1 when it is not zero),
// so a script can ask "may this key be removed".
func RunKeysStatus(ctx context.Context, cfg *config.Config, out io.Writer) (int, error) {
	var ciphers secretkeys.Ciphers
	var err error
	for _, k := range []struct {
		name, spec string
		to         **secret.Cipher
	}{
		{"PLATFORM_SECRETS_KEY", cfg.Platform.SecretsKey, &ciphers.Platform},
		{"VPN_SECRETS_KEY", cfg.VPN.SecretsKey, &ciphers.VPN},
		{"EXERCISE_SECRETS_KEY", cfg.Exercise.SecretsKey, &ciphers.Exercise},
	} {
		if *k.to, err = newCipher(k.spec); err != nil {
			return 0, fmt.Errorf("%s is invalid: %w", k.name, err)
		}
	}
	repo := repository.NewRepository(repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres})
	defer repo.Close()
	if err = repo.SchemaCurrent(); err != nil {
		return 0, err
	}
	rep, err := newSecretKeys(repo, ciphers).Check(ctx, true)
	if err != nil {
		return 0, err
	}
	rep.Write(out)
	return rep.Problems(), nil
}
