package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/delivery/repository/keyRotationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/secretKeysRepo"
	"github.com/cybericebox/daemon/internal/eventrender"
	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
	"github.com/cybericebox/daemon/internal/useCase/secretkeys"
	"github.com/cybericebox/daemon/pkg/secret"
)

// Exit codes of `keys check`.
const (
	CheckRetirable = 0 // the key may be removed from the servers
	CheckNotYet    = 1 // it may not yet; the reasons are printed
	CheckUndecided = 2 // no answer: an unknown family or key, an unreadable database
)

// keyLifetimes are the times that bound what lives outside the database.
func keyLifetimes(cfg *config.Config) secretkeys.Lifetimes {
	return secretkeys.Lifetimes{
		SessionIdle:      cfg.Auth.SessionIdleTTL,
		SessionAbsolute:  cfg.Auth.SessionAbsoluteTTL,
		EventRenderGrant: eventrender.MaxLifetime,
	}
}

// keyLocator says where a key is kept: a file of a key directory with its mode, or the variable it comes from. It stats
// the file, never reads it.
func keyLocator(cfg *config.Config) secretkeys.Locator {
	explicit := map[secretkeys.Family]struct{ dir, variable string }{
		secretkeys.FamilyPlatform: {cfg.Platform.SecretsKeyDir, "PLATFORM_SECRETS_KEY"},
		secretkeys.FamilyVPN:      {cfg.VPN.SecretsKeyDir, "VPN_SECRETS_KEY"},
		secretkeys.FamilyExercise: {cfg.Exercise.SecretsKeyDir, "EXERCISE_SECRETS_KEY"},
	}
	where := func(dir, variable, id string) string {
		if dir == "" {
			return fmt.Sprintf("given in the environment (%s): remove it there (the variable or the Secret it comes from)", variable)
		}
		path := filepath.Join(dir, id+".key")
		info, err := os.Stat(path)
		if err != nil {
			return fmt.Sprintf("%s was not found by this process (the key may come from %s or from a mount this process does not see)",
				secret.Printable(path, 200), variable)
		}
		return fmt.Sprintf("%s (mode %04o; the daemon does not see its owner or its backups)", secret.Printable(path, 200), info.Mode().Perm())
	}
	return func(f secretkeys.Family, id string) string {
		if master, derived := secret.IsDerivedID(id); derived {
			return "derived, not stored: it goes with the master key version, " + where(cfg.Keys.SecretsMasterKeyDir, "SECRETS_MASTER_KEY", master)
		}
		e := explicit[f]
		return where(e.dir, e.variable, id)
	}
}

// ErrCheckUndecided: `keys check` could not decide.
var ErrCheckUndecided = errors.New("keys check could not decide")

// checkCommand prints the answer of `keys check` and returns its exit code: for a family and a key, 0 when the key is
// retirable and 1 when it is not yet; with no family it prints the overview and returns 0. An unknown family or key is
// the error (exit 2).
func checkCommand(out io.Writer, rep secretkeys.Report, family, keyID string, locate secretkeys.Locator, l secretkeys.Lifetimes) (int, error) {
	if family == "" {
		secretkeys.WriteOverview(out, rep, l)
		return CheckRetirable, nil
	}
	v, err := rep.Verdict(secretkeys.Family(family), keyID)
	if err != nil {
		return CheckUndecided, fmt.Errorf("%w: %w", ErrCheckUndecided, err)
	}
	var master *secretkeys.MasterRule
	if v.Derived {
		m, mErr := rep.Master(v.MasterID, l)
		if mErr != nil {
			return CheckUndecided, fmt.Errorf("%w: %w", ErrCheckUndecided, mErr)
		}
		master = &m
	}
	secretkeys.WriteVerdict(out, v, locate, master)
	if v.Retirable {
		return CheckRetirable, nil
	}
	return CheckNotYet, nil
}

// snapshotDB is what the scan reads through: one transaction, so every table and the progress rows are one view.
type snapshotDB interface {
	postgres.DBTX
	keyRotationRepo.Querier
}

// KeysCheckReport scans the stored secrets and the re-encryption progress in one read-only snapshot of the database.
func KeysCheckReport(ctx context.Context, pool *pgxpool.Pool, ciphers secretkeys.Ciphers) (secretkeys.Report, error) {
	tx, err := secretKeysRepo.Snapshot(ctx, pool)
	if err != nil {
		return secretkeys.Report{}, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	return KeysCheckReportOn(ctx, pool, tx, ciphers)
}

// KeysCheckReportOn is KeysCheckReport on a snapshot the caller opened (the tests put a hook between its statements).
func KeysCheckReportOn(ctx context.Context, pool *pgxpool.Pool, db snapshotDB, ciphers secretkeys.Ciphers) (secretkeys.Report, error) {
	rep, err := secretkeys.New(secretKeysRepo.New(db), ciphers).Check(ctx, true)
	if err != nil {
		return secretkeys.Report{}, err
	}
	stored, err := keyRotationRepo.New(pool, keyrotation.Contexts()).ProgressIn(ctx, db)
	if err != nil {
		return secretkeys.Report{}, fmt.Errorf("read the re-encryption progress: %w", err)
	}
	rep.Rotation = rotationStates(stored)
	return rep, nil
}

// RunKeysCheck is `daemon keys check [<family> <key id>]`. It reads one snapshot of the database in a read-only transaction,
// holds no lock and changes nothing: no row, no key, no file.
func RunKeysCheck(ctx context.Context, cfg *config.Config, family, keyID string, out io.Writer) (int, error) {
	ciphers, err := loadCiphers(cfg)
	if err != nil {
		return CheckUndecided, err
	}
	repo := repository.NewRepository(repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres})
	defer repo.Close()
	if err = repo.SchemaCurrent(); err != nil {
		return CheckUndecided, err
	}
	rep, err := KeysCheckReport(ctx, repo.Pool(), ciphers)
	if err != nil {
		return CheckUndecided, err
	}
	return checkCommand(out, rep, family, keyID, keyLocator(cfg), keyLifetimes(cfg))
}
