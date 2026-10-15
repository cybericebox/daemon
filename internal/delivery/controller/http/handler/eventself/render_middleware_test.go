package eventself

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

type deniedRenderCalls struct {
	*renderFake
	membershipIDs []uuid.UUID
}

func (f *deniedRenderCalls) RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error {
	f.membershipIDs = append(f.membershipIDs, eventID)
	return f.renderFake.RequireReadEvent(ctx, eventID, userID)
}

type renderTagProbe struct{ calls int }

func (f *renderTagProbe) EventTagExists(_ context.Context, tag string) bool {
	f.calls++
	return tag == "summer"
}
func TestRenderGlobalMiddlewareDenialsAndPreflightAreNeutral(t *testing.T) {
	gin.SetMode(gin.TestMode)
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
	identity := &rbac.Claims{UserID: uuid.Must(uuid.NewV4()), SessionID: uuid.Must(uuid.NewV4()), Role: rbac.RoleUser}
	for _, who := range []*rbac.Claims{nil, identity} {
		f := &deniedRenderCalls{renderFake: &renderFake{id: uuid.Must(uuid.NewV4())}}
		tags := &renderTagProbe{}
		policy := middleware.OriginPolicy{Hosts: cfg.Hosts, Tags: tags}
		r := gin.New()
		r.Use(response.WithErrorHandler, middleware.HandleCORS(policy), middleware.OriginGuardWith(policy))
		NewEventRenderAPIHandler(f, renderIdentityGate{who}, cfg).Init(r.Group("api"))
		for _, path := range []string{"/api/auth/event-render-key", "/api/events/self/render-grant", "/api/events/self/render-manifest", "/api/events/self/render-page?revision=r1"} {
			for _, preflight := range []bool{false, true} {
				var responses []*httptest.ResponseRecorder
				for _, tag := range []string{"summer", "unknown"} {
					method := http.MethodGet
					if strings.Contains(path, "grant") {
						method = http.MethodPost
					}
					if preflight {
						method = http.MethodOptions
					}
					req := httptest.NewRequest(method, path, strings.NewReader(`{"Challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`))
					req.Header.Set("Origin", "https://"+tag+".example.test")
					req.Header.Set("Content-Type", "application/json")
					rec := httptest.NewRecorder()
					r.ServeHTTP(rec, req)
					responses = append(responses, rec)
				}
				a, b := responses[0], responses[1]
				require.Equal(t, a.Code, b.Code, "%s preflight=%v", path, preflight)
				require.Equal(t, a.Body.String(), b.Body.String())
				for _, key := range []string{"Cache-Control", "Vary", "Content-Type", "Access-Control-Allow-Credentials", "Access-Control-Allow-Headers", "Access-Control-Allow-Methods"} {
					require.Equal(t, a.Header().Get(key), b.Header().Get(key), "%s %s", path, key)
				}
				require.Equal(t, "private, no-store", a.Header().Get("Cache-Control"))
			}
		}
		require.Zero(t, tags.calls, "render middleware must not ask whether an event exists")
		if who != nil {
			require.Len(t, f.membershipIDs, 2, "known and missing grant both check event membership")
		}
	}
}

type renderClockWork struct {
	*renderFake
	at *time.Time
}

