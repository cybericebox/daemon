// lifecycleworker drives the actual PostgreSQL/River/native agent seam for END
// evidence. It is separate from the application writer's files and is not run
// during the code-only integration phase.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/infrastructure/labagent"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	lablifecycleJob "github.com/cybericebox/daemon/internal/jobs/lablifecycle"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
	"github.com/cybericebox/daemon/internal/useCase/event"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/riverqueue/river"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
)

type configuration struct {
	SourceCommit                       string
	DSNFile, CertFile, KeyFile, CAFile string
	AgentEndpoint, Instance            string
}

func privateFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("private fixture file unavailable")
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
		return nil, fmt.Errorf("private fixture file must be mode0600")
	}
	return os.ReadFile(path)
}
func run() error {
	configPath := flag.String("config", "", "private END fixture configuration file")
	flag.Parse()
	raw, err := privateFile(*configPath)
	if err != nil {
		return err
	}
	var cfg configuration
	if json.Unmarshal(raw, &cfg) != nil {
		return fmt.Errorf("invalid fixture configuration")
	}
	var provenance struct{ SourceCommit string }
	raw, err = os.ReadFile("third_party/laboratory-sdk/PROVENANCE.json")
	if err != nil || json.Unmarshal(raw, &provenance) != nil || cfg.SourceCommit != provenance.SourceCommit {
		return fmt.Errorf("fixture does not match exact SDK source gate")
	}
	dsn, err := privateFile(cfg.DSNFile)
	if err != nil {
		return err
	}
	cert, err := privateFile(cfg.CertFile)
	if err != nil {
		return err
	}
	key, err := privateFile(cfg.KeyFile)
	if err != nil {
		return err
	}
	ca, err := privateFile(cfg.CAFile)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	pool, err := pgxpool.New(ctx, strings.TrimSpace(string(dsn)))
	if err != nil {
		return fmt.Errorf("fixture database connection failed")
	}
	defer pool.Close()
	if pool.Ping(ctx) != nil {
		return fmt.Errorf("fixture database unavailable")
	}
	agent, err := labagent.NewFromConnection(labagent.Connection{Endpoint: cfg.AgentEndpoint, CertPEM: cert, KeyPEM: key, CAPEM: ca}, cfg.Instance)
	if err != nil {
		return fmt.Errorf("fixture agent connection failed")
	}
	defer agent.Close()
	// The real adapter obtains LifecycleFeature from this actual mTLS runtime.
	if agent.Health(ctx) != nil {
		return fmt.Errorf("fixture agent health failed")
	}
	features, err := agent.GetFeatures(ctx, &labpb.Empty{})
	if err != nil || !features.GetLifecycle().GetPerLabStop() || !features.GetLifecycle().GetConfirmedRuntime() {
		return fmt.Errorf("actual runtime lifecycle capability is not qualified")
	}
	migrator, err := rivermigrate.New(riverpgxv5.New(pool), nil)
	if err != nil {
		return fmt.Errorf("River migrator unavailable")
	}
	if _, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil); err != nil {
		return fmt.Errorf("River migration failed")
	}
	uc := event.NewEventUseCase(event.Dependencies{Repo: postgres.New(pool), Infra: agent})
	workers := river.NewWorkers()
	river.AddWorker(workers, lablifecycleJob.NewWorker(uc))
	client, err := river.NewClient(riverpgxv5.New(pool), &river.Config{Workers: workers, Queues: map[string]river.QueueConfig{river.QueueDefault: {MaxWorkers: 1}}, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), PeriodicJobs: []*river.PeriodicJob{river.NewPeriodicJob(river.PeriodicInterval(time.Second), func() (river.JobArgs, *river.InsertOpts) {
		return jobsModel.LabLifecycleArgs{}, &river.InsertOpts{MaxAttempts: 1}
	}, &river.PeriodicJobOpts{RunOnStart: true})}})
	if err != nil {
		return fmt.Errorf("River client initialization failed")
	}
	if err = client.Start(ctx); err != nil {
		return fmt.Errorf("River worker start failed")
	}
	fmt.Println("actual River/native lifecycle worker started")
	<-ctx.Done()
	stop, stopCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer stopCancel()
	if client.Stop(stop) != nil {
		return fmt.Errorf("River worker stop failed")
	}
	fmt.Println("actual River/native lifecycle worker stopped")
	return nil
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
