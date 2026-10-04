package config

import (
	"fmt"
	"os"
)

// The standard places a container finds its TLS material: the serving certificate and key, and the CA
// of the client certificates (the authenticated-origin-pull CA of the edge).
const (
	DefaultTLSCertFile  = "/tls/tls.crt"
	DefaultTLSKeyFile   = "/tls/tls.key"
	DefaultClientCAFile = "/aop/ca.crt"
)

// fileExists is replaced in tests.
var fileExists = func(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

// applyTLSDefaults turns TLS on at the standard paths when the settings are not given and the files exist.
// A value that is set in the environment (even an empty one) is never replaced.
func (s *HTTPServerConfig) applyTLSDefaults() {
	_, certSet := os.LookupEnv("HTTP_SERVER_TLS_CERT_FILE")
	_, keySet := os.LookupEnv("HTTP_SERVER_TLS_KEY_FILE")
	if !certSet && !keySet && fileExists(DefaultTLSCertFile) && fileExists(DefaultTLSKeyFile) {
		s.TLS.CertFile, s.TLS.KeyFile = DefaultTLSCertFile, DefaultTLSKeyFile
	}
	if !s.TLSEnabled() {
		return
	}
	_, caSet := os.LookupEnv("HTTP_SERVER_TLS_CLIENT_CA_FILE")
	if !caSet && fileExists(DefaultClientCAFile) {
		s.TLS.ClientCAFile = DefaultClientCAFile
		if _, authSet := os.LookupEnv("HTTP_SERVER_TLS_CLIENT_AUTH"); !authSet {
			s.TLS.ClientAuth = "require"
		}
	}
}

// ValidateFiles checks that the TLS files that are configured exist: a path given in the environment that
// points nowhere is an error (the deploy mounted it elsewhere, or not at all), never a silent plain listener.
// The client CA is needed whenever client certificates are asked for. Defaults found on disk always exist.
func (s HTTPServerConfig) ValidateFiles() error {
	for name, path := range map[string]string{"HTTP_SERVER_TLS_CERT_FILE": s.TLS.CertFile, "HTTP_SERVER_TLS_KEY_FILE": s.TLS.KeyFile} {
		if path != "" && !fileExists(path) {
			return fmt.Errorf("%s: no such file %q", name, path)
		}
	}
	if s.ClientAuthOn() && !fileExists(s.TLS.ClientCAFile) {
		return fmt.Errorf("HTTP_SERVER_TLS_CLIENT_AUTH=%s needs the client CA file, but %q does not exist (HTTP_SERVER_TLS_CLIENT_CA_FILE, default %s)",
			s.TLS.ClientAuth, s.TLS.ClientCAFile, DefaultClientCAFile)
	}
	return nil
}

// Mode names the active listener mode for the start-up log.
func (s HTTPServerConfig) Mode() string {
	switch {
	case s.ClientAuthOn():
		return "https+client-auth-" + s.TLS.ClientAuth
	case s.TLSEnabled():
		return "https"
	}
	return "http"
}
