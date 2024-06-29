package repository

import (
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/agent"
	"github.com/cybericebox/daemon/internal/delivery/repository/email"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/storageS3"
	"github.com/cybericebox/daemon/internal/delivery/repository/vpn"
)

type (
	Repository struct {
		*storageS3.StorageS3Repository
		*postgres.PostgresRepository
		*email.EmailRepository
		*agent.AgentRepository
		*vpn.VPNRepository
	}

	Dependencies struct {
		Config *config.RepositoryConfig
	}
)

func NewRepository(deps Dependencies) *Repository {
	return &Repository{
		storageS3.NewRepository(storageS3.Dependencies{}),
		postgres.NewRepository(postgres.Dependencies{Config: &deps.Config.Postgres}),
		email.NewRepository(email.Dependencies{Config: &deps.Config.Email}),
		agent.NewRepository(agent.Dependencies{Config: &deps.Config.Agent}),
		vpn.NewRepository(vpn.Dependencies{Config: &deps.Config.VPN}),
	}
}
