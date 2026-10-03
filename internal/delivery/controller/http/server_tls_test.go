package http

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
)

type testCA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
	pem  []byte
}

var serial int64 = 100

func newTestCA(t *testing.T, name string) *testCA {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial++
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	return &testCA{cert: cert, key: key, pem: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}
}

// issue signs a leaf; server leaves get the localhost names, client leaves the client-auth usage.
func (ca *testCA) issue(t *testing.T, cn string, server bool) (certPEM, keyPEM []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial++
	tpl := &x509.Certificate{
		SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: cn},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
	}
	if server {
		tpl.DNSNames, tpl.IPAddresses = []string{"localhost"}, []net.IP{net.ParseIP("127.0.0.1")}
		tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
	} else {
		tpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
}

func freePort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	_, port, _ := net.SplitHostPort(ln.Addr().String())
	return port
}

func write(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

type tlsStand struct {
	cfg        config.HTTPServerConfig
	serverCA   *testCA
	clientCA   *testCA
	dir        string
	tlsAddr    string
	healthAddr string
}

// startTLS runs a Server in TLS mode on free ports; the handler echoes the client address and serves /api/health.
func startTLS(t *testing.T, clientAuth bool) *tlsStand {
	t.Helper()
	gin.SetMode(gin.TestMode)
	st := &tlsStand{serverCA: newTestCA(t, "server-ca"), clientCA: newTestCA(t, "aop-ca"), dir: t.TempDir()}
	certPEM, keyPEM := st.serverCA.issue(t, "api.example.test", true)
	write(t, filepath.Join(st.dir, "tls.crt"), certPEM)
	write(t, filepath.Join(st.dir, "tls.key"), keyPEM)
	write(t, filepath.Join(st.dir, "ca.crt"), st.clientCA.pem)

	tlsPort, healthPort := freePort(t), freePort(t)
	st.cfg = config.HTTPServerConfig{
		Host: "127.0.0.1", Port: "1", TLSPort: tlsPort, HealthPort: healthPort,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, MaxHeaderMegabytes: 1,
		TLS: config.TLSConfig{
			Enabled: true, ClientAuth: clientAuth,
			CertFile: filepath.Join(st.dir, "tls.crt"), KeyFile: filepath.Join(st.dir, "tls.key"), CAFile: filepath.Join(st.dir, "ca.crt"),
		},
	}
	router := gin.New()
	router.GET("/secret", func(c *gin.Context) { c.String(http.StatusOK, "secret "+c.Request.Proto) })
	RegisterHealth(router)

	srv := NewServer(&st.cfg, "127.0.0.1", router)
	srv.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Stop(ctx)
	})
	st.tlsAddr, st.healthAddr = "127.0.0.1:"+tlsPort, "127.0.0.1:"+healthPort
	waitListening(t, st.tlsAddr)
	waitListening(t, st.healthAddr)
	return st
}

