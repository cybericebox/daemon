package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/pires/go-proxyproto"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/config"
)

// proxyStand is an HTTPS listener behind PROXY protocol with the real hardened router: /ip answers the client
// address the session would store.
type proxyStand struct {
	*tlsStand
}

func startProxied(t *testing.T, clientAuth string, from ...string) *proxyStand {
	t.Helper()
	gin.SetMode(gin.TestMode)
	st := &tlsStand{serverCA: newTestCA(t, "server-ca"), clientCA: newTestCA(t, "aop-ca"), dir: t.TempDir()}
	certPEM, keyPEM := st.serverCA.issue(t, "api.example.test", true)
	write(t, filepath.Join(st.dir, "tls.crt"), certPEM)
	write(t, filepath.Join(st.dir, "tls.key"), keyPEM)
	write(t, filepath.Join(st.dir, "ca.crt"), st.clientCA.pem)
	port := freePort(t)
	st.cfg = config.HTTPServerConfig{
		Host: "127.0.0.1", HTTPSPort: port, ReadTimeout: 3 * time.Second, WriteTimeout: 3 * time.Second, MaxHeaderMegabytes: 1,
		ProxyProtocolFrom: from,
		TLS: config.HTTPServerTLSConfig{
			ClientAuth: clientAuth, MinVersion: "1.2",
			CertFile: filepath.Join(st.dir, "tls.crt"), KeyFile: filepath.Join(st.dir, "tls.key"), ClientCAFile: filepath.Join(st.dir, "ca.crt"),
		},
	}
	router := gin.New()
	cfg := config.HTTPControllerConfig{MaxBodyBytes: 1 << 20, Server: st.cfg}
	require.NoError(t, hardenRouter(router, &cfg))
	router.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })

	srv := NewServer(&st.cfg, "127.0.0.1", router)
	srv.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Stop(ctx)
	})
	st.tlsAddr = "127.0.0.1:" + port
	waitListening(t, st.tlsAddr)
	return &proxyStand{st}
}

// ipOver dials, optionally sends a PROXY v2 header claiming source, runs TLS and returns what /ip answers.
func (s *proxyStand) ipOver(t *testing.T, source string, cert *tls.Certificate, headers map[string]string) (string, error) {
	t.Helper()
	conn, err := net.Dial("tcp", s.tlsAddr)
	require.NoError(t, err)
	defer conn.Close()
	if source != "" {
		src := net.ParseIP(source)
		header := &proxyproto.Header{
			Version: 2, Command: proxyproto.PROXY, TransportProtocol: proxyproto.TCPv4,
			SourceAddr:      &net.TCPAddr{IP: src, Port: 4242},
			DestinationAddr: &net.TCPAddr{IP: net.ParseIP("10.0.0.1"), Port: 443},
		}
		_, err = header.WriteTo(conn)
		require.NoError(t, err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(s.serverCA.pem)
	tlsCfg := &tls.Config{RootCAs: pool, ServerName: "localhost", NextProtos: []string{"http/1.1"}}
	if cert != nil {
		tlsCfg.Certificates = []tls.Certificate{*cert}
	}
	client := &http.Client{
		Transport: &http.Transport{
			DialTLSContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
				secured := tls.Client(conn, tlsCfg)
				return secured, secured.HandshakeContext(ctx)
			},
		},
		Timeout: 3 * time.Second,
	}
	req, _ := http.NewRequest(http.MethodGet, "https://localhost/ip", nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return strings.TrimSpace(string(body)), nil
}

func TestProxyProtocol_TrustedPeerHeaderSetsTheClientAddress(t *testing.T) {
	st := startProxied(t, "off", "127.0.0.1")
	got, err := st.ipOver(t, "198.51.100.7", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "198.51.100.7", got)
}

func TestProxyProtocol_UntrustedPeerHeaderIsIgnored(t *testing.T) {
	st := startProxied(t, "off", "192.0.2.0/24") // the test peer 127.0.0.1 is not in it
	got, err := st.ipOver(t, "198.51.100.7", nil, nil)
	require.NoError(t, err, "the header is consumed, the connection still works")
	assert.Equal(t, "127.0.0.1", got)
}

func TestProxyProtocol_TrustedPeerWithoutAHeaderKeepsThePeerAddress(t *testing.T) {
	st := startProxied(t, "off", "127.0.0.1")
	got, err := st.ipOver(t, "", nil, nil)
	require.NoError(t, err, "the header is optional, never required")
	assert.Equal(t, "127.0.0.1", got, "a private peer: the session stores no address")
}

func TestProxyProtocol_MTLSStillVerifiesBehindTheHeader(t *testing.T) {
	st := startProxied(t, "require", "127.0.0.1")
	_, err := st.ipOver(t, "198.51.100.7", nil, nil)
	require.Error(t, err, "no client certificate")
	_, err = st.ipOver(t, "198.51.100.7", clientCertFrom(t, newTestCA(t, "other-ca")), nil)
	require.Error(t, err, "a certificate from another CA")
	got, err := st.ipOver(t, "198.51.100.7", clientCertFrom(t, st.clientCA), nil)
	require.NoError(t, err)
	assert.Equal(t, "198.51.100.7", got)
}

func TestProxyProtocol_CloudflareHeaderStillWinsOnAVerifiedConnection(t *testing.T) {
	st := startProxied(t, "require", "127.0.0.1")
	cert := clientCertFrom(t, st.clientCA)
	got, err := st.ipOver(t, "104.16.0.1", cert, map[string]string{"CF-Connecting-IP": "203.0.113.9"})
	require.NoError(t, err)
	assert.Equal(t, "203.0.113.9", got, "behind Cloudflare the PROXY address is an edge address; the header overrides it")
}

func TestProxyProtocol_OffLeavesTheListenerAsItWas(t *testing.T) {
	st := startProxied(t, "off")
	got, err := st.ipOver(t, "", nil, nil)
	require.NoError(t, err)
	assert.Equal(t, "127.0.0.1", got)
	_, err = st.ipOver(t, "198.51.100.7", nil, nil)
	require.Error(t, err, "without the setting a PROXY header is not TLS and the handshake fails, as before")
}

func TestParseProxyProtocolFrom(t *testing.T) {
	got, err := config.ParseProxyProtocolFrom([]string{"10.64.3.2", " 10.64.3.0/24 ", "", "::ffff:10.0.0.1", "2001:db8::/32"})
	require.NoError(t, err)
	assert.Equal(t, []string{"10.64.3.2/32", "10.64.3.0/24", "10.0.0.1/32", "2001:db8::/32"}, prefixStrings(got))
	_, err = config.ParseProxyProtocolFrom([]string{"nope"})
	require.Error(t, err)
}

func prefixStrings[T interface{ String() string }](in []T) []string {
	out := make([]string, 0, len(in))
	for _, p := range in {
		out = append(out, p.String())
	}
	return out
}
