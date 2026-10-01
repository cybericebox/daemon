package labagent

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"testing"
	"time"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// enrollServer is an agent that only knows Enroll, over server-authenticated TLS.
type enrollServer struct {
	labpb.UnimplementedLabManagerServer
	got *labpb.EnrollRequest
}

func (s *enrollServer) Enroll(_ context.Context, in *labpb.EnrollRequest) (*labpb.CertificateResponse, error) {
	s.got = in
	if in.GetToken() != "good-token" {
		return nil, status.Error(codes.PermissionDenied, "the enrollment token is not valid")
	}
	if in.GetCsrPem() == "" {
		return nil, status.Error(codes.InvalidArgument, "no request")
	}
	return &labpb.CertificateResponse{CertificatePem: "-----BEGIN CERTIFICATE-----\nissued\n-----END CERTIFICATE-----\n", NotAfterUnix: time.Now().Add(720 * time.Hour).Unix()}, nil
}

// startAgent serves with a certificate for 127.0.0.1 signed by a fresh CA; it returns the endpoint and the CA PEM.
func startAgent(t *testing.T, srv labpb.LabManagerServer) (endpoint string, caPEM []byte) {
	t.Helper()
	caKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	caTpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign}
	caDER, err := x509.CreateCertificate(rand.Reader, caTpl, caTpl, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caCert, _ := x509.ParseCertificate(caDER)
	leafKey, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leafTpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "agent"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTpl, caCert, &leafKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer(grpc.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{leafDER}, PrivateKey: leafKey}}, MinVersion: tls.VersionTLS12})))
	labpb.RegisterLabManagerServer(server, srv)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	return listener.Addr().String(), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
}

func TestEnrollSendsOnlyTheRequestAndThePublicKeyOverServerAuthTLS(t *testing.T) {
	agent := &enrollServer{}
	endpoint, ca := startAgent(t, agent)
	cert, err := Enroll(context.Background(), endpoint, ca, "good-token", "CSR", "PUBKEY", "k-1")
	if err != nil || cert == "" {
		t.Fatalf("enroll = %q, %v", cert, err)
	}
	if agent.got.GetToken() != "good-token" || agent.got.GetCsrPem() != "CSR" || agent.got.GetAccessPublicKeyPem() != "PUBKEY" || agent.got.GetAccessKeyId() != "k-1" {
		t.Fatalf("agent saw %+v", agent.got)
	}
}

func TestEnrollMapsADeniedTokenAndRefusesAnUntrustedServer(t *testing.T) {
	endpoint, ca := startAgent(t, &enrollServer{})
	if _, err := Enroll(context.Background(), endpoint, ca, "used-token", "CSR", "PUB", "k"); !errors.Is(err, infraModel.ErrEnrollmentDenied) {
		t.Fatalf("a refused token = %v, want ErrEnrollmentDenied", err)
	}
	// Without the CA the server certificate is not trusted: that is a failure, not a denied token.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := Enroll(ctx, endpoint, nil, "good-token", "CSR", "PUB", "k"); err == nil || errors.Is(err, infraModel.ErrEnrollmentDenied) {
		t.Fatalf("an untrusted server certificate = %v", err)
	}
	if _, err := Enroll(context.Background(), endpoint, []byte("junk"), "t", "CSR", "PUB", "k"); err == nil {
		t.Fatal("a junk CA must be refused")
	}
}
