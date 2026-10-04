package config

import (
	"os"
	"time"
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

// applyFixed sets the values that are no settings: internal mechanics, protocol constants and formats that
// never need to differ between deployments. They were once environment variables; the defaults they had are
// the constants here.
func (c *Config) applyFixed() {
	c.HTTPController.Server.Host = "0.0.0.0"
	c.HTTPController.Server.MaxHeaderMegabytes = 1

	// SUPPORT_EMAIL is the Reply-To of all mail; the admin mail settings override it.
	c.Infrastructure.SMTP.ReplyToName, c.Infrastructure.SMTP.ReplyToEmail = "", ""

	// The revoked-session poll: past this without a successful poll the replica refuses signed-in requests.
	c.Auth.SessionRevocationStaleAfter = 30 * time.Second

	// The resumable upload protocol: the clients rely on the chunk size.
	c.Media.UploadChunkBytes = MaxUploadChunkBytes
	c.Media.UploadTTL = 24 * time.Hour

	// The mail send limiter internals; the quota window is the provider's day, the caps are what an admin may
	// type for the provider limits (effectively unlimited).
	t := &c.Tunables
	t.MailMaxRateWait = 20 * time.Second
	t.MailQuotaRetryAfter = 10 * time.Minute
	t.MailQuotaRecheck = 30 * time.Second
	t.MailQuotaWindow = 24 * time.Hour
	t.MailMaxPerSecondLimit = 10000
	t.MailDailyQuotaLimit = 1_000_000_000

	// Mail-rendering format limits: what mail clients need.
	t.EmailImageUploadMaxBytes = 10 << 20
	t.EmailImageMaxBytes = 300 << 10
	t.EmailImageMaxWidth = 1200
	t.EmailImageMaxPixels = 24_000_000

	// Error journal mechanics.
	ej := &c.ErrorJournal
	ej.SamplesPerGroup = 5
	ej.BufferSize = 1024
	ej.NotFoundFlushInterval = 10 * time.Second
	ej.WatchInterval = time.Minute
}
