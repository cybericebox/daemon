package config

import "testing"

func TestHTTPServerConfig_DefaultsAreOff(t *testing.T) {
	setTestHosts(t)
	t.Setenv("RECAPTCHA_SECRET", "r")
	s := MustGetConfig().HTTPController

	if s.Server.TLS.Enabled || s.Server.TLS.ClientAuth {
		t.Fatalf("TLS and client auth are off by default: %+v", s.Server.TLS)
	}
	if s.Server.TLSPort != "8443" || s.Server.HealthPort != "8081" || s.HealthBind != "0.0.0.0" {
		t.Fatalf("ports: tls=%s health=%s bind=%s", s.Server.TLSPort, s.Server.HealthPort, s.HealthBind)
	}
	if s.Server.InternalPort != "" {
		t.Fatalf("the internal listener is off by default: %q", s.Server.InternalPort)
	}
	if s.Server.TLS.CertFile != "/certificates/tls.crt" || s.Server.TLS.KeyFile != "/certificates/tls.key" {
		t.Fatalf("cert/key defaults: %+v", s.Server.TLS)
	}
	if s.Server.TLS.CAFile != "/aop/ca.crt" {
		t.Fatalf("the client CA defaults to the origin-pull bundle: %q", s.Server.TLS.CAFile)
	}
}

func TestHTTPServerConfig_ParsedFromEnv(t *testing.T) {
	setTestHosts(t)
	t.Setenv("RECAPTCHA_SECRET", "r")
	t.Setenv("HTTP_SERVER_TLS_ENABLED", "true")
	t.Setenv("HTTP_SERVER_TLS_CLIENT_AUTH", "true")
	t.Setenv("HTTP_SERVER_TLS_CERT_FILE", "/tls/tls.crt")
	t.Setenv("HTTP_SERVER_TLS_KEY_FILE", "/tls/tls.key")
	t.Setenv("HTTP_SERVER_TLS_CA_FILE", "/x/ca.pem")
	t.Setenv("HTTP_SERVER_TLS_PORT", "9443")
	t.Setenv("HTTP_SERVER_HEALTH_PORT", "9081")
	t.Setenv("HTTP_SERVER_INTERNAL_PORT", "8082")
	t.Setenv("HEALTH_BIND", "10.1.2.3")
	s := MustGetConfig().HTTPController

	if !s.Server.TLS.Enabled || !s.Server.TLS.ClientAuth || s.Server.TLS.CertFile != "/tls/tls.crt" || s.Server.TLS.KeyFile != "/tls/tls.key" || s.Server.TLS.CAFile != "/x/ca.pem" {
		t.Fatalf("tls: %+v", s.Server.TLS)
	}
	if s.Server.TLSPort != "9443" || s.Server.HealthPort != "9081" || s.Server.InternalPort != "8082" || s.HealthBind != "10.1.2.3" {
		t.Fatalf("ports: %+v bind=%s", s.Server, s.HealthBind)
	}
}

func TestHTTPControllerConfig_ClientAuthNeedsTLS(t *testing.T) {
	c := HTTPControllerConfig{MaxBodyBytes: 1}
	c.Server.TLS.ClientAuth = true
	if err := c.Validate(); err == nil {
		t.Fatal("client auth without TLS must be rejected")
	}
	c.Server.TLS.Enabled = true
	if err := c.Validate(); err == nil {
		t.Fatal("client auth without a CA file must be rejected")
	}
	c.Server.TLS.CAFile = "/aop/ca.crt"
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
}
