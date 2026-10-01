package app

import (
	"fmt"
	"os"
	"strings"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/infrastructure/agentfleet"
	"github.com/cybericebox/daemon/pkg/agentcrypto"
)

// defaultTenant is the tenant of a call without a client certificate (TLS off, local development).
const defaultTenant = "default"

// envAccess reads the signing key of the environment agent's lab access tokens. The tenant is the CN of
// its client certificate (one source of the tenant name), "default" without TLS. nil when no key is set.
func envAccess(cfg config.AgentConfig) (*agentfleet.EnvAccess, error) {
	if strings.TrimSpace(cfg.AccessPrivateKey) == "" {
		return nil, nil
	}
	if cfg.AccessKeyID == "" {
		return nil, fmt.Errorf("AGENT_ACCESS_KEY_ID is required with AGENT_ACCESS_PRIVATE_KEY")
	}
	key, err := agentcrypto.ParseAccessPrivateKey(strings.ReplaceAll(cfg.AccessPrivateKey, `\n`, "\n"))
	if err != nil {
		return nil, fmt.Errorf("AGENT_ACCESS_PRIVATE_KEY: %w", err)
	}
	tenant := defaultTenant
	if cfg.TLS.Enabled {
		certPEM, readErr := os.ReadFile(cfg.TLS.CertFile)
		if readErr != nil {
			return nil, fmt.Errorf("read the agent client certificate for its tenant: %w", readErr)
		}
		cert, parseErr := agentcrypto.ReadCertificate(string(certPEM))
		if parseErr != nil {
			return nil, fmt.Errorf("agent client certificate: %w", parseErr)
		}
		tenant = cert.Tenant
	}
	return &agentfleet.EnvAccess{Tenant: tenant, KeyID: cfg.AccessKeyID, Key: key}, nil
}
