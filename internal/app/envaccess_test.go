package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/pkg/agentcrypto"
)

func TestEnvAccess(t *testing.T) {
	if got, err := envAccess(config.AgentConfig{}); got != nil || err != nil {
		t.Fatalf("no key = %+v, %v", got, err)
	}
	key, err := agentcrypto.NewAccessKey()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = envAccess(config.AgentConfig{AccessPrivateKey: key.PrivateKeyPEM}); err == nil {
		t.Fatal("a key needs its id")
	}
	if _, err = envAccess(config.AgentConfig{AccessPrivateKey: "junk", AccessKeyID: "k"}); err == nil {
		t.Fatal("a junk key must fail")
	}
	// Without TLS the tenant is "default"; a one-line key with literal \n is accepted.
	oneLine := ""
	for _, r := range key.PrivateKeyPEM {
		if r == '\n' {
			oneLine += `\n`
		} else {
			oneLine += string(r)
		}
	}
	got, err := envAccess(config.AgentConfig{AccessPrivateKey: oneLine, AccessKeyID: "k-env"})
	if err != nil || got.Tenant != "default" || got.KeyID != "k-env" || len(got.Key) == 0 {
		t.Fatalf("no TLS = %+v, %v", got, err)
	}
	// With TLS the tenant is the CN of the client certificate file.
	certFile := filepath.Join(t.TempDir(), "client.crt")
	if err = os.WriteFile(certFile, []byte(selfSignedCert(t, "acme")), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg := config.AgentConfig{AccessPrivateKey: key.PrivateKeyPEM, AccessKeyID: "k-env"}
	cfg.TLS.Enabled, cfg.TLS.CertFile = true, certFile
	if got, err = envAccess(cfg); err != nil || got.Tenant != "acme" {
		t.Fatalf("TLS = %+v, %v", got, err)
	}
	cfg.TLS.CertFile = filepath.Join(t.TempDir(), "missing.crt")
	if _, err = envAccess(cfg); err == nil {
		t.Fatal("a missing certificate file must fail")
	}
}
