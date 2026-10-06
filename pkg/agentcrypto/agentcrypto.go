// Package agentcrypto holds the key material an infrastructure agent is enrolled with: the client
// key and certificate request for mutual TLS, the signing key pair of the lab access tokens, and the
// reading of an issued certificate. Private keys are generated here and never leave the platform:
// only the certificate request and the public access key go to the agent.
package agentcrypto

import (
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"time"
)

// ClientKey is a freshly generated mutual-TLS client key (ECDSA P-256) with its certificate request.
// The agent ignores the requested subject: the certificate CN is the tenant name.
type ClientKey struct {
	KeyPEM string
	CSRPEM string
}

// NewClientKey generates a client key and a PKCS#10 request for it.
func NewClientKey() (ClientKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return ClientKey{}, fmt.Errorf("generate client key: %w", err)
	}
	der, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return ClientKey{}, fmt.Errorf("encode client key: %w", err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{Subject: pkix.Name{CommonName: "platform"}}, key)
	if err != nil {
		return ClientKey{}, fmt.Errorf("create certificate request: %w", err)
	}
	return ClientKey{
		KeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: der})),
		CSRPEM: string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr})),
	}, nil
}

// AccessKey is an Ed25519 key pair that signs the lab access tokens of one tenant. The agent holds the
// public key, identified by ID (the token's kid).
type AccessKey struct {
	ID            string
	PrivateKeyPEM string // PKCS#8
	PublicKeyPEM  string // PKIX
}

// NewAccessKey generates a signing key pair with a random id of the form k-<16 hex>: 1-64 characters
// of A-Z a-z 0-9 . _ -, as the agent requires.
func NewAccessKey() (AccessKey, error) {
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return AccessKey{}, fmt.Errorf("generate access key: %w", err)
	}
	privDER, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return AccessKey{}, fmt.Errorf("encode access private key: %w", err)
	}
	pubDER, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		return AccessKey{}, fmt.Errorf("encode access public key: %w", err)
	}
	id := make([]byte, 8)
	if _, err = rand.Read(id); err != nil {
		return AccessKey{}, err
	}
	return AccessKey{
		ID:            "k-" + hex.EncodeToString(id),
		PrivateKeyPEM: string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privDER})),
		PublicKeyPEM:  string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: pubDER})),
	}, nil
}

// ParseAccessPrivateKey reads an Ed25519 PKCS#8 PEM private key.
func ParseAccessPrivateKey(privateKeyPEM string) (ed25519.PrivateKey, error) {
	block, _ := pem.Decode([]byte(privateKeyPEM))
	if block == nil {
		return nil, errors.New("access private key: no PEM block")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("access private key: %w", err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("access private key: not an Ed25519 key")
	}
	return key, nil
}

// Certificate is what is read from an issued client certificate.
type Certificate struct {
	// Tenant is the certificate subject CN: the tenant name, also the issuer of the access tokens.
	Tenant    string
	NotBefore time.Time
	NotAfter  time.Time
}

// ReadCertificate parses the first certificate of certPEM. The tenant is its subject CN, the one
// source of the tenant name.
func ReadCertificate(certPEM string) (Certificate, error) {
	rest := []byte(certPEM)
	for {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			return Certificate{}, errors.New("certificate: no CERTIFICATE block")
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return Certificate{}, fmt.Errorf("certificate: %w", err)
		}
		if cert.Subject.CommonName == "" {
			return Certificate{}, errors.New("certificate: the subject has no CN, so no tenant name")
		}
		return Certificate{Tenant: cert.Subject.CommonName, NotBefore: cert.NotBefore, NotAfter: cert.NotAfter}, nil
	}
}

// MatchesKey reports whether the certificate was issued for the private key.
func MatchesKey(certPEM, keyPEM string) error {
	if _, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM)); err != nil {
		return fmt.Errorf("the certificate does not match the client key: %w", err)
	}
	return nil
}
