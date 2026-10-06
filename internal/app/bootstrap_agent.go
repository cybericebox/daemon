package app

import (
	"context"
	"os"
	"strings"

	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// agentBootstrapper is the part of the agents use case the startup needs.
type agentBootstrapper interface {
	BootstrapAgent(ctx context.Context, in infraModel.AgentEnrollment) (bool, error)
}

// bootstrapAgent enrolls the agent named by AGENT_ENDPOINT with AGENT_ENROLLMENT_TOKEN when no live agent
// with that endpoint exists yet. The agent then lives in the database like any other; the token is not
// used again. A failure is a clear log, never a reason to stop the daemon.
func bootstrapAgent(ctx context.Context, cfg config.AgentConfig, agents agentBootstrapper) {
	if cfg.Endpoint == "" {
		log.Warn().Msg("No AGENT_ENDPOINT: add an infrastructure agent in the admin; until then operations that need infrastructure are blocked")
		return
	}
	var ca string
	if cfg.CAFile != "" {
		raw, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			log.Error().Err(err).Str("file", cfg.CAFile).Msg("Agent bootstrap: cannot read AGENT_CA_FILE")
			return
		}
		ca = string(raw)
	}
	enrolled, err := agents.BootstrapAgent(ctx, infraModel.AgentEnrollment{
		Name: cfg.Name, Endpoint: cfg.Endpoint, Token: cfg.EnrollmentToken, CAPEM: ca, Enabled: true, Priority: 100,
	})
	switch {
	case err != nil:
		log.Error().Err(err).Str("endpoint", cfg.Endpoint).Msg("Agent bootstrap failed: check AGENT_ENROLLMENT_TOKEN (one-time, expires) and the endpoint; the daemon starts without this agent, add it in the admin")
	case enrolled:
		log.Info().Str("endpoint", cfg.Endpoint).Msg("Infrastructure agent enrolled from the configuration")
	case strings.TrimSpace(cfg.EnrollmentToken) != "":
		log.Warn().Str("endpoint", cfg.Endpoint).Msg("AGENT_ENROLLMENT_TOKEN is ignored: the agent is already enrolled; remove the token from the configuration")
	}
}
