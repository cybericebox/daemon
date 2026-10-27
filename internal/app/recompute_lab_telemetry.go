package app

import (
	"context"
	"fmt"
	"io"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// RunRecomputeLabTelemetry is the `recompute-lab-telemetry <event id or tag>` subcommand: it rebuilds the
// lab usage minute tables and the transitions journal of one event from the raw lab observations and
// their archive (see EventAnalyticsUseCase.RecomputeLabTelemetry). It does not migrate (the server does)
// and refuses an event whose rollups are older than the archive and raw rows (unless force) and a database whose schema is not current. The summary goes to out.
func RunRecomputeLabTelemetry(ctx context.Context, cfg *config.Config, idOrTag string, force bool, out io.Writer) error {
	repo := repository.NewRepository(repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres})
	defer repo.Close()
	if err := repo.SchemaCurrent(); err != nil {
		return err
	}
	uc := eventAnalyticsUseCase.New(eventAnalyticsUseCase.Dependencies{Store: eventAnalyticsRepo.New(repo)})
	eventID, err := uc.ResolveEvent(ctx, idOrTag)
	if err != nil {
		return err
	}
	res, err := uc.RecomputeLabTelemetry(ctx, eventID, force)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(out,
		"event %s: %d archive chunks read, %d raw rows restored, %d observations rolled up, %d in the journal\n",
		eventID, res.ArchiveChunks, res.RestoredRows, res.UsageObservations, res.JournalObservations)
	return err
}
