package protection

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cybericebox/daemon/internal/config"
)

// The secret must travel in the POST body: a failed request is logged with its URL.
func TestTheRecaptchaSecretIsSentInTheBodyNeverInTheURL(t *testing.T) {
	var gotURL, gotBody, gotType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotURL, gotType = r.URL.String(), r.Header.Get("Content-Type")
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		_, _ = w.Write([]byte(`{"success":true,"score":0.9,"action":"signin"}`))
	}))
	defer srv.Close()
	prev := siteVerifyURL
	siteVerifyURL = srv.URL + "/siteverify"
	defer func() { siteVerifyURL = prev }()

	p := &Protection{recaptcha: config.RecaptchaConfig{SecretKey: "S3CRET-KEY", Score: 0.5}}
	if err := p.verifyRecaptchaToken(context.Background(), "client-token", "signin"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(gotURL, "S3CRET-KEY") || strings.Contains(gotURL, "?") {
		t.Fatalf("the URL carries a secret: %s", gotURL)
	}
	if !strings.Contains(gotBody, "secret=S3CRET-KEY") || !strings.Contains(gotBody, "response=client-token") || gotType != "application/x-www-form-urlencoded" {
		t.Fatalf("body %q type %q", gotBody, gotType)
	}

	// A network failure is logged by the caller with its error: that text must hold no secret either.
	siteVerifyURL = "http://127.0.0.1:1/siteverify"
	err := p.verifyRecaptchaToken(context.Background(), "client-token", "signin")
	if err == nil || strings.Contains(err.Error(), "S3CRET-KEY") {
		t.Fatalf("err = %v", err)
	}
}
