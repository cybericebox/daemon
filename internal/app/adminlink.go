package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/token"
)

// adminLinkIssuer is the part of the auth use case the admin-link command needs.
type adminLinkIssuer interface {
	IssueSuperAdminSetupLink(ctx context.Context) (authUseCase.AdminLink, error)
}

// RunAdminLink is the `admin-link` subcommand: it prints a one-time setup link for the configured
// super admin, for a platform that has no mail provider yet. It does not migrate (the server does)
// and refuses a database whose schema is not current. The link goes to out only.
func RunAdminLink(ctx context.Context, cfg *config.Config, out io.Writer) error {
	repo := repository.NewRepository(repository.Dependencies{PostgresConfig: &cfg.Infrastructure.Postgres})
	defer repo.Close()
	if err := repo.SchemaCurrent(); err != nil {
		return err
	}
	uc := authUseCase.NewAuthUseCase(authUseCase.Dependencies{
		Repo:   repo,
		Token:  token.MustNew(token.Config{TokenSignature: cfg.Auth.TokenSignature, SetupTokenTTL: cfg.Auth.SetupTokenTTL}),
		Config: cfg.Auth,
	})
	return printAdminLink(ctx, uc, out)
}

func printAdminLink(ctx context.Context, issuer adminLinkIssuer, out io.Writer) error {
	link, err := issuer.IssueSuperAdminSetupLink(ctx)
	if err != nil {
		return err
	}
	if _, err = fmt.Fprintf(out, "%s\nexpires at %s (single use)\n", link.URL, link.ExpiresAt.UTC().Format(time.RFC3339)); err != nil {
		return errors.Join(errors.New("write the admin link"), err)
	}
	return nil
}
