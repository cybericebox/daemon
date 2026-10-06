package labagent

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"

	labclient "github.com/cybericebox/laboratory/pkg/agent/client"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// Connection describes an agent whose mutual-TLS material is held in memory (the admin-configured
// agents keep it encrypted in the database), unlike the files of the environment-configured one.
type Connection struct {
	// Endpoint is host:443 of the agent. Dial it by its certificate hostname.
	Endpoint string
	// CertPEM and KeyPEM are the client certificate and its key.
	CertPEM, KeyPEM []byte
	// CAPEM verifies the agent's server certificate; empty means the system roots (a publicly
	// trusted certificate), so it is needed for self-signed development stands only.
	CAPEM []byte
}

// Validate checks the material parses, without dialing.
func (c Connection) Validate() error {
	_, err := c.transport()
	return err
}

func (c Connection) transport() (credentials.TransportCredentials, error) {
	if c.Endpoint == "" {
		return nil, errors.New("agent endpoint is empty")
	}
	pair, err := tls.X509KeyPair(c.CertPEM, c.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("client certificate and key: %w", err)
	}
	var pool *x509.CertPool
	if len(c.CAPEM) > 0 {
		pool = x509.NewCertPool()
		if !pool.AppendCertsFromPEM(c.CAPEM) {
			return nil, errors.New("server CA: no certificate found")
		}
	}
	return credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{pair}, RootCAs: pool, MinVersion: tls.VersionTLS12}), nil
}

type pemClient struct {
	labpb.LabManagerClient
	conn *grpc.ClientConn
}

func (c *pemClient) Close() error { return c.conn.Close() }

// NewFromConnection dials an agent from in-memory mutual-TLS material. The connection is lazy: the
// first call establishes it. instance is the platform-instance label value.
func NewFromConnection(c Connection, instance string) (*Client, error) {
	creds, err := c.transport()
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(c.Endpoint,
		grpc.WithTransportCredentials(creds),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(labclient.MaxMessageSize), grpc.MaxCallSendMsgSize(labclient.MaxMessageSize)),
	)
	if err != nil {
		return nil, fmt.Errorf("dial agent %q: %w", c.Endpoint, err)
	}
	return &Client{Client: &pemClient{LabManagerClient: labpb.NewLabManagerClient(conn), conn: conn}, instance: instance}, nil
}
