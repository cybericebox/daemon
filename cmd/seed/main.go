// Command seed fills a development database with test data for live end-to-end checks, or
// removes it again (--delete). It reads the same environment as the daemon (`make seed` loads
// .env) and refuses to run in production. See the README, section «Test data».
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/seed"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/secret"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		profile     = flag.String("profile", "small", "event size: small (3 teams x 3 users) or load (16 teams x 5 users)")
		startIn     = flag.Duration("start-in", 30*time.Minute, "how long from now the event starts (at least 1m)")
		duration    = flag.Duration("duration", 3*time.Hour, "how long the event lasts")
		reveal      = flag.String("reveal", string(eventConfigModel.RevealAllReady), "task reveal mode: all_ready or as_ready")
		tag         = flag.String("event-tag", seed.DefaultEventTag, "event subdomain; must start with \""+seed.TagPrefix+"\"")
		infra       = flag.Bool("infrastructure", true, "allow dynamic labs on the event and seed the lab exercises")
		credentials = flag.String("credentials", seed.DefaultCredentialsPath, "file for the seeded accounts and their password (gitignored)")
		del         = flag.Bool("delete", false, "remove everything the seeder created instead of creating it")
		dryRun      = flag.Bool("dry-run", false, "with --delete: list what would be removed")
		allowRemote = flag.Bool("allow-remote", false, "allow a database that is not on this machine")
	)
	flag.Parse()

	// Before any config or connection: a production environment is never touched.
	if strings.EqualFold(strings.TrimSpace(os.Getenv("ENV")), config.Production) {
		return fmt.Errorf("refusing to run: ENV=%s", config.Production)
	}
	if *dryRun && !*del {
		return fmt.Errorf("--dry-run only goes with --delete")
	}
	selected, err := seed.ProfileByName(*profile)
	if err != nil {
		return err
	}

	cfg := config.MustGetConfig()
	if cfg.Environment == config.Production {
		return fmt.Errorf("refusing to run: ENV=%s", config.Production)
	}
	if host := cfg.Infrastructure.Postgres.Host; !*allowRemote && !isLocal(host) {
		return fmt.Errorf("the database host %q is not local; pass --allow-remote if this is meant", host)
	}

	repo := repository.NewRepository(repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres})
	defer repo.Close()

	deps := seed.Deps{
		Queries: repo.Queries,
		Pool:    repo.Pool(),
		Password: password.New(password.Config{Complexity: password.ComplexityConfig{
			MinLength:            cfg.Auth.Password.MinLength,
			MaxLength:            cfg.Auth.Password.MaxLength,
			MinCapitalLetters:    cfg.Auth.Password.MinCapitalLetters,
			MinSmallLetters:      cfg.Auth.Password.MinSmallLetters,
			MinDigits:            cfg.Auth.Password.MinDigits,
			MinSpecialCharacters: cfg.Auth.Password.MinSpecialCharacters,
		}}),
	}
	if key := cfg.Exercise.SecretsKey; key != "" {
		deps.Cipher, _ = secret.New(key)
	}
	seeder := seed.New(deps)
	opts := seed.Options{
		Profile:         selected,
		StartIn:         *startIn,
		Duration:        *duration,
		RevealMode:      eventConfigModel.TaskRevealMode(*reveal),
		EventTag:        *tag,
		Infrastructure:  *infra,
		CredentialsPath: *credentials,
		DryRun:          *dryRun,
		Out:             os.Stdout,
	}
	ctx := context.Background()

	if *del {
		report, cleanupErr := seeder.Cleanup(ctx, opts)
		if cleanupErr != nil {
			return cleanupErr
		}
		for _, kept := range report.Kept {
			fmt.Println("kept:", kept)
		}
		return nil
	}

	report, err := seeder.Seed(ctx, opts)
	if err != nil {
		return err
	}
	fmt.Printf("\nDone: %d accounts, %d exercises, %d teams.\n", report.Users, report.Exercises, report.Teams)
	fmt.Printf("Event: %s.%s, starts %s.\n", report.EventTag, cfg.Auth.Hosts.EventDomain, report.StartAt.Local().Format("15:04:05"))
	fmt.Printf("Accounts and the password: %s\n", report.CredentialsPath)
	return nil
}

func isLocal(host string) bool {
	switch strings.ToLower(host) {
	case "", "localhost", "127.0.0.1", "::1", "host.docker.internal":
		return true
	}
	return false
}
