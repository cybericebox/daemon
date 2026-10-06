package labagent

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"
)

func testKeyPair(t *testing.T) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "platform"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func TestConnectionValidatesTheMaterialWithoutDialing(t *testing.T) {
	cert, key := testKeyPair(t)
	if err := (Connection{Endpoint: "agent.example.test:443", CertPEM: cert, KeyPEM: key}).Validate(); err != nil {
		t.Fatalf("system roots, valid pair: %v", err)
	}
	if err := (Connection{Endpoint: "agent.example.test:443", CertPEM: cert, KeyPEM: key, CAPEM: cert}).Validate(); err != nil {
		t.Fatalf("with a CA: %v", err)
	}
	for name, bad := range map[string]Connection{
		"no endpoint":  {CertPEM: cert, KeyPEM: key},
		"garbage cert": {Endpoint: "a:443", CertPEM: []byte("x"), KeyPEM: key},
		"mismatch":     {Endpoint: "a:443", CertPEM: cert, KeyPEM: []byte("x")},
		"garbage CA":   {Endpoint: "a:443", CertPEM: cert, KeyPEM: key, CAPEM: []byte("x")},
		"empty pair":   {Endpoint: "a:443"},
	} {
		if bad.Validate() == nil {
			t.Errorf("%s accepted", name)
		}
	}
	c, err := NewFromConnection(Connection{Endpoint: "agent.example.test:443", CertPEM: cert, KeyPEM: key}, "prod")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if c.Instance() != "prod" {
		t.Fatalf("instance = %q", c.Instance())
	}
}