func (f renderClockWork) RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error {
	*f.at = f.at.Add(12 * time.Millisecond)
	return f.renderFake.RequireReadEvent(ctx, eventID, userID)
}
func TestRenderDenialWaitsBeforeWritingAndRespectsCancellation(t *testing.T) {
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
	identity := &rbac.Claims{UserID: uuid.Must(uuid.NewV4()), SessionID: uuid.Must(uuid.NewV4()), Role: rbac.RoleUser}
	for _, tag := range []string{"summer", "unknown"} {
		t.Run(tag, func(t *testing.T) {
			at := time.Unix(1800000000, 0)
			rec := httptest.NewRecorder()
			f := renderClockWork{&renderFake{id: uuid.Must(uuid.NewV4())}, &at}
			h := NewEventRenderAPIHandler(f, renderIdentityGate{identity}, cfg)
			h.now = func() time.Time { return at }
			var waited time.Duration
			h.wait = func(_ context.Context, d time.Duration) error {
				require.Equal(t, http.StatusOK, rec.Code, "denial status was written before waiting")
				require.Zero(t, rec.Body.Len())
				waited = d
				at = at.Add(d)
				return nil
			}
			r := gin.New()
			r.Use(response.WithErrorHandler)
			h.Init(r.Group("api"))
			req := httptest.NewRequest(http.MethodPost, "/api/events/self/render-grant", strings.NewReader(`{"Challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`))
			req.Header.Set("Origin", "https://"+tag+".example.test")
			r.ServeHTTP(rec, req)
			require.Equal(t, 88*time.Millisecond, waited)
			require.Equal(t, 404, rec.Code)
			require.Equal(t, 100*time.Millisecond, at.Sub(time.Unix(1800000000, 0)))
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.ErrorIs(t, waitForRenderDenial(ctx, time.Second), context.Canceled)
	// Public success and the verification key do not pay the denial floor.
	h := NewEventRenderAPIHandler(&renderFake{id: uuid.Must(uuid.NewV4()), public: true}, renderGate{}, cfg)
	h.wait = func(context.Context, time.Duration) error { t.Fatal("public success was delayed"); return nil }
	r := gin.New()
	h.Init(r.Group("api"))
	req := httptest.NewRequest(http.MethodGet, "/api/events/self/render-manifest", nil)
	req.Header.Set("Origin", "https://summer.example.test")
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	require.Equal(t, 200, rec.Code)
}
func TestRenderOriginsStayCanonicalAndLegacyPolicyKeepsExistenceCheck(t *testing.T) {
	hosts := config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}
	tags := &renderTagProbe{}
	policy := middleware.OriginPolicy{Hosts: hosts, Tags: tags}
	r := gin.New()
	r.Use(middleware.HandleCORS(policy), middleware.OriginGuardWith(policy))
	r.GET("/api/auth/event-render-key", func(c *gin.Context) { c.Status(200) })
	r.GET("/api/legacy-read", func(c *gin.Context) { c.Status(200) })
	for _, origin := range []string{"https://summer.example.test:443", "https://summer.example.test/path", "https://summer.example.test.", "https://foreign.test", "http://summer.example.test", "null"} {
		req := httptest.NewRequest(http.MethodGet, "/api/auth/event-render-key", nil)
		req.Header.Set("Origin", origin)
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		require.Equal(t, 403, rec.Code, origin)
	}
	require.Zero(t, tags.calls)
	for _, tag := range []string{"summer", "unknown"} {
		req := httptest.NewRequest(http.MethodGet, "/api/legacy-read", nil)
		req.Header.Set("Origin", "https://"+tag+".example.test")
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, req)
		expected := 200
		if tag == "unknown" {
			expected = 404
		}
		require.Equal(t, expected, rec.Code)
	}
	require.Positive(t, tags.calls)
}
func TestRenderSignerInvalidConfigurationStopsInitialization(t *testing.T) {
	require.Panics(t, func() {
		NewEventRenderAPIHandler(nil, renderGate{}, config.AuthConfig{SessionEncryptionKey: "invalid"})
	})
}

func TestRenderGuestDenialThroughActualControllerPermissionMiddleware(t *testing.T) {
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", API: "api.example.test", ID: "id.example.test", EventDomain: "example.test"}}
	// No cookie: the real permission gate must deny before an auth use-case call.
	prot := protection.New(protection.Dependencies{Config: cfg})
	policy := middleware.OriginPolicy{Hosts: cfg.Hosts, Tags: &renderTagProbe{}}
	router := gin.New()
	router.Use(response.WithErrorHandler, prot.RequireAPIHost, middleware.HandleCORS(policy), middleware.OriginGuardWith(policy))
	NewEventRenderAPIHandler(&renderFake{}, prot, cfg).Init(router.Group("api"))
	var got []*httptest.ResponseRecorder
	for _, tag := range []string{"summer", "unknown"} {
		req := httptest.NewRequest(http.MethodPost, "/api/events/self/render-grant", strings.NewReader(`{"Challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`))
		req.Host = cfg.Hosts.API
		req.Header.Set("Origin", "https://"+tag+".example.test")
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, 401, rec.Code)
		got = append(got, rec)
	}
	require.Equal(t, got[0].Body.String(), got[1].Body.String())
	for _, header := range []string{"Cache-Control", "Vary", protection.SignInURLHeader, "Content-Type"} {
		require.Equal(t, got[0].Header().Get(header), got[1].Header().Get(header), header)
	}
	require.Equal(t, "private, no-store", got[0].Header().Get("Cache-Control"))
}
