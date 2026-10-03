package config

import (
	"strings"
	"testing"
)

func TestHTTPServerConfig_DefaultsArePlainOnly(t *testing.T) {
	setTestHosts(t)
	t.Setenv("RECAPTCHA_SECRET", "r")
	s := MustGetConfig().HTTPController

	if s.Server.TLSEnabled() || s.Server.ClientAuthOn() || s.Server.TLS.ClientAuth != "off" {
		t.Fatalf("TLS and client auth are off by default: %+v", s.Server.TLS)
	}
	if s.Server.Port == "" || s.Server.HTTPSPort != "8443" || s.Server.HealthPort != "" || s.HealthBind != "0.0.0.0" {
		t.Fatalf("ports: http=%s https=%s health=%s bind=%s", s.Server.Port, s.Server.HTTPSPort, s.Server.HealthPort, s.HealthBind)
	}
	if s.Server.InternalPort != "" {
		t.Fatalf("the internal listener is off by default: %q", s.Server.InternalPort)
	}
	if s.Server.TLS.CertFile != "" || s.Server.TLS.KeyFile != "" || s.Server.TLS.ClientCAFile != "" || s.Server.TLS.MinVersion != "1.2" {
		t.Fatalf("tls defaults: %+v", s.Server.TLS)
	}
}

func TestHTTPServerConfig_ParsedFromEnv(t *testing.T) {
	setTestHosts(t)
	t.Setenv("RECAPTCHA_SECRET", "r")
	t.Setenv("HTTP_SERVER_PORT", "")
	t.Setenv("HTTP_SERVER_HTTPS_PORT", "9443")
	t.Setenv("HTTP_SERVER_TLS_CERT_FILE", "/tls/tls.crt")
	t.Setenv("HTTP_SERVER_TLS_KEY_FILE", "/tls/tls.key")
	t.Setenv("HTTP_SERVER_TLS_MIN_VERSION", "1.3")
	t.Setenv("HTTP_SERVER_TLS_CLIENT_CA_FILE", "/x/ca.pem")
	t.Setenv("HTTP_SERVER_TLS_CLIENT_AUTH", "optional")
	t.Setenv("HTTP_SERVER_HEALTH_PORT", "9081")
	t.Setenv("HTTP_SERVER_INTERNAL_PORT", "8082")
	t.Setenv("HEALTH_BIND", "10.1.2.3")
	s := MustGetConfig().HTTPController

	if !s.Server.TLSEnabled() || !s.Server.ClientAuthOn() || s.Server.TLS.MinVersion != "1.3" || s.Server.TLS.ClientCAFile != "/x/ca.pem" || s.Server.TLS.CertFile != "/tls/tls.crt" || s.Server.TLS.KeyFile != "/tls/tls.key" {
		t.Fatalf("tls: %+v", s.Server.TLS)
	}
	if s.Server.Port != "" {
		t.Fatalf("an empty HTTP_SERVER_PORT turns the plain listener off: %q", s.Server.Port)
	}
	if s.Server.HTTPSPort != "9443" || s.Server.HealthPort != "9081" || s.Server.InternalPort != "8082" || s.HealthBind != "10.1.2.3" {
		t.Fatalf("ports: %+v bind=%s", s.Server, s.HealthBind)
	}
}

func TestHTTPControllerConfig_Validate(t *testing.T) {
	base := func() HTTPControllerConfig {
		c := HTTPControllerConfig{MaxBodyBytes: 1}
		c.Server.Port = "80"
		c.Server.TLS.MinVersion, c.Server.TLS.ClientAuth = "1.2", "off"
		return c
	}
	withTLS := func(c *HTTPControllerConfig) { c.Server.TLS.CertFile, c.Server.TLS.KeyFile = "/c", "/k" }
	cases := []struct {
		name string
		mut  func(*HTTPControllerConfig)
		want string // empty = valid
	}{
		{"plain only", func(*HTTPControllerConfig) {}, ""},
		{"tls only", func(c *HTTPControllerConfig) { withTLS(c); c.Server.Port = "" }, ""},
		{"cert without key", func(c *HTTPControllerConfig) { c.Server.TLS.CertFile = "/c" }, "set together"},
		{"key without cert", func(c *HTTPControllerConfig) { c.Server.TLS.KeyFile = "/k" }, "set together"},
		{"require without CA", func(c *HTTPControllerConfig) { withTLS(c); c.Server.TLS.ClientAuth = "require" }, "CLIENT_CA_FILE"},
		{"optional without CA", func(c *HTTPControllerConfig) { withTLS(c); c.Server.TLS.ClientAuth = "optional" }, "CLIENT_CA_FILE"},
		{"require with CA", func(c *HTTPControllerConfig) {
			withTLS(c)
			c.Server.TLS.ClientAuth, c.Server.TLS.ClientCAFile = "require", "/ca"
		}, ""},
		{"client auth without TLS", func(c *HTTPControllerConfig) { c.Server.TLS.ClientAuth, c.Server.TLS.ClientCAFile = "require", "/ca" }, "CERT_FILE"},
		{"unknown client auth", func(c *HTTPControllerConfig) { c.Server.TLS.ClientAuth = "maybe" }, "off, optional or require"},
		{"bad min version", func(c *HTTPControllerConfig) { c.Server.TLS.MinVersion = "1.1" }, "MIN_VERSION"},
		{"both listeners off", func(c *HTTPControllerConfig) { c.Server.Port = "" }, "no listener"},
	}
	for _, tc := range cases {
		c := base()
		tc.mut(&c)
		err := c.Validate()
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: unexpected error %v", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: want error containing %q, got %v", tc.name, tc.want, err)
		}
	}
}
