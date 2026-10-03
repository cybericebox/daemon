package protection

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

type fakeVerifier struct {
	err           error
	token, action string
	calls         int
}

func (f *fakeVerifier) Verify(_ context.Context, token, action string) error {
	f.calls++
	f.token, f.action = token, action
	return f.err
}

func captchaRouter(p *Protection) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	r.POST("/x", p.RequireCaptcha("signIn"), func(c *gin.Context) { c.Status(http.StatusOK) })
	return r
}

func post(r *gin.Engine, path, body string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, strings.NewReader(body)))
	return w
}

func TestRequireCaptchaPassesTheTokenAndActionToTheProvider(t *testing.T) {
	f := &fakeVerifier{}
	r := captchaRouter(New(Dependencies{Captcha: f}))
	if w := post(r, "/x", `{"RecaptchaToken":"tok"}`); w.Code != http.StatusOK {
		t.Fatalf("code %d", w.Code)
	}
	if f.token != "tok" || f.action != "signIn" {
		t.Fatalf("verifier saw %q %q", f.token, f.action)
	}
}

func TestRequireCaptchaRefusesWhenTheProviderDoes(t *testing.T) {
	f := &fakeVerifier{err: errors.New("bad")}
	r := captchaRouter(New(Dependencies{Captcha: f}))
	if w := post(r, "/x", `{"RecaptchaToken":"tok"}`); w.Code == http.StatusOK {
		t.Fatal("a refused token must not pass")
	}
	if w := post(r, "/x", `{}`); w.Code != http.StatusBadRequest || f.calls != 1 {
		t.Fatalf("a missing token is refused before the provider: %d, %d calls", w.Code, f.calls)
	}
}

func TestTheProviderIsChosenByConfig(t *testing.T) {
	for provider, want := range map[string]string{
		config.CaptchaTurnstile: "*protection.turnstileVerifier",
		config.CaptchaRecaptcha: "protection.recaptchaVerifier",
		config.CaptchaNone:      "protection.noneVerifier",
	} {
		p := New(Dependencies{Config: config.AuthConfig{Captcha: config.CaptchaConfig{Provider: provider}}})
		if got := typeName(p.captcha); got != want {
			t.Errorf("%s: %s, want %s", provider, got, want)
		}
	}
	if err := (noneVerifier{}).Verify(context.Background(), "", "x"); err != nil {
		t.Fatal("none accepts everything")
	}
}

func typeName(v any) string {
	switch v.(type) {
	case *turnstileVerifier:
		return "*protection.turnstileVerifier"
	case recaptchaVerifier:
		return "protection.recaptchaVerifier"
	case noneVerifier:
		return "protection.noneVerifier"
	}
	return "?"
}

func turnstileServer(t *testing.T, reply string) (*turnstileVerifier, *string) {
	t.Helper()
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body = r.URL.String() + "|" + string(b)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	prev := turnstileVerifyURL
	turnstileVerifyURL = srv.URL + "/siteverify"
	t.Cleanup(func() { turnstileVerifyURL = prev })
	return &turnstileVerifier{secret: "TS-SECRET", hosts: testHosts}, &body
}

func TestTurnstileSendsTheSecretInTheBodyAndAcceptsAPlatformHostname(t *testing.T) {
	v, body := turnstileServer(t, `{"success":true,"action":"signIn","hostname":"id.example.test"}`)
	if err := v.Verify(context.Background(), "the-token", "signIn"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(strings.SplitN(*body, "|", 2)[0], "TS-SECRET") ||
		!strings.Contains(*body, "secret=TS-SECRET") || !strings.Contains(*body, "response=the-token") {
		t.Fatalf("request %q", *body)
	}
}

func TestTurnstileRefusals(t *testing.T) {
	for name, reply := range map[string]string{
		"not successful": `{"success":false}`,
		"foreign host":   `{"success":true,"hostname":"evil.test"}`,
		"api host":       `{"success":true,"hostname":"api.example.test"}`,
		"no host":        `{"success":true}`,
		"another action": `{"success":true,"action":"signUp","hostname":"id.example.test"}`,
		"not json":       `oops`,
	} {
		v, _ := turnstileServer(t, reply)
		if err := v.Verify(context.Background(), "t", "signIn"); err == nil {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTurnstileAcceptsAnEventSiteAndNoActionReported(t *testing.T) {
	v, _ := turnstileServer(t, `{"success":true,"hostname":"ctf.example.test"}`)
	if err := v.Verify(context.Background(), "t", "clientToken"); err != nil {
		t.Fatal(err)
	}
}

func TestTurnstileNetworkFailureLeaksNoSecret(t *testing.T) {
	prev := turnstileVerifyURL
	turnstileVerifyURL = "http://127.0.0.1:1/siteverify"
	defer func() { turnstileVerifyURL = prev }()
	err := (&turnstileVerifier{secret: "TS-SECRET", hosts: testHosts}).Verify(context.Background(), "t", "a")
	if err == nil || strings.Contains(err.Error(), "TS-SECRET") {
		t.Fatalf("err = %v", err)
	}
}
