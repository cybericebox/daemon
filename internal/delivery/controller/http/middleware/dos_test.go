package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/clienttoken"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

var dosHosts = config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test"}

func dosConfig() config.DOSConfig {
	return config.DOSConfig{Protection: "on", ClientTokenTTL: time.Hour,
		NoTokenPerMinute: 60, NoTokenBurst: 2, ClientPerMinute: 60, ClientBurst: 3,
		AuthPerMinute: 60, AuthBurst: 4, EventPerMinute: 60, EventBurst: 4}
}

type dosEnv struct {
	r      *gin.Engine
	signer *clienttoken.Signer
	now    *time.Time
}

func newDOSEnv(cfg config.DOSConfig) dosEnv {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	l := newLimiter(config.RateLimitConfig{UserPerMinute: 60, UserBurst: 5, AnonPerMinute: 60, AnonBurst: 1}, &now)
	signer := clienttoken.NewSigner("secret", "", time.Hour)
	l.EnableDOSProtection(cfg, dosHosts, signer)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(l.Anonymous)
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	for _, path := range []string{"/api/x", "/api/client-token", "/api/health", "/api/auth/avatar/:id",
		"/api/auth/sign-in", "/api/auth/password/reset", "/api/auth/google/callback"} {
		r.Any(path, ok)
	}
	return dosEnv{r: r, signer: signer, now: &now}
}

func (e dosEnv) do(method, path, origin string, cookies ...*http.Cookie) int {
	req := httptest.NewRequest(method, path, nil)
	req.RemoteAddr = "203.0.113.5:1"
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	return w.Code
}

func TestDOSTheMissingTokenRefusalIsMarkedForTheFrontend(t *testing.T) {
	e := newDOSEnv(dosConfig())
	req := httptest.NewRequest("GET", "/api/x", nil)
	w := httptest.NewRecorder()
	e.r.ServeHTTP(w, req)
	if w.Code != http.StatusTooManyRequests || w.Header().Get(clienttoken.RequiredHeader) != "required" {
		t.Fatalf("code %d, header %q", w.Code, w.Header().Get(clienttoken.RequiredHeader))
	}
	ck := e.client()
	for i := 0; i < 4; i++ {
		req = httptest.NewRequest("GET", "/api/x", nil)
		req.AddCookie(ck)
		w = httptest.NewRecorder()
		e.r.ServeHTTP(w, req)
	}
	if w.Code != http.StatusTooManyRequests || w.Header().Get(clienttoken.RequiredHeader) != "" {
		t.Fatalf("a rate limit proper is not marked: %d %q", w.Code, w.Header().Get(clienttoken.RequiredHeader))
	}
}

func (e dosEnv) client() *http.Cookie {
	v, _, _ := e.signer.Issue()
	return &http.Cookie{Name: clienttoken.Cookie, Value: v}
}

func TestDOSEachClientCookieHasItsOwnBucket(t *testing.T) {
	e := newDOSEnv(dosConfig())
	a, b := e.client(), e.client()
	for i := 0; i < 3; i++ {
		if c := e.do("GET", "/api/x", "", a); c != 200 {
			t.Fatalf("a request %d: %d", i, c)
		}
	}
	if c := e.do("GET", "/api/x", "", a); c != http.StatusTooManyRequests {
		t.Fatalf("a over its burst: %d", c)
	}
	if c := e.do("GET", "/api/x", "", b); c != 200 {
		t.Fatalf("b must not feel a: %d", c)
	}
	*e.now = e.now.Add(2 * time.Second)
	if c := e.do("GET", "/api/x", "", a); c != 200 {
		t.Fatalf("a refills: %d", c)
	}
}

func TestDOSWithoutACookieOnlyTheOpenRoutesPassAndShareASmallBucket(t *testing.T) {
	e := newDOSEnv(dosConfig())
	if c := e.do("GET", "/api/x", ""); c != http.StatusTooManyRequests {
		t.Fatalf("an ordinary route without the cookie: %d", c)
	}
	if c := e.do("GET", "/api/health", ""); c != 200 {
		t.Fatalf("health: %d", c)
	}
	// Burst 2: the token endpoint, public media and the Google callback share it.
	for _, path := range []string{"/api/client-token", "/api/auth/avatar/:id"} {
		if c := e.do("POST", path, ""); c != 200 {
			t.Fatalf("%s: %d", path, c)
		}
	}
	if c := e.do("POST", "/api/client-token", ""); c != http.StatusTooManyRequests {
		t.Fatalf("the no-token bucket is small and shared: %d", c)
	}
	if c := e.do("GET", "/api/auth/google/callback", ""); c != http.StatusTooManyRequests {
		t.Fatalf("the callback shares it: %d", c)
	}
	*e.now = e.now.Add(time.Minute)
	if c := e.do("POST", "/api/client-token", ""); c != 200 {
		t.Fatalf("refilled: %d", c)
	}
}

