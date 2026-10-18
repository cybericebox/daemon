package eventself

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/eventrender"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUC "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type renderFake struct {
	id                  uuid.UUID
	public, previewSeen bool
	readAllowed         bool
	revision            string
	calls               int
}

func (f *renderFake) ResolveEventByTag(_ context.Context, tag string, _ time.Time) (eventUC.EventTenantView, error) {
	if tag != "summer" {
		return eventUC.EventTenantView{}, eventModel.ErrEventNotFound.Err()
	}
	return eventUC.EventTenantView{EventID: f.id, Tag: tag, Public: f.public}, nil
}
func (f *renderFake) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error {
	if f.readAllowed {
		return nil
	}
	return eventManagerModel.ErrEventManagementForbidden.Err()
}
func (f *renderFake) GetEventRenderManifest(ctx context.Context, id uuid.UUID, slug string, preview bool, now time.Time) (eventUC.EventRenderManifestView, error) {
	f.calls++
	f.previewSeen = preview
	_, genericSession := rbac.CurrentUserSessionFromContext(ctx)
	if genericSession {
		panic("render grant became platform session")
	}
	if !f.public && !preview {
		return eventUC.EventRenderManifestView{}, eventModel.ErrEventNotFound.Err()
	}
	return eventUC.EventRenderManifestView{Event: eventUC.EventInfoView{EventID: id}, Revision: f.revision, Audience: "public", Page: eventUC.RenderPageInfo{Slug: slug}, CacheUntil: now.Add(time.Second).UnixMilli()}, nil
}
func (f *renderFake) GetEventRenderPage(ctx context.Context, id uuid.UUID, slug, revision string, preview bool, now time.Time) (eventUC.EventRenderPageView, error) {
	if revision != f.revision {
		return eventUC.EventRenderPageView{}, eventUC.ErrRenderRevisionChanged
	}
	return eventUC.EventRenderPageView{Revision: revision}, nil
}

type renderGate struct{}

func (renderGate) RequirePermission(rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) { ctx.Next() }
}

const testRenderSecret = "0123456789abcdef0123456789abcdef-render-secret"

func TestRenderHTTPSecretOnlyNoOriginAndScopedGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := uuid.Must(uuid.NewV4())
	fake := &renderFake{id: id, public: true, revision: "r1"}
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", RenderSecret: testRenderSecret, Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewEventRenderAPIHandler(fake, renderGate{}, cfg).Init(router.Group("api"))
	request := func(path, secret, authorization string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		if secret != "" {
			r.Header.Set(renderSecretHeader, secret)
		}
		if authorization != "" {
			r.Header.Set("Authorization", authorization)
		}
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	manifest := "/api/render/events/summer/manifest"
	// Without the secret (or with a wrong one) the routes do not exist.
	for _, path := range []string{"/api/render/key", manifest, "/api/render/events/summer/page?revision=r1"} {
		require.Equal(t, 404, request(path, "", "").Code, path)
		require.Equal(t, 404, request(path, "wrong", "").Code, path)
		require.Equal(t, 404, request(path, testRenderSecret+"x", "").Code, path)
	}
	// No Origin header is needed or looked at.
	require.Equal(t, 200, request("/api/render/key", testRenderSecret, "").Code)
	require.Equal(t, 200, request(manifest, testRenderSecret, "").Code)
	require.Equal(t, "private, no-store", request(manifest, testRenderSecret, "").Header().Get("Cache-Control"))
	require.Equal(t, 404, request("/api/render/events/unknown/manifest", testRenderSecret, "").Code)
	require.Equal(t, 404, request("/api/render/events/Bad_Tag/manifest", testRenderSecret, "").Code)
	// The removed browser-style routes are gone.
	require.Equal(t, 404, request("/api/events/self/render-manifest", testRenderSecret, "").Code)
	require.Equal(t, 404, request("/api/events/self/render-page?revision=r1", testRenderSecret, "").Code)
	require.Equal(t, 404, request("/api/auth/event-render-key", testRenderSecret, "").Code)
	// A revision's page is immutable for the server cache.
	page := request("/api/render/events/summer/page?revision=r1", testRenderSecret, "")
	require.Equal(t, 200, page.Code)
	require.Equal(t, "private, max-age=31536000, immutable", page.Header().Get("Cache-Control"))

	fake.public = false
	require.Equal(t, 404, request(manifest, testRenderSecret, "").Code)
	signer, err := eventrender.New(cfg.SessionEncryptionKey)
	require.NoError(t, err)
	token, _, err := signer.Issue("https://summer.example.test", id, uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), true, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", time.Now())
	require.NoError(t, err)
	require.Equal(t, 200, request(manifest, testRenderSecret, "EventRender "+token).Code)
	require.True(t, fake.previewSeen)
	// A grant cannot be used without the secret, for another event, or for another audience.
	require.Equal(t, 404, request(manifest, "", "EventRender "+token).Code)
	require.Equal(t, 404, request("/api/render/events/other/manifest", testRenderSecret, "EventRender "+token).Code)
	otherAudience, _, err := signer.Issue("https://other.example.test", id, uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), true, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", time.Now())
	require.NoError(t, err)
	require.Equal(t, 404, request(manifest, testRenderSecret, "EventRender "+otherAudience).Code)
	fake.revision = "r2"
	pageGrant := request("/api/render/events/summer/page?revision=r1", testRenderSecret, "EventRender "+token)
	require.Equal(t, 409, pageGrant.Code)
}

