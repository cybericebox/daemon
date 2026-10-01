package agentcrypto

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"regexp"
	"testing"
	"time"
)

// issue signs the request like an agent would: a certificate with CN = the tenant for the CSR's key.
func issue(t *testing.T, csrPEM, tenant string, notAfter time.Time) string {
	t.Helper()
	block, _ := pem.Decode([]byte(csrPEM))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		t.Fatalf("the request is invalid: %v", err)
	}
	caPub, caPriv, _ := ed25519.GenerateKey(rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "agent-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter.Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	_ = caPub
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: tenant}, NotBefore: time.Now().Add(-time.Minute), NotAfter: notAfter, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, csr.PublicKey, caPriv)
	if err != nil {
		t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func TestClientKeyRequestIsValidAndTheIssuedCertificateGivesTheTenant(t *testing.T) {
	key, err := NewClientKey()
	if err != nil {
		t.Fatal(err)
	}
	notAfter := time.Now().Add(90 * 24 * time.Hour).UTC().Truncate(time.Second)
	cert := issue(t, key.CSRPEM, "platform", notAfter)
	got, err := ReadCertificate(cert)
	if err != nil || got.Tenant != "platform" || !got.NotAfter.Equal(notAfter) {
		t.Fatalf("certificate = %+v, %v", got, err)
	}
	if err = MatchesKey(cert, key.KeyPEM); err != nil {
		t.Fatalf("the certificate belongs to the key: %v", err)
	}
	other, _ := NewClientKey()
	if MatchesKey(cert, other.KeyPEM) == nil {
		t.Fatal("another key must not match")
	}
}

func TestReadCertificateRejectsGarbageAndEmptyCN(t *testing.T) {
	if _, err := ReadCertificate("nope"); err == nil {
		t.Fatal("garbage accepted")
	}
	key, _ := NewClientKey()
	block, _ := pem.Decode([]byte(key.CSRPEM))
	csr, _ := x509.ParseCertificateRequest(block.Bytes)
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(3), NotBefore: time.Now(), NotAfter: time.Now().Add(time.Hour)}
	der, _ := x509.CreateCertificate(rand.Reader, tpl, tpl, csr.PublicKey, priv)
	if _, err := ReadCertificate(string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))); err == nil {
		t.Fatal("a certificate without CN has no tenant")
	}
}

func TestAccessKeyIsEd25519WithAnAgentAcceptableID(t *testing.T) {
	k, err := NewAccessKey()
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`).MatchString(k.ID) {
		t.Fatalf("id %q is not acceptable to the agent", k.ID)
	}
	priv, err := ParseAccessPrivateKey(k.PrivateKeyPEM)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode([]byte(k.PublicKeyPEM))
	pub, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil || !priv.Public().(ed25519.PublicKey).Equal(pub) {
		t.Fatalf("the public key must belong to the private one: %v", err)
	}
	if _, err = ParseAccessPrivateKey("x"); err == nil {
		t.Fatal("garbage accepted")
	}
	other, _ := NewAccessKey()
	if other.ID == k.ID {
		t.Fatal("ids must be random")
	}
}