func TestDOSTheTokenEndpointIsAlwaysCountedInTheNoTokenBucket(t *testing.T) {
	e := newDOSEnv(dosConfig())
	ck := e.client()
	for i := 0; i < 2; i++ {
		if c := e.do("POST", "/api/client-token", "", ck); c != 200 {
			t.Fatalf("refresh %d: %d", i, c)
		}
	}
	if c := e.do("POST", "/api/client-token", "", ck); c != http.StatusTooManyRequests {
		t.Fatalf("a valid cookie does not lift the token endpoint's limit: %d", c)
	}
	if c := e.do("GET", "/api/x", "", ck); c != 200 {
		t.Fatalf("the cookie's own bucket is separate: %d", c)
	}
}

func TestDOSTamperedOrExpiredCookiesCountAsNoCookie(t *testing.T) {
	e := newDOSEnv(dosConfig())
	if c := e.do("GET", "/api/x", "", &http.Cookie{Name: clienttoken.Cookie, Value: "v1.AAAA.BBBB"}); c != http.StatusTooManyRequests {
		t.Fatalf("forged: %d", c)
	}
	ck := e.client()
	other := clienttoken.NewSigner("secret", "", -time.Minute)
	v, _, _ := other.Issue()
	if c := e.do("GET", "/api/x", "", &http.Cookie{Name: clienttoken.Cookie, Value: v}); c != http.StatusTooManyRequests {
		t.Fatalf("expired: %d", c)
	}
	if c := e.do("GET", "/api/x", "", ck); c != 200 {
		t.Fatalf("the genuine one passes: %d", c)
	}
}

func TestDOSTheCredentialGroupHasASharedBucketOfItsOwn(t *testing.T) {
	e := newDOSEnv(dosConfig())
	// Four browsers, one request each, fill the auth burst (4); a fifth browser is refused on sign-in
	// but still reads a public page: the buckets are split.
	for i := 0; i < 4; i++ {
		if c := e.do("POST", "/api/auth/sign-in", "", e.client()); c != 200 {
			t.Fatalf("sign-in %d: %d", i, c)
		}
	}
	fifth := e.client()
	if c := e.do("POST", "/api/auth/password/reset", "", fifth); c != http.StatusTooManyRequests {
		t.Fatalf("the group is shared: %d", c)
	}
	if c := e.do("GET", "/api/x", "", fifth); c != 200 {
		t.Fatalf("public pages are not starved by sign-in attempts: %d", c)
	}
}

func TestDOSPublicPagesHaveABucketPerEventTag(t *testing.T) {
	e := newDOSEnv(dosConfig())
	for i := 0; i < 4; i++ {
		if c := e.do("GET", "/api/x", "https://spring.example.test", e.client()); c != 200 {
			t.Fatalf("spring %d: %d", i, c)
		}
	}
	if c := e.do("GET", "/api/x", "https://spring.example.test", e.client()); c != http.StatusTooManyRequests {
		t.Fatalf("spring over its burst: %d", c)
	}
	if c := e.do("GET", "/api/x", "https://autumn.example.test", e.client()); c != 200 {
		t.Fatalf("another event is not affected: %d", c)
	}
	if c := e.do("GET", "/api/x", "https://id.example.test", e.client()); c != 200 {
		t.Fatalf("a platform host has no event bucket: %d", c)
	}
}

func TestDOSNothingIsKeyedOnTheAddress(t *testing.T) {
	e := newDOSEnv(dosConfig())
	ck := e.client()
	for i := 0; i < 3; i++ {
		req := httptest.NewRequest("GET", "/api/x", nil)
		req.RemoteAddr = "198.51.100." + string(rune('1'+i)) + ":1"
		req.AddCookie(ck)
		e.r.ServeHTTP(httptest.NewRecorder(), req)
	}
	if c := e.do("GET", "/api/x", "", ck); c != http.StatusTooManyRequests {
		t.Fatalf("one cookie, many addresses, one bucket: %d", c)
	}
}

func TestDOSASessionCookieSkipsTheAnonymousBuckets(t *testing.T) {
	e := newDOSEnv(dosConfig())
	session := &http.Cookie{Name: authModel.SessionCookie, Value: "x"}
	for i := 0; i < 10; i++ {
		if c := e.do("GET", "/api/x", "", session); c != 200 {
			t.Fatalf("signed-in request %d: %d (the user bucket is in the gate)", i, c)
		}
	}
}

// off: the one shared anonymous bucket, as before; no cookie needed.
func TestDOSOffKeepsTheSharedAnonymousBucket(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	l := newLimiter(config.RateLimitConfig{UserPerMinute: 60, UserBurst: 5, AnonPerMinute: 60, AnonBurst: 2}, &now)
	r := limiterRouter(l, false)
	for i := 0; i < 2; i++ {
		if w := limGet(r, "/api/x", "203.0.113.5:1", "", false); w.Code != 200 {
			t.Fatalf("request %d without a client cookie: %d", i, w.Code)
		}
	}
	if w := limGet(r, "/api/x", "203.0.113.5:1", "", false); w.Code != http.StatusTooManyRequests {
		t.Fatalf("shared bucket spent: %d", w.Code)
	}
}
