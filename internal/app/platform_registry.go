package app

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	"github.com/cybericebox/daemon/pkg/secret"
)

// platformRegistry reads the platform's own registry (PLATFORM_REGISTRY_*) from the configuration. A half-set or
// malformed one is a start-up error: a daemon that silently fell back to the provider settings would store
// images where the deployment did not mean them to go. The log names the host, never the account or password.
func platformRegistry(cfg *config.Config) imageRegistryUseCase.OwnRegistry {
	own := imageRegistryUseCase.OwnRegistry{
		Host: cfg.Platform.RegistryHost, Username: cfg.Platform.RegistryUsername, Password: cfg.Platform.RegistryPassword,
	}
	if own.Host == "" && (own.Username != "" || own.Password != "") {
		log.Fatal().Msg("PLATFORM_REGISTRY_USERNAME/PASSWORD are set but PLATFORM_REGISTRY_HOST is not")
	}
	if err := own.Validate(); err != nil {
		log.Fatal().Err(err).Msg("The platform's own image registry is misconfigured")
	}
	if own.Enabled() {
		log.Info().Str("host", own.Host).Msg("Platform images are stored in the platform's own registry")
	}
	return own
}

// RunImagesMigrateUploads is `daemon images migrate-uploads`: it copies the platform images that were uploaded to
// the Docker Hub / GHCR registry saved in the platform settings into the own registry (see MigrateUploads). It does
// not migrate the schema (the server does) and refuses a database whose schema is not current. One line per image
// goes to out. The returned count is the images that could not be copied (the command exits 1 then).
func RunImagesMigrateUploads(ctx context.Context, cfg *config.Config, out io.Writer) (int, error) {
	own := imageRegistryUseCase.OwnRegistry{
		Host: cfg.Platform.RegistryHost, Username: cfg.Platform.RegistryUsername, Password: cfg.Platform.RegistryPassword,
	}
	if !own.Enabled() {
		return 0, imageRegistryUseCase.ErrMigrateNeedsOwn
	}
	if err := own.Validate(); err != nil {
		return 0, err
	}
	ciphers, err := loadCiphers(cfg)
	if err != nil {
		return 0, err
	}
	if ciphers.Platform == nil {
		return 0, errors.New("PLATFORM_SECRETS_KEY is not set: the saved registry token cannot be opened")
	}
	repo := repository.NewRepository(repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres})
	defer repo.Close()
	if err = repo.SchemaCurrent(); err != nil {
		return 0, err
	}
	var sealer secret.Sealer = ciphers.Platform
	uc := imageRegistryUseCase.New(imageRegistryUseCase.Dependencies{Repo: repo, Cipher: sealer, Own: own})
	report, err := uc.MigrateUploads(ctx, func(line string) { _, _ = fmt.Fprintln(out, line) })
	if err != nil {
		return report.Failed, err
	}
	_, _ = fmt.Fprintf(out, "%d image(s): %d copied, %d already present, %d failed\n", report.Images, report.Copied, report.Present, report.Failed)
	return report.Failed, nil
}
