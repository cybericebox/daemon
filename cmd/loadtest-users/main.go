// Command loadtest-users creates and removes the accounts of a load-test run.
//
//	loadtest-users create --run <id> --count <n> [--manual 3] --out <file|->
//	loadtest-users delete --run <id> [--dry-run]
//
// It reads the same environment as the daemon and runs inside the backend pod (kubectl exec) or a
// one-off Job. Every address carries the run marker (lt-<run>-NNN@loadtest.invalid), so delete
// removes exactly that run. The credentials never reach a log: `--out -` streams them to the
// caller's stdout. See tools/loadtest/RUNBOOK.md.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	"github.com/cybericebox/daemon/internal/delivery/repository/sessionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/loadtest"
	sessionPkg "github.com/cybericebox/daemon/internal/session"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/secret"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "loadtest-users:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 || (args[0] != "create" && args[0] != "delete") {
		return fmt.Errorf("usage: loadtest-users create --run <id> --count <n> --out <file|-> | delete --run <id> [--dry-run]")
	}
	command := args[0]
	fs := flag.NewFlagSet(command, flag.ContinueOnError)
	runID := fs.String("run", "", "run id: 3-20 characters of a-z and 0-9")
	count := fs.Int("count", 0, "create: how many accounts for the load script")
	manual := fs.Int("manual", 3, "create: extra accounts kept for a person, marked manual, never used by the script")
	out := fs.String("out", "", "create: credentials file, or - for stdout")
	dryRun := fs.Bool("dry-run", false, "delete: list what would be removed")
	if err := fs.Parse(args[1:]); err != nil {
		return err
	}
	if err := loadtest.ValidateRun(*runID); err != nil {
		return err
	}
	if command == "create" && *out == "" {
		return fmt.Errorf("--out is required: a file path or - for stdout")
	}

	cfg := config.MustGetConfig()
	repo := repository.NewRepository(repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres})
	defer repo.Close()

	cipher, err := secret.New(cfg.Auth.SessionEncryptionKey)
	if err != nil {
		return fmt.Errorf("SESSION_ENCRYPTION_KEY: %w", err)
	}
	pw := password.New(password.Config{Complexity: password.ComplexityConfig{
		MinLength:            cfg.Auth.Password.MinLength,
		MaxLength:            cfg.Auth.Password.MaxLength,
		MinCapitalLetters:    cfg.Auth.Password.MinCapitalLetters,
		MinSmallLetters:      cfg.Auth.Password.MinSmallLetters,
		MinDigits:            cfg.Auth.Password.MinDigits,
		MinSpecialCharacters: cfg.Auth.Password.MinSpecialCharacters,
	}})
	svc := loadtest.New(
		userRepo.New(repo.Queries), sessionRepo.New(repo.Queries), sessionPkg.NewCodec(cipher), pw.Hash,
		loadtest.Config{
			Lifetimes:  sessionPkg.Lifetimes{Idle: cfg.Auth.SessionIdleTTL, Absolute: cfg.Auth.SessionAbsoluteTTL},
			Complexity: pw.Complexity(),
		},
		os.Stderr, // progress goes to stderr so `--out -` keeps stdout clean
	)

	ctx := context.Background()
	if command == "delete" {
		_, err = svc.Delete(ctx, *runID, *dryRun)
		return err
	}
	file, err := svc.Create(ctx, *runID, *count, *manual)
	if err != nil {
		return err
	}
	return loadtest.Write(*out, os.Stdout, file)
}
