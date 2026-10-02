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
		_, _ = w.Write([]byte(`{"success":true,"score":0.9,"action":"signin","hostname":"id.example.test"}`))
	}))
	defer srv.Close()
	prev := siteVerifyURL
	siteVerifyURL = srv.URL + "/siteverify"
	defer func() { siteVerifyURL = prev }()

	p := &Protection{recaptcha: config.RecaptchaConfig{SecretKey: "S3CRET-KEY", Score: 0.5}, hosts: testHosts}
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

var testHosts = config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test"}

// L9: a token solved on somebody else's page is not a proof for our visitor.
func TestRecaptchaTokenMustBeSolvedOnAPlatformFrontend(t *testing.T) {
	for hostname, ok := range map[string]bool{
		"id.example.test":       true,
		"ctf.example.test":      true, // an event site
		"id.example.test.":      true,
		"ID.EXAMPLE.TEST":       true,
		"evil.test":             false,
		"api.example.test":      false, // the API host is not a frontend
		"a.b.example.test":      false,
		"id.example.test.evil":  false,
		"":                      false,
		"localhost":             false,
		"example.test.evil.com": false,
	} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			_, _ = w.Write([]byte(`{"success":true,"score":0.9,"action":"signin","hostname":"` + hostname + `"}`))
		}))
		prev := siteVerifyURL
		siteVerifyURL = srv.URL
		p := &Protection{recaptcha: config.RecaptchaConfig{SecretKey: "k", Score: 0.5}, hosts: testHosts}
		err := p.verifyRecaptchaToken(context.Background(), "t", "signin")
		siteVerifyURL = prev
		srv.Close()
		if (err == nil) != ok {
			t.Errorf("hostname %q: accepted=%v, want %v (err=%v)", hostname, err == nil, ok, err)
		}
	}
}
