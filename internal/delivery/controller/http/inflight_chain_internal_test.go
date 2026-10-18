package http

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// chainUseCase is just enough of the use case for NewController to build its chain: no event tags, and every
// session cookie fails to open. Any other call panics on the nil embedded interface.
type chainUseCase struct{ IUseCase }

func (chainUseCase) EventTagExists(context.Context, string) (bool, error) { return false, nil }
func (chainUseCase) SetTagListener(eventUseCase.TagListener)              {}
func (chainUseCase) OpenSession(string) (authUseCase.SessionPass, error) {
	return authUseCase.SessionPass{}, authModel.ErrAuthInvalidSession.Err()
}

type chainSink struct {
	mu      sync.Mutex
	reports []errorJournal.Event
}

func (s *chainSink) Report(e errorJournal.Event) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reports = append(s.reports, e)
}
func (s *chainSink) CountNotFound(string) {}
func (s *chainSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.reports)
}

// The in-flight limiter in the real NewController chain: its 503 is readable by the page (CORS), carries the
// request id and Retry-After and is kept out of the error journal; a request the origin guard refuses never
// reaches it, so it cannot take a slot.
func TestInflightLimiterInTheControllerChain(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sink := &chainSink{}
	hosts := config.HostsConfig{
		Main:        "example.test",
		API:         "api.example.test",
		ID:          "id.example.test",
		Admin:       "admin.example.test",
		Exercises:   "exercises.example.test",
		EventDomain: "example.test",
	}
	ctrl := NewController(Dependencies{
		UseCase: chainUseCase{},
		Config: &config.HTTPControllerConfig{
			MaxBodyBytes: 1 << 20,
			Server:       config.HTTPServerConfig{Host: "127.0.0.1", Port: "0"},
		},
		AuthConfig: config.AuthConfig{
			SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			Hosts:                hosts,
			Captcha:              config.CaptchaConfig{Provider: config.CaptchaNone},
		},
		RateLimit: config.RateLimitConfig{
			UserPerMinute:   1000,
			UserBurst:       1000,
			AnonPerMinute:   1000,
			AnonBurst:       1000,
			ClosedPerMinute: 1000,
			ClosedBurst:     1000,
		},
		Inflight:     config.InflightConfig{Max: 1},
		ErrorJournal: sink,
	})
	router, ok := ctrl.server.listeners[0].srv.Handler.(*gin.Engine)
	if !ok {
		t.Fatal("the API listener does not serve the gin router")
	}
	entered, release := make(chan struct{}, 1), make(chan struct{})
	t.Cleanup(func() { close(release) })
	block := func(c *gin.Context) {
		entered <- struct{}{}
		<-release
		c.Status(http.StatusNoContent)
	}
	router.GET("/api/test/block", block)
	router.POST("/api/test/block", block)

	const origin = "https://admin.example.test"
	send := func(method, origin string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/test/block", nil)
		req.Host = hosts.API
		req.Header.Set("Origin", origin)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		return w
	}
	held := make(chan *httptest.ResponseRecorder, 1)
	go func() { held <- send(http.MethodGet, origin) }()
	select {
	case <-entered:
	case w := <-held:
		t.Fatalf("the first request should hold the one slot, got %d %s", w.Code, w.Body.String())
	case <-time.After(5 * time.Second):
		t.Fatal("the first request never reached its handler")
	}

	w := send(http.MethodGet, origin)
	if w.Code != http.StatusServiceUnavailable || w.Header().Get("Retry-After") != "1" {
		t.Fatalf(
			"want the overload 503 with Retry-After 1, got %d %q",
			w.Code,
			w.Header().Get("Retry-After"),
		)
	}
	if got := w.Header().Get("Access-Control-Allow-Origin"); got != origin {
		t.Fatalf("the 503 is not readable by the page: Access-Control-Allow-Origin %q", got)
	}
	if w.Header().Get("X-Request-ID") == "" {
		t.Fatal("the 503 carries no X-Request-ID")
	}
	if n := sink.count(); n != 0 {
		t.Fatalf("the overload 503 was journaled: %d entries", n)
	}

	if refused := send(http.MethodPost, "https://evil.test"); refused.Code != http.StatusForbidden {
		t.Fatalf(
			"a foreign origin must be refused by the origin guard before the limiter: %d",
			refused.Code,
		)
	}
}

// Only the internal listener marks its requests: the in-flight limiter exempts them from the anonymous share
// and the per-session cap.
func TestNewServer_MarksOnlyTheInternalListener(t *testing.T) {
	cfg := config.HTTPServerConfig{
		Host:               "127.0.0.1",
		Port:               "0",
		InternalPort:       "0",
		MaxHeaderMegabytes: 1,
	}
	s := NewServer(&cfg, "127.0.0.1", http.NotFoundHandler())
	seen := false
	for _, l := range s.listeners {
		ctx := context.Background()
		if l.srv.ConnContext != nil {
			ctx = l.srv.ConnContext(ctx, nil)
		}
		internal := l.name == "internal"
		seen = seen || internal
		if middleware.OnInternalListener(ctx) != internal {
			t.Fatalf("%s listener: marked internal = %v", l.name, !internal)
		}
	}
	if !seen {
		t.Fatal("no internal listener was built")
	}
}
