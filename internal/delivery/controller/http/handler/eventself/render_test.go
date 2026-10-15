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
func TestRenderHTTPAnonymousAndScopedGrant(t *testing.T) {
	gin.SetMode(gin.TestMode)
	id := uuid.Must(uuid.NewV4())
	fake := &renderFake{id: id, public: true, revision: "r1"}
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewEventRenderAPIHandler(fake, renderGate{}, cfg).Init(router.Group("api"))
	request := func(path, origin, authorization string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, path, nil)
		r.Header.Set("Origin", origin)
		r.Header.Set("Authorization", authorization)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	require.Equal(t, 200, request("/api/auth/event-render-key", "", "").Code)
	require.Equal(t, 200, request("/api/events/self/render-manifest", "https://summer.example.test", "").Code)
	fake.public = false
	require.Equal(t, 404, request("/api/events/self/render-manifest", "https://summer.example.test", "").Code)
	require.Equal(t, 404, request("/api/events/self/render-manifest", "https://unknown.example.test", "").Code)
	signer, err := eventrender.New(cfg.SessionEncryptionKey)
	require.NoError(t, err)
	token, _, err := signer.Issue("https://summer.example.test", id, uuid.Must(uuid.NewV4()), uuid.Must(uuid.NewV4()), true, "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", time.Now())
	require.NoError(t, err)
	require.Equal(t, 200, request("/api/events/self/render-manifest", "https://summer.example.test", "EventRender "+token).Code)
	require.True(t, fake.previewSeen)
	require.Equal(t, 404, request("/api/events/self/render-manifest", "https://unknown.example.test", "EventRender "+token).Code)
	require.Equal(t, 404, request("/api/events/self/render-manifest", "https://summer.example.test:444", "EventRender "+token).Code)
	fake.revision = "r2"
	require.Equal(t, 409, request("/api/events/self/render-page?revision=r1", "https://summer.example.test", "EventRender "+token).Code)
	require.Equal(t, "private, no-store", request("/api/events/self/render-manifest", "https://summer.example.test", "").Header().Get("Cache-Control"))
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
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
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
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
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
	cfg := config.AuthConfig{SessionEncryptionKey: "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f", Hosts: config.HostsConfig{Domain: "example.test", EventDomain: "example.test"}}
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
