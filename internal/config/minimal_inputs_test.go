package config

import (
	"os"
	"testing"
	"time"
)

// unsetenv removes name for the test and restores it afterwards.
func unsetenv(t *testing.T, name string) {
	t.Helper()
	old, had := os.LookupEnv(name)
	_ = os.Unsetenv(name)
	t.Cleanup(func() {
		if had {
			_ = os.Setenv(name, old)
		}
	})
}

func TestEnvironmentDefaultsToProduction(t *testing.T) {
	setTestHosts(t)
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	t.Setenv("JWT_TOKEN_SIGNATURE", "0123456789abcdef0123456789abcdef")
	unsetenv(t, "ENV")
	cfg := MustGetConfig()
	if cfg.Environment != Production {
		t.Fatalf("the default environment is production, got %q", cfg.Environment)
	}
	if cfg.HTTPController.EnableSwaggerDocs || cfg.Infrastructure.Postgres.MigrationsPath != "migrations" {
		t.Fatalf("production: swagger %v, migrations %q", cfg.HTTPController.EnableSwaggerDocs, cfg.Infrastructure.Postgres.MigrationsPath)
	}
}

func TestPostgresDatabaseDefaultIsTheDeployedOne(t *testing.T) {
	setTestHosts(t)
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	unsetenv(t, "POSTGRES_DB")
	if got := MustGetConfig().Infrastructure.Postgres.Database; got != "cybericebox" {
		t.Fatalf("database default = %q", got)
	}
}

// The knobs that were removed are constants now: the environment no longer reaches them.
func TestRemovedKnobsAreFixed(t *testing.T) {
	setTestHosts(t)
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	for _, name := range []string{
		"HTTP_SERVER_HOST", "HTTP_SERVER_MAX_HEADER_MB", "SMTP_REPLY_TO_NAME", "SMTP_REPLY_TO_EMAIL",
		"SESSION_REVOCATION_STALE_AFTER", "MEDIA_UPLOAD_CHUNK_BYTES", "MEDIA_UPLOAD_TTL", "CALENDAR_TEST_LAB_LEASE",
		"MAIL_MAX_RATE_WAIT", "MAIL_QUOTA_RETRY_AFTER", "MAIL_QUOTA_RECHECK", "MAIL_QUOTA_WINDOW",
		"MAIL_MAX_PER_SECOND_LIMIT", "MAIL_DAILY_QUOTA_LIMIT", "EMAIL_IMAGE_UPLOAD_MAX_BYTES", "EMAIL_IMAGE_MAX_BYTES",
		"EMAIL_IMAGE_MAX_WIDTH", "EMAIL_IMAGE_MAX_PIXELS", "ERROR_JOURNAL_SAMPLES_PER_GROUP", "ERROR_JOURNAL_BUFFER_SIZE",
		"ERROR_JOURNAL_NOT_FOUND_FLUSH_INTERVAL", "ERROR_JOURNAL_WATCH_INTERVAL",
	} {
		t.Setenv(name, "1")
	}
	cfg := MustGetConfig()
	if cfg.HTTPController.Server.Host != "0.0.0.0" || cfg.HTTPController.Server.MaxHeaderMegabytes != 1 ||
		cfg.Infrastructure.SMTP.ReplyToName != "" || cfg.Infrastructure.SMTP.ReplyToEmail != "" ||
		cfg.Auth.SessionRevocationStaleAfter != 30*time.Second ||
		cfg.Media.UploadChunkBytes != 50<<20 || cfg.Media.UploadTTL != 24*time.Hour {
		t.Fatalf("fixed values were overridden: %+v", cfg.HTTPController.Server)
	}
	tn := cfg.Tunables
	if tn.MailMaxRateWait != 20*time.Second || tn.MailQuotaRetryAfter != 10*time.Minute || tn.MailQuotaRecheck != 30*time.Second ||
		tn.MailQuotaWindow != 24*time.Hour || tn.MailMaxPerSecondLimit != 10000 || tn.MailDailyQuotaLimit != 1_000_000_000 ||
		tn.EmailImageUploadMaxBytes != 10<<20 || tn.EmailImageMaxBytes != 300<<10 || tn.EmailImageMaxWidth != 1200 ||
		tn.EmailImageMaxPixels != 24_000_000 {
		t.Fatalf("fixed tunables were overridden: %+v", tn)
	}
	ej := cfg.ErrorJournal
	if ej.SamplesPerGroup != 5 || ej.BufferSize != 1024 || ej.NotFoundFlushInterval != 10*time.Second || ej.WatchInterval != time.Minute {
		t.Fatalf("fixed error journal values were overridden: %+v", ej)
	}
}

func withFiles(t *testing.T, files ...string) {
	t.Helper()
	old := fileExists
	fileExists = func(path string) bool {
		for _, f := range files {
			if f == path {
				return true
			}
		}
		return false
	}
	t.Cleanup(func() { fileExists = old })
}

func TestTLSDefaultsFollowTheMountedFiles(t *testing.T) {
	setTestHosts(t)
	t.Setenv("RECAPTCHA_SECRET", "rsecret")
	for _, n := range []string{
		"HTTP_SERVER_PORT", "HTTP_SERVER_TLS_CERT_FILE", "HTTP_SERVER_TLS_KEY_FILE", "HTTP_SERVER_TLS_CLIENT_CA_FILE", "HTTP_SERVER_TLS_CLIENT_AUTH",
	} {
		unsetenv(t, n)
	}

	withFiles(t)
	s := MustGetConfig().HTTPController.Server
	if s.TLSEnabled() || s.ClientAuthOn() || s.Port != "8080" || s.HTTPSPort != "8443" {
		t.Fatalf("no files: TLS stays off and the plain listener is on 8080: %+v", s)
	}

	withFiles(t, DefaultTLSCertFile, DefaultTLSKeyFile)
	s = MustGetConfig().HTTPController.Server
	if !s.TLSEnabled() || s.TLS.CertFile != DefaultTLSCertFile || s.TLS.KeyFile != DefaultTLSKeyFile || s.ClientAuthOn() || s.Port != "" {
		t.Fatalf("certificate and key: TLS on, no client auth, no plain listener: %+v", s)
	}

	withFiles(t, DefaultTLSCertFile, DefaultTLSKeyFile, DefaultClientCAFile)
	s = MustGetConfig().HTTPController.Server
	if !s.ClientAuthOn() || s.TLS.ClientAuth != "require" || s.TLS.ClientCAFile != DefaultClientCAFile {
		t.Fatalf("with the CA the client certificate is required: %+v", s.TLS)
	}

	// What is set stays: an explicit empty cert turns TLS off, an explicit auth mode wins, an explicit port stays.
	t.Setenv("HTTP_SERVER_TLS_CLIENT_AUTH", "optional")
	t.Setenv("HTTP_SERVER_PORT", "9000")
	s = MustGetConfig().HTTPController.Server
	if s.TLS.ClientAuth != "optional" || s.Port != "9000" {
		t.Fatalf("explicit settings must win: %+v port=%s", s.TLS, s.Port)
	}
	t.Setenv("HTTP_SERVER_TLS_CERT_FILE", "")
	t.Setenv("HTTP_SERVER_TLS_KEY_FILE", "")
	t.Setenv("HTTP_SERVER_TLS_CLIENT_AUTH", "off")
	if s = MustGetConfig().HTTPController.Server; s.TLSEnabled() {
		t.Fatalf("an explicit empty certificate turns TLS off: %+v", s.TLS)
	}
}
