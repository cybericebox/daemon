package labagent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/config"
)

// With no CA file the agent is verified against the system roots: the client is
// built (the connection is lazy) and nothing tries to read a CA.
func TestNewWithoutCAFileUsesSystemRoots(t *testing.T) {
	dir := t.TempDir()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "platform"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(key)
	certFile, keyFile := filepath.Join(dir, "c.crt"), filepath.Join(dir, "c.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}

	cfg := config.AgentConfig{Endpoint: "ctl.example.test:443"}
	cfg.TLS = config.TLSConfig{Enabled: true, CertFile: certFile, KeyFile: keyFile}
	c, err := New(cfg)
	if err != nil {
		t.Fatalf("empty CA file must be accepted: %v", err)
	}
	_ = c.Close()

	cfg.TLS.CAFile = filepath.Join(dir, "missing.pem")
	if _, err := New(cfg); err == nil {
		t.Fatal("a configured but missing CA file must still fail")
	}
}
