package labagent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"time"

	labclient "github.com/cybericebox/laboratory/pkg/agent/client"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// enrollTimeout bounds the enrollment call: one connection, one request.
const enrollTimeout = 30 * time.Second

// Enroll asks the agent at endpoint to issue a client certificate for the request and to trust the
// access public key, with the one-time token of the agent's tenant. It works over server-authenticated TLS
// only (the platform has no certificate yet): the agent is verified against caPEM, or the system roots
// when it is empty. A used, expired or unknown token is infraModel.ErrEnrollmentDenied. The returned
// certificate's CN is the tenant name.
func Enroll(ctx context.Context, endpoint string, caPEM []byte, token, csrPEM, accessPublicKeyPEM, accessKeyID string) (string, error) {
	var pool *x509.CertPool
	if len(caPEM) > 0 {
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(caPEM) {
			return "", errors.New("server CA: no certificate found")
		}
	}
	conn, err := grpc.NewClient(endpoint,
		grpc.WithTransportCredentials(credentials.NewTLS(&tls.Config{RootCAs: pool, MinVersion: tls.VersionTLS12})),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(labclient.MaxMessageSize), grpc.MaxCallSendMsgSize(labclient.MaxMessageSize)),
	)
	if err != nil {
		return "", fmt.Errorf("dial agent %q: %w", endpoint, err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(ctx, enrollTimeout)
	defer cancel()
	res, err := labpb.NewLabManagerClient(conn).Enroll(ctx, &labpb.EnrollRequest{
		Token: token, CsrPem: csrPEM, AccessPublicKeyPem: accessPublicKeyPEM, AccessKeyId: accessKeyID,
	})
	if err != nil {
		if status.Code(err) == codes.PermissionDenied {
			return "", fmt.Errorf("%w: %s", infraModel.ErrEnrollmentDenied, status.Convert(err).Message())
		}
		return "", fmt.Errorf("enroll: %w", err)
	}
	if res.GetCertificatePem() == "" {
		return "", errors.New("enroll: the agent returned no certificate")
	}
	return res.GetCertificatePem(), nil
}

// RenewCertificate gets a new client certificate (same CN) for a new key over the agent's mutual-TLS
// connection.
func (c *Client) RenewCertificate(ctx context.Context, csrPEM string) (string, error) {
	res, err := c.Client.RenewCertificate(ctx, &labpb.RenewCertificateRequest{CsrPem: csrPEM})
	if err != nil {
		return "", fmt.Errorf("renew certificate: %w", err)
	}
	if res.GetCertificatePem() == "" {
		return "", errors.New("renew certificate: the agent returned no certificate")
	}
	return res.GetCertificatePem(), nil
}

// RotateAccessKey adds an access public key to the tenant (both keys work until the old one is removed).
// The same id with the same key is accepted again.
func (c *Client) RotateAccessKey(ctx context.Context, keyID, publicKeyPEM string) error {
	if _, err := c.Client.RotateAccessKey(ctx, &labpb.RotateAccessKeyRequest{KeyId: keyID, PublicKeyPem: publicKeyPEM}); err != nil {
		return fmt.Errorf("rotate access key: %w", err)
	}
	return nil
}

// RemoveAccessKey removes an access key from the tenant. A key that is already gone is the desired state;
// the last key of a tenant cannot be removed.
func (c *Client) RemoveAccessKey(ctx context.Context, keyID string) error {
	if _, err := c.Client.RemoveAccessKey(ctx, &labpb.RemoveAccessKeyRequest{KeyId: keyID}); err != nil {
		if status.Code(err) == codes.NotFound {
			return nil
		}
		return fmt.Errorf("remove access key: %w", err)
	}
	return nil
}
