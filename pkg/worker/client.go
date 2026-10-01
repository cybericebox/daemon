package worker

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/rs/zerolog/log"
)

type (
	workerClient struct {
		client *river.Client[pgx.Tx]
		pool   *pgxpool.Pool
	}

	IEnqueuerFactory interface {
		NewEnqueuer() IEnqueuer
	}

	iWorkerRegistry interface {
		RegisterAll(workers *river.Workers)
		PeriodicJobs() []*river.PeriodicJob
	}
)

func NewWorkerClient(pool *pgxpool.Pool) *workerClient {
	return &workerClient{
		pool: pool,
	}
}

func (wc *workerClient) Initialize(ctx context.Context, registry iWorkerRegistry) {
	driver := riverpgxv5.New(wc.pool)

	migrator, err := rivermigrate.New(driver, nil)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create river migrator")
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		log.Fatal().Err(err).Msg("Failed to run river migrations")
	}
	workers := river.NewWorkers()
	registry.RegisterAll(workers)

	client, err := river.NewClient[pgx.Tx](
		driver, &river.Config{
			Queues:       map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 10}},
			Workers:      workers,
			PeriodicJobs: registry.PeriodicJobs(),
		},
	)
	if err != nil {
		log.Fatal().Err(err).Msg("Failed to create river client")
	}

	if err = client.Start(ctx); err != nil {
		log.Fatal().Err(err).Msg("Failed to start river client")
	}
	wc.client = client
}

func (wc *workerClient) Stop(ctx context.Context) {
	if wc.client == nil {
		log.Warn().Msg("River client is not initialized")
		return
	}

	// Graceful stop: waits for in-flight jobs to finish, releasing this client's
	// pool connections. If ctx expires first, River leaves work (and its
	// connections) running in the background — which would then hang pool.Close.
	// Escalate to a hard stop that cancels in-progress job contexts so the
	// connections are released promptly.
	if err := wc.client.Stop(ctx); err != nil {
		log.Warn().Err(err).Msg("Graceful river stop did not finish; forcing cancel")
		forceCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := wc.client.StopAndCancel(forceCtx); err != nil {
			log.Error().Err(err).Msg("Forced river stop failed")
		}
	}
}

// NewEnqueuer returns a live enqueuer bound to this worker client. The River
// client itself is late-bound: ucs (which calls NewEnqueuer) is constructed
// BEFORE Initialize starts River, so the enqueuer resolves factory.client at
// Enqueue time rather than snapshotting it here (a nil-guarded error until the
// worker is up).
func (wc *workerClient) NewEnqueuer() IEnqueuer {
	return &enqueuer{factory: wc}
}