func waitListening(t *testing.T, addr string) {
	t.Helper()
	for i := 0; i < 100; i++ {
		if c, err := net.DialTimeout("tcp", addr, 100*time.Millisecond); err == nil {
			_ = c.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("%s is not listening", addr)
}

func (st *tlsStand) client(clientCert *tls.Certificate) *http.Client {
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(st.serverCA.pem)
	cfg := &tls.Config{RootCAs: pool, ServerName: "localhost"}
	if clientCert != nil {
		cfg.Certificates = []tls.Certificate{*clientCert}
	}
	return &http.Client{Transport: &http.Transport{TLSClientConfig: cfg, ForceAttemptHTTP2: true}, Timeout: 3 * time.Second}
}

func clientCertFrom(t *testing.T, ca *testCA) *tls.Certificate {
	t.Helper()
	c, k := ca.issue(t, "cloudflare", false)
	pair, err := tls.X509KeyPair(c, k)
	if err != nil {
		t.Fatal(err)
	}
	return &pair
}

func get(t *testing.T, c *http.Client, url string) (int, string, error) {
	t.Helper()
	resp, err := c.Get(url)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body), nil
}

func TestMTLSRefusesAMissingClientCertificate(t *testing.T) {
	st := startTLS(t, true)
	if _, _, err := get(t, st.client(nil), "https://"+st.tlsAddr+"/secret"); err == nil {
		t.Fatal("a client without a certificate must be refused")
	}
}

func TestMTLSRefusesAClientCertificateFromAnotherCA(t *testing.T) {
	st := startTLS(t, true)
	other := newTestCA(t, "other-ca")
	if _, _, err := get(t, st.client(clientCertFrom(t, other)), "https://"+st.tlsAddr+"/secret"); err == nil {
		t.Fatal("a certificate from another CA must be refused")
	}
}

func TestMTLSAcceptsAValidClientCertificateOverHTTP2(t *testing.T) {
	st := startTLS(t, true)
	code, body, err := get(t, st.client(clientCertFrom(t, st.clientCA)), "https://"+st.tlsAddr+"/secret")
	if err != nil || code != http.StatusOK {
		t.Fatalf("valid certificate: %d %v", code, err)
	}
	if body != "secret HTTP/2.0" {
		t.Fatalf("HTTP/2 must be on: %q", body)
	}
}

func TestTLSWithoutClientAuthNeedsNoClientCertificate(t *testing.T) {
	st := startTLS(t, false)
	if code, _, err := get(t, st.client(nil), "https://"+st.tlsAddr+"/secret"); err != nil || code != http.StatusOK {
		t.Fatalf("client auth off: %d %v", code, err)
	}
}

func TestTLSRefusesOldProtocolVersions(t *testing.T) {
	st := startTLS(t, false)
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(st.serverCA.pem)
	_, err := tls.Dial("tcp", st.tlsAddr, &tls.Config{RootCAs: pool, ServerName: "localhost", MaxVersion: tls.VersionTLS11})
	if err == nil {
		t.Fatal("TLS 1.1 must be refused")
	}
}

func TestHealthListenerServesOnlyHealthInPlainHTTP(t *testing.T) {
	st := startTLS(t, true)
	plain := &http.Client{Timeout: 3 * time.Second}
	if code, _, err := get(t, plain, "http://"+st.healthAddr+"/api/health"); err != nil || code != http.StatusOK {
		t.Fatalf("health: %d %v", code, err)
	}
	if code, _, err := get(t, plain, "http://"+st.healthAddr+"/secret"); err != nil || code != http.StatusNotFound {
		t.Fatalf("everything else is 404 on the health port: %d %v", code, err)
	}
}

func TestPlainModeServesOnePortAndNoHealthListener(t *testing.T) {
	gin.SetMode(gin.TestMode)
	port := freePort(t)
	cfg := config.HTTPServerConfig{Host: "127.0.0.1", Port: port, TLSPort: freePort(t), HealthPort: freePort(t), ReadTimeout: time.Second, WriteTimeout: time.Second, MaxHeaderMegabytes: 1}
	router := gin.New()
	router.GET("/secret", func(c *gin.Context) { c.String(http.StatusOK, "plain") })
	srv := NewServer(&cfg, "127.0.0.1", router)
	srv.Start()
	t.Cleanup(func() { srv.Stop(context.Background()) })
	waitListening(t, "127.0.0.1:"+port)
	if code, body, err := get(t, http.DefaultClient, "http://127.0.0.1:"+port+"/secret"); err != nil || code != 200 || body != "plain" {
		t.Fatalf("plain: %d %q %v", code, body, err)
	}
	if c, err := net.DialTimeout("tcp", "127.0.0.1:"+cfg.HealthPort, 200*time.Millisecond); err == nil {
		_ = c.Close()
		t.Fatal("no health listener in plain mode")
	}
}

func TestInternalListenerServesTheSameHandlerInPlainHTTP(t *testing.T) {
	gin.SetMode(gin.TestMode)
	port := freePort(t)
	cfg := config.HTTPServerConfig{Host: "127.0.0.1", Port: freePort(t), InternalPort: port, ReadTimeout: time.Second, WriteTimeout: time.Second, MaxHeaderMegabytes: 1}
	router := gin.New()
	router.GET("/secret", func(c *gin.Context) { c.String(http.StatusOK, "api") })
	srv := NewServer(&cfg, "127.0.0.1", router)
	srv.Start()
	t.Cleanup(func() { srv.Stop(context.Background()) })
	waitListening(t, "127.0.0.1:"+port)
	if code, body, err := get(t, http.DefaultClient, "http://127.0.0.1:"+port+"/secret"); err != nil || code != 200 || body != "api" {
		t.Fatalf("internal: %d %q %v", code, body, err)
	}
}

func TestCertificateIsReloadedWithoutARestart(t *testing.T) {
	st := startTLS(t, false)
	serverNames := func() string {
		conn, err := tls.Dial("tcp", st.tlsAddr, &tls.Config{InsecureSkipVerify: true})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		return conn.ConnectionState().PeerCertificates[0].Subject.CommonName
	}
	if got := serverNames(); got != "api.example.test" {
		t.Fatalf("first: %s", got)
	}
	certPEM, keyPEM := st.serverCA.issue(t, "renewed.example.test", true)
	write(t, st.cfg.TLS.CertFile, certPEM)
	write(t, st.cfg.TLS.KeyFile, keyPEM)
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(st.cfg.TLS.CertFile, later, later)
	_ = os.Chtimes(st.cfg.TLS.KeyFile, later, later)
	if got := serverNames(); got != "renewed.example.test" {
		t.Fatalf("after renewal: %s", got)
	}
}

func TestClientCABundleIsReloadedWithoutARestart(t *testing.T) {
	st := startTLS(t, true)
	newCA := newTestCA(t, "rotated-aop")
	cert := clientCertFrom(t, newCA)
	if _, _, err := get(t, st.client(cert), "https://"+st.tlsAddr+"/secret"); err == nil {
		t.Fatal("not trusted yet")
	}
	write(t, st.cfg.TLS.CAFile, append(append([]byte{}, st.clientCA.pem...), newCA.pem...))
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(st.cfg.TLS.CAFile, later, later)
	if code, _, err := get(t, st.client(cert), "https://"+st.tlsAddr+"/secret"); err != nil || code != http.StatusOK {
		t.Fatalf("after the CA bundle changed: %d %v", code, err)
	}
}

func TestRenewalKeepsServingTheOldCertificateWhenTheNewFilesAreBroken(t *testing.T) {
	certFile, keyFile := writeKeyPair(t, t.TempDir())
	r := newCertReloader(certFile, keyFile)
	if _, err := r.GetCertificate(nil); err != nil {
		t.Fatal(err)
	}
	write(t, certFile, []byte("half written"))
	later := time.Now().Add(time.Minute)
	_ = os.Chtimes(certFile, later, later)
	if c, err := r.GetCertificate(nil); err != nil || c == nil {
		t.Fatalf("the old certificate stays: %v", err)
	}
}

// Client address selection.

func originRouter(clientAuth bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	cfg := config.HTTPControllerConfig{MaxBodyBytes: 1 << 20}
	cfg.Server.TLS.Enabled, cfg.Server.TLS.ClientAuth = clientAuth, clientAuth
	if err := hardenRouter(r, &cfg); err != nil {
		panic(err)
	}
	r.GET("/ip", func(c *gin.Context) { c.String(http.StatusOK, c.ClientIP()) })
	return r
}

func ipVia(r *gin.Engine, remote, cf string, state *tls.ConnectionState) string {
	req := httptest.NewRequest(http.MethodGet, "/ip", nil)
	req.RemoteAddr = remote
	if cf != "" {
		req.Header.Set("CF-Connecting-IP", cf)
	}
	req.TLS = state
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Body.String()
}

func verified() *tls.ConnectionState {
	return &tls.ConnectionState{VerifiedChains: [][]*x509.Certificate{{{}}}}
}

func TestClientIPComesFromCloudflareOnAVerifiedMTLSConnection(t *testing.T) {
	r := originRouter(true)
	for _, tc := range []struct{ name, cf, want string }{
		{"v4", "198.51.100.7", "198.51.100.7"},
		{"v6", "2001:db8::1", "2001:db8::1"},
		{"missing falls back to the peer", "", "10.0.0.5"},
		{"garbage falls back to the peer", "not-an-ip", "10.0.0.5"},
		{"a list falls back to the peer", "1.2.3.4, 5.6.7.8", "10.0.0.5"},
	} {
		if got := ipVia(r, "10.0.0.5:4000", tc.cf, verified()); got != tc.want {
			t.Errorf("%s: got %s want %s", tc.name, got, tc.want)
		}
	}
}

func TestCloudflareHeaderIsNeverTrustedOnAnUnverifiedConnection(t *testing.T) {
	// mTLS on, but the connection is plain (the internal listener) or TLS without a verified chain.
	r := originRouter(true)
	if got := ipVia(r, "10.0.0.5:4000", "198.51.100.7", nil); got != "10.0.0.5" {
		t.Fatalf("plain: %s", got)
	}
	if got := ipVia(r, "10.0.0.5:4000", "198.51.100.7", &tls.ConnectionState{}); got != "10.0.0.5" {
		t.Fatalf("unverified TLS: %s", got)
	}
	// mTLS off: the header is ignored even on a TLS connection that happens to carry a chain.
	off := originRouter(false)
	if got := ipVia(off, "10.0.0.5:4000", "198.51.100.7", verified()); got != "10.0.0.5" {
		t.Fatalf("mTLS off: %s", got)
	}
}
