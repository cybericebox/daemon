package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/delivery/repository/keyRotationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/secretKeysRepo"
	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
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

// loadCiphers builds the keyrings of the three families from the configuration.
func loadCiphers(cfg *config.Config) (secretkeys.Ciphers, error) {
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
			return ciphers, fmt.Errorf("%s is invalid: %w", k.name, err)
		}
	}
	return ciphers, nil
}

// RunKeysStatus is `daemon keys status`: it opens every stored keyed value with its row context and prints, per table
// column, how many were sealed by each key, how many are in the older form without a key id and which cannot be opened
// (counted per reason class), then the state of the re-encryption runs. It reads only. The returned count is the number
// of values that cannot be opened (the command exits 1 when it is not zero), so a script can ask "may this key be removed".
func RunKeysStatus(ctx context.Context, cfg *config.Config, strict bool, out io.Writer) (int, error) {
	ciphers, err := loadCiphers(cfg)
	if err != nil {
		return 0, err
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
	stored, err := keyRotationRepo.New(repo.Pool(), keyrotation.Contexts()).Progress(ctx)
	if err != nil {
		return 0, fmt.Errorf("read the re-encryption progress: %w", err)
	}
	rep.Rotation = rotationStates(stored)
	rep.Write(out)
	return rep.StrictProblems(strict), nil
}

// ErrUnexpectedKey: the key that would seal is not the one the caller expects (a wrong ring order).
var ErrUnexpectedKey = errors.New("the sealing key is not the expected one; nothing was done")

// rotationStates turns the stored progress rows into what the report shows.
func rotationStates(stored []keyRotationRepo.StoredProgress) []secretkeys.RotationState {
	out := make([]secretkeys.RotationState, 0, len(stored))
	for _, p := range stored {
		out = append(out, secretkeys.RotationState{
			Family: p.Family, Source: p.Source, KeyID: p.KeyID, State: p.State, Total: p.Total, Done: p.Done,
			Failed: p.Failed, Conflicts: p.Conflicts, FailedByReason: p.FailedByReason, UpdatedAt: p.UpdatedAt,
			ProcessGone: p.ProcessGone, LockUnknown: p.LockUnknown, LastMovedAt: p.LastMovedAt,
		})
	}
	return out
}

// ErrNeedsYes: a real re-encryption run was started without --yes.
var ErrNeedsYes = errors.New("a real run needs --yes (use --dry-run to look first)")

// reencrypter is what the command needs of the use case.
type reencrypter interface {
	Keys(family string) (sealing string, readOnly []string, err error)
	Run(ctx context.Context, family string, opts keyrotation.Options) (keyrotation.Report, error)
}

// reencryptCommand prints the three preconditions, refuses a real run without --yes, runs and prints the report. It
// returns the number of rows that could not be opened or were changed meanwhile; a store error or an interruption is
// the error (the partial report is printed first).
func reencryptCommand(ctx context.Context, uc reencrypter, family string, dryRun, yes bool, expectKey string, out io.Writer) (int, error) {
	fmt.Fprintln(out, "Before a real run:")
	for i, line := range keyrotation.Preconditions {
		fmt.Fprintf(out, "  %d. %s\n", i+1, line)
	}
	fmt.Fprintln(out)
	sealing, readOnly, err := uc.Keys(family)
	if err != nil {
		return 0, err
	}
	fmt.Fprintf(out, "Values will be sealed with key %q.\n", secret.SafeKeyID(sealing))
	if len(readOnly) == 0 {
		fmt.Fprintln(out, "No other key is loaded for this family: nothing can be read from an older key.")
	} else {
		shown := make([]string, len(readOnly))
		for i, id := range readOnly {
			shown[i] = fmt.Sprintf("%q", secret.SafeKeyID(id))
		}
		fmt.Fprintf(out, "Read only (never seal): %s.\n", strings.Join(shown, ", "))
	}
	fmt.Fprintln(out, "If the sealing key is the OLD one, stop: the new key must be first in the keyring.")
	fmt.Fprintln(out)
	if expectKey != "" && expectKey != sealing {
		return 0, fmt.Errorf("%w (expected %q, found %q)", ErrUnexpectedKey, secret.SafeKeyID(expectKey), secret.SafeKeyID(sealing))
	}
	if !dryRun && !yes {
		return 0, ErrNeedsYes
	}
	rep, err := uc.Run(ctx, family, keyrotation.Options{DryRun: dryRun})
	if len(rep.Sources) > 0 {
		rep.Write(out)
	}
	if err != nil {
		return 0, err
	}
	n := rep.Failed()
	for _, s := range rep.Sources {
		n += s.Conflicts
	}
	return n, nil
}

// signalContext ends the context on SIGINT or SIGTERM, so a Ctrl+C or a stop of the container ends the run as
// interrupted (the progress row says so) instead of leaving it running.
// After the first signal the handler is released, so a second Ctrl+C ends the process at once.
func signalContext(parent context.Context, signals ...os.Signal) (context.Context, context.CancelFunc) {
	if len(signals) == 0 {
		signals = []os.Signal{os.Interrupt, syscall.SIGTERM}
	}
	ctx, cancel := context.WithCancel(parent)
	c := make(chan os.Signal, 1)
	signal.Notify(c, signals...)
	go func() {
		select {
		case <-c:
			// Released before the context ends: whoever sees the cancel knows a second signal is no longer caught.
			signal.Stop(c)
			cancel()
		case <-ctx.Done():
			signal.Stop(c)
		}
	}()
	return ctx, cancel
}

// RunKeysReencrypt is `daemon keys reencrypt <family> [--dry-run] [--yes] [--expect-key <id>]`.
func RunKeysReencrypt(ctx context.Context, cfg *config.Config, family string, dryRun, yes bool, expectKey string, out io.Writer) (int, error) {
	ciphers, err := loadCiphers(cfg)
	if err != nil {
		return 0, err
	}
	repo := repository.NewRepository(repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres})
	defer repo.Close()
	if err = repo.SchemaCurrent(); err != nil {
		return 0, err
	}
	rings := keyrotation.Rings{
		keyRotationRepo.FamilyPlatform: ciphers.Platform,
		keyRotationRepo.FamilyVPN:      ciphers.VPN,
		keyRotationRepo.FamilyExercise: ciphers.Exercise,
	}
	uc := keyrotation.New(keyRotationRepo.New(repo.Pool(), keyrotation.Contexts()), rings)
	ctx, stop := signalContext(ctx)
	defer stop()
	return reencryptCommand(ctx, uc, family, dryRun, yes, expectKey, out)
}