func TestRenderWithoutConfiguredSecretServesNothing(t *testing.T) {
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewEventRenderAPIHandler(&renderFake{id: uuid.Must(uuid.NewV4()), public: true}, renderGate{}, cfg).Init(router.Group("api"))
	for _, secret := range []string{"", "anything"} {
		r := httptest.NewRequest(http.MethodGet, "/api/render/key", nil)
		r.Header.Set(renderSecretHeader, secret)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		require.Equal(t, 404, w.Code)
	}
}

type renderIdentityGate struct{ identity *rbac.Claims }

func (g renderIdentityGate) RequirePermission(permission rbac.Permission) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if g.identity != nil {
			ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), *g.identity))
		}
		if permission == rbac.PermSelf && g.identity == nil {
			response.AbortWithUnauthenticated(ctx)
			return
		}
		ctx.Next()
	}
}
func TestRenderGrantIssuanceRequiresAPISessionAndBoundedChallenge(t *testing.T) {
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", RenderSecret: testRenderSecret, Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
	id := uuid.Must(uuid.NewV4())
	identity := &rbac.Claims{UserID: uuid.Must(uuid.NewV4()), SessionID: uuid.Must(uuid.NewV4()), Role: rbac.RoleUser}
	challenge := "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	for _, tc := range []struct {
		name, origin, body string
		identity           *rbac.Claims
		status             int
	}{
		{"valid", "https://summer.example.test", `{"Challenge":"` + challenge + `"}`, identity, 200},
		{"anonymous", "https://summer.example.test", `{"Challenge":"` + challenge + `"}`, nil, 401},
		{"wrong origin port", "https://summer.example.test:444", `{"Challenge":"` + challenge + `"}`, identity, 404},
		{"bad challenge", "https://summer.example.test", `{"Challenge":"x"}`, identity, 400},
		{"oversized", "https://summer.example.test", `{"Challenge":"` + strings.Repeat("a", 1024) + `"}`, identity, 400},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake := &renderFake{id: id, public: false, readAllowed: true}
			router := gin.New()
			router.Use(response.WithErrorHandler)
			NewEventRenderAPIHandler(fake, renderIdentityGate{tc.identity}, cfg).Init(router.Group("api"))
			req := httptest.NewRequest(http.MethodPost, "/api/events/self/render-grant", strings.NewReader(tc.body))
			req.Header.Set("Origin", tc.origin)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code)
			require.Equal(t, "private, no-store", rec.Header().Get("Cache-Control"))
			if tc.status == 200 {
				var result struct{ Data eventRenderGrantResponse }
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
				s, err := eventrender.New(cfg.SessionEncryptionKey)
				require.NoError(t, err)
				claims, err := s.Verify(result.Data.Token, tc.origin, id, time.Now())
				require.NoError(t, err)
				require.Equal(t, challenge, claims.Challenge)
				require.Equal(t, identity.SessionID.String(), claims.SessionID)
				require.True(t, claims.Preview)
			}
		})
	}
}

func TestRenderPublicEventDoesNotIssueAGrant(t *testing.T) {
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", RenderSecret: testRenderSecret, Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
	fake := &renderFake{id: uuid.Must(uuid.NewV4()), public: true}
	identity := &rbac.Claims{UserID: uuid.Must(uuid.NewV4()), SessionID: uuid.Must(uuid.NewV4()), Role: rbac.RoleUser}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewEventRenderAPIHandler(fake, renderIdentityGate{identity}, cfg).Init(router.Group("api"))
	req := httptest.NewRequest(http.MethodPost, "/api/events/self/render-grant", strings.NewReader(`{"Challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`))
	req.Header.Set("Origin", "https://summer.example.test")
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, 404, rec.Code)
	require.NotContains(t, rec.Body.String(), "Token")
}

func TestRenderGrantUnknownAndUnauthorizedStaffAreNeutral(t *testing.T) {
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", RenderSecret: testRenderSecret, Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
	fake := &renderFake{id: uuid.Must(uuid.NewV4()), public: false}
	identity := &rbac.Claims{UserID: uuid.Must(uuid.NewV4()), SessionID: uuid.Must(uuid.NewV4()), Role: rbac.RoleUser}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewEventRenderAPIHandler(fake, renderIdentityGate{identity}, cfg).Init(router.Group("api"))
	var bodies []string
	for _, origin := range []string{"https://summer.example.test", "https://unknown.example.test"} {
		req := httptest.NewRequest(http.MethodPost, "/api/events/self/render-grant", strings.NewReader(`{"Challenge":"AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"}`))
		req.Header.Set("Origin", origin)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, 404, rec.Code)
		require.NotContains(t, rec.Body.String(), "Token")
		bodies = append(bodies, rec.Body.String())
	}
	require.Equal(t, bodies[0], bodies[1])
}
