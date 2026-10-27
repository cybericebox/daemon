package app

import (
	"context"
	"fmt"
	"io"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	retentionUseCase "github.com/cybericebox/daemon/internal/useCase/retention"
)

// RunEraseLabTelemetry is the `erase-lab-telemetry <user id>` subcommand: it erases one person from the lab
// telemetry on request (an objection), whether or not the account still exists. The VPN client name becomes a random
// pseudonym in every table (minutes, journal, raw rows, current state, archive); IPs and counters stay. It does not
// migrate (the server does) and refuses a database whose schema is not current. The summary goes to out; it never
// prints the pseudonym, so nothing ties the person to it outside the database.
func RunEraseLabTelemetry(ctx context.Context, cfg *config.Config, userID string, out io.Writer) error {
	id, err := uuid.FromString(userID)
	if err != nil {
		return fmt.Errorf("the user id must be a UUID: %w", err)
	}
	repo := repository.NewRepository(repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres})
	defer repo.Close()
	if err = repo.SchemaCurrent(); err != nil {
		return err
	}
	uc := retentionUseCase.New(retentionUseCase.Dependencies{Store: retentionRepo.New(repo)})
	entry, chunks, err := uc.EraseLabTelemetryOf(ctx, id)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out,
		"user %s: erased from the lab telemetry of %d event(s), %d archive chunk(s) rewritten; "+
			"the retention job keeps rewriting what arrives under the old name for a day\n",
		id, len(entry.EventIDs), chunks)
	return err
}
