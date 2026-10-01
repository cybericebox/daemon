package repository

import (
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type (
	Repository struct {
		*postgres.PostgresRepository
	}

	Dependencies struct {
		PostgresConfig *config.PostgresConfig
	}
)

func NewRepository(deps Dependencies) *Repository {
	return &Repository{
		postgres.NewRepository(postgres.Dependencies{Config: deps.PostgresConfig}),
	}
}

func (r *Repository) Close() {
	r.PostgresRepository.Close()
}
