package http

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/quic-go/quic-go/http3"

	"github.com/cybericebox/daemon/internal/config"
)

// startH3 runs a Server with a self-signed certificate; the chain is a middleware that stamps a header and a
// route that answers 429, standing in for the limiters.
func startH3(t *testing.T, enabled bool) (port string, pool *x509.CertPool) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	ca := newTestCA(t, "server-ca")
	dir := t.TempDir()
	certPEM, keyPEM := ca.issue(t, "api.example.test", true)
	write(t, filepath.Join(dir, "tls.crt"), certPEM)
	write(t, filepath.Join(dir, "tls.key"), keyPEM)
	pool = x509.NewCertPool()
	pool.AddCert(ca.cert)

	port = freePort(t)
	cfg := config.HTTPServerConfig{
		Host: "127.0.0.1", HTTPSPort: port,
		ReadTimeout: 5 * time.Second, WriteTimeout: 5 * time.Second, IdleTimeout: 30 * time.Second, MaxHeaderMegabytes: 1,
		TLS:   config.HTTPServerTLSConfig{ClientAuth: "off", MinVersion: "1.2", CertFile: filepath.Join(dir, "tls.crt"), KeyFile: filepath.Join(dir, "tls.key")},
		HTTP3: config.HTTP3Config{Enabled: enabled, AdvertisedPort: "443"},
	}
	router := gin.New()
	router.Use(func(c *gin.Context) { c.Header("X-Chain", "applied") })
	router.GET("/limited", func(c *gin.Context) { c.Status(http.StatusTooManyRequests) })
	RegisterHealth(router)

	srv := NewServer(&cfg, "127.0.0.1", router)
	srv.Start()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Stop(ctx)
	})
	waitListening(t, "127.0.0.1:"+port)
	return port, pool
}

func TestHTTP3_ServesTheSameChain(t *testing.T) {
	port, pool := startH3(t, true)
	tr := &http3.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost"}}
	defer tr.Close()
	client := &http.Client{Transport: tr, Timeout: 5 * time.Second}

	var resp *http.Response
	var err error
	for range 20 { // the UDP socket opens a moment after the TCP one
		if resp, err = client.Get("https://127.0.0.1:" + port + "/api/health"); err == nil {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.ProtoMajor != 3 || resp.StatusCode != http.StatusOK {
		t.Fatalf("proto %d status %d, want HTTP/3 200", resp.ProtoMajor, resp.StatusCode)
	}
	if resp.Header.Get("X-Chain") != "applied" {
		t.Fatal("the middleware chain did not run over HTTP/3")
	}

	limited, err := client.Get("https://127.0.0.1:" + port + "/limited")
	if err != nil {
		t.Fatal(err)
	}
	defer limited.Body.Close()
	if limited.StatusCode != http.StatusTooManyRequests || limited.Header.Get("X-Chain") != "applied" {
		t.Fatalf("limited route: status %d chain %q", limited.StatusCode, limited.Header.Get("X-Chain"))
	}
}

func TestHTTP3_AltSvcOnlyWhenEnabled(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		port, pool := startH3(t, enabled)
		client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "localhost"}}, Timeout: 5 * time.Second}
		resp, err := client.Get("https://127.0.0.1:" + port + "/api/health")
		if err != nil {
			t.Fatal(err)
		}
		got := resp.Header.Get("Alt-Svc")
		resp.Body.Close()
		if enabled && got != `h3=":443"; ma=86400` {
			t.Fatalf("Alt-Svc = %q, want the h3 advertisement", got)
		}
		if !enabled && got != "" {
			t.Fatalf("Alt-Svc = %q with HTTP/3 off, want none", got)
		}
	}
}
