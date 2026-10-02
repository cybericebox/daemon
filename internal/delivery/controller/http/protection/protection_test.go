package protection_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	adminAuditUseCase "github.com/cybericebox/daemon/internal/useCase/adminAudit"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

// fakeUseCase is a hand-written stand-in for the auth use case. The interface
// only exposes ValidateSessionCookie/UpdateLastSeen/SignOut now — there is
// exactly one credential (the session cookie), so the old local-token /
// exchange-code / return-to methods no longer exist on IUseCase.
type fakeUseCase struct {
	sessionResult *authUseCase.SessionAuthResult
	sessionErr    error
	auditEntries  []adminAuditUseCase.Entry
}

func (f *fakeUseCase) ValidateSessionCookie(
	_ context.Context,
	_ string,
) (*authUseCase.SessionAuthResult, error) {
	return f.sessionResult, f.sessionErr
}

func (f *fakeUseCase) UpdateLastSeen(_ context.Context, _, _ uuid.UUID) error { return nil }
func (f *fakeUseCase) SignOut(_ context.Context, _ uuid.UUID) error           { return nil }
func (f *fakeUseCase) RecordAdminAction(_ context.Context, entry adminAuditUseCase.Entry) error {
	f.auditEntries = append(f.auditEntries, entry)
	return nil
}

func newProt(uc protection.IUseCase) *protection.Protection {
	return protection.New(
		protection.Dependencies{
			UseCase: uc,
			Config:  config.AuthConfig{Hosts: config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test", Exercises: "exercises.example.test", EventDomain: "example.test"}},
		},
	)
}

// findCookie returns the named Set-Cookie from the recorder, or nil.
func findCookie(w *httptest.ResponseRecorder, name string) *http.Cookie {
	for _, c := range w.Result().Cookies() {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func TestRequireAuthentication_NoCookie_401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newProt(&fakeUseCase{})
	r := gin.New()
	r.Use(response.WithErrorHandler, p.RequireAPIHost, p.RequirePermission(rbac.PermSelf))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "api.example.test"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
	// An absent cookie is its own error (the client can see its own request,
	// nothing to hide) — distinct from a cookie that fails validation.
	if !strings.Contains(w.Body.String(), "Session cookie is missing") {
		t.Fatalf("want ErrAuthMissingSessionCookie message, got body %s", w.Body.String())
	}
}

func TestRequireAuthentication_ValidSession_PopulatesContext(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	p := newProt(
		&fakeUseCase{
			sessionResult: &authUseCase.SessionAuthResult{
				Claims: authModel.AuthClaims{SessionID: sid, UserID: uid, Role: "user"},
				Session: &authModel.Session{
					ID:        sid,
					UserID:    uid,
					ExpiresAt: time.Now().Add(time.Hour),
				},
			},
		},
	)
	r := gin.New()
	r.Use(response.WithErrorHandler, p.RequireAPIHost, p.RequirePermission(rbac.PermSelf))
	var gotUser uuid.UUID
	var gotSession uuid.UUID
	var gotRole rbac.Role
	r.GET(
		"/x", func(c *gin.Context) {
			cl, _ := rbac.CurrentUserSessionFromContext(c.Request.Context())
			gotUser, gotSession, gotRole = cl.UserID, cl.SessionID, cl.Role
			c.Status(http.StatusOK)
		},
	)

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "api.example.test"
	req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "whatever"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if gotUser != uid || gotSession != sid || gotRole != rbac.RoleUser {
		t.Fatalf("context not populated: user=%v session=%v role=%v", gotUser, gotSession, gotRole)
	}
}

// TestResolveAuth_InvalidSessionCookie_ClearsSessionCookie verifies that when
// ValidateSessionCookie returns an error (stale/invalid cookie), resolveAuth
// (via RequireAuthentication) clears the session cookie so the browser stops
// resending a dead credential. Absent cookie must NOT clear anything (nothing
// to clear).
func TestResolveAuth_InvalidSessionCookie_ClearsSessionCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("invalid session cookie is cleared", func(t *testing.T) {
		uc := &fakeUseCase{sessionErr: errAuthFailed}
		p := newProt(uc)
		r := gin.New()
		r.Use(response.WithErrorHandler, p.RequireAPIHost, p.RequirePermission(rbac.PermSelf))
		r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Host = "api.example.test"
		req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "stale-session"})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", w.Code)
		}
		cleared := findCookie(w, authModel.SessionCookie)
		if cleared == nil {
			t.Fatal(
				"session cookie must be cleared (Set-Cookie with MaxAge<0) on validation failure, but no Set-Cookie found",
			)
		}
		if cleared.MaxAge >= 0 {
			t.Errorf("session cookie MaxAge: want <0 (clear), got %d", cleared.MaxAge)
		}
	})

	t.Run("absent session cookie does not emit Set-Cookie", func(t *testing.T) {
		uc := &fakeUseCase{sessionErr: errAuthFailed}
		p := newProt(uc)
		r := gin.New()
		r.Use(response.WithErrorHandler, p.RequireAPIHost, p.RequirePermission(rbac.PermSelf))
		r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.Host = "api.example.test"
		// deliberately no session cookie
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", w.Code)
		}
		if c := findCookie(w, authModel.SessionCookie); c != nil {
			t.Errorf(
				"absent cookie: must not emit Set-Cookie for %q, but got one (MaxAge=%d)",
				authModel.SessionCookie,
				c.MaxAge,
			)
		}
	})
}

func TestRequireAuthentication_SessionValidationFailurePreservesCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newProt(&fakeUseCase{sessionErr: model.ErrPlatform.Err()})
	r := gin.New()
	r.Use(response.WithErrorHandler, p.RequireAPIHost, p.RequirePermission(rbac.PermSelf))
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "api.example.test"
	req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "valid-session"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want 500, got %d", w.Code)
	}
	if c := findCookie(w, authModel.SessionCookie); c != nil {
		t.Fatalf("temporary validation failure must preserve the session cookie, got %+v", c)
	}
	if got := w.Header().Get(protection.SignInURLHeader); got != "" {
		t.Fatalf("temporary validation failure must not advertise sign-in, got %q", got)
	}
}

// errAuthFailed simulates ValidateSessionCookie failing on a stale/invalid
// cookie. It must be a classified err.Error (not a plain errors.New), same as
// the real use case returns, so response.WithErrorHandler maps it to 401
// instead of falling back to a generic 500.
var errAuthFailed = authModel.ErrAuthSessionExpired.Err()

func TestRequireAPIHost_ExactAPIHost_200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newProt(&fakeUseCase{})
	r := gin.New()
	r.Use(p.RequireAPIHost)
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "api.example.test"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestRequireAPIHost_ForeignHost_404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newProt(&fakeUseCase{})
	r := gin.New()
	r.Use(p.RequireAPIHost)
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "evil.com"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

// TestRequireAPIHost_BareApexDomain_404 proves a behavior change: the old
// ValidateRequestDomain accepted the bare apex domain (Host: "example.test")
// as a valid root request. RequireAPIHost requires the Host to be EXACTLY
// api.<domain> now, so the bare apex must be rejected with 404.
func TestRequireAPIHost_BareApexDomain_404(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newProt(&fakeUseCase{})
	r := gin.New()
	r.Use(p.RequireAPIHost)
	r.GET("/x", func(c *gin.Context) { c.Status(http.StatusOK) })

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Host = "example.test"
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("bare apex domain must now be rejected: want 404, got %d", w.Code)
	}
}

// TestRequirePermission verifies the 403 permission gate. RequirePermission
// now always runs checkAuthentication first (it is the single entry point),
// so — unlike before, when a preceding handler could inject a role directly
// into the context — the role must come from a real authenticated session
// (fakeUseCase + session cookie), same as TestRequireAuthentication_ValidSession_PopulatesContext.
func TestRequirePermission(t *testing.T) {
	gin.SetMode(gin.TestMode)

	run := func(role rbac.Role) int {
		uid := uuid.Must(uuid.NewV7())
		sid := uuid.Must(uuid.NewV7())
		p := newProt(
			&fakeUseCase{
				sessionResult: &authUseCase.SessionAuthResult{
					Claims: authModel.AuthClaims{SessionID: sid, UserID: uid, Role: role},
					Session: &authModel.Session{
						ID:        sid,
						UserID:    uid,
						ExpiresAt: time.Now().Add(time.Hour),
					},
				},
			},
		)
		r := gin.New()
		r.Use(response.WithErrorHandler)
		r.GET("/x", p.RequirePermission(rbac.PermUsersRead), func(c *gin.Context) { c.Status(http.StatusOK) })

		req := httptest.NewRequest(http.MethodGet, "/x", nil)
		req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "whatever"})
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	if run(rbac.RoleAdmin) != http.StatusOK {
		t.Error("admin holds users.read → 200")
	}
	if run(rbac.RoleUser) != http.StatusForbidden {
		t.Error("user lacks users.read → 403")
	}
}

func TestRequirePermission_PublicReadWithoutSession(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newProt(&fakeUseCase{})
	r := gin.New()
	r.Use(response.WithErrorHandler)
	r.GET("/public", p.RequirePermission(rbac.PermEventContentRead), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.GET("/private", p.RequirePermission(rbac.PermSelf), func(c *gin.Context) { c.Status(http.StatusOK) })

	for _, test := range []struct {
		path string
		want int
	}{
		{path: "/public", want: http.StatusOK},
		{path: "/private", want: http.StatusUnauthorized},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, test.path, nil))
		if w.Code != test.want {
			t.Errorf("GET %s without session: got %d, want %d; body=%s", test.path, w.Code, test.want, w.Body.String())
		}
	}
}

func TestRequirePermission_RecordsSuccessfulAdminMutation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid, sid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{sessionResult: &authUseCase.SessionAuthResult{Claims: authModel.AuthClaims{SessionID: sid, UserID: uid, Role: rbac.RoleAdmin}}}
	p := newProt(uc)
	r := gin.New()
	r.POST("/users/:id/status", p.RequirePermission(rbac.PermUsersStatusWrite), func(c *gin.Context) { c.Status(http.StatusOK) })
	req := httptest.NewRequest(http.MethodPost, "/users/abc/status", nil)
	req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "session"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if len(uc.auditEntries) != 1 {
		t.Fatalf("want one audit entry, got %d", len(uc.auditEntries))
	}
	got := uc.auditEntries[0]
	if got.ActorID != uid || got.Permission != string(rbac.PermUsersStatusWrite) || got.Route != "/users/:id/status" {
		t.Fatalf("unexpected audit entry: %+v", got)
	}
}

func TestRequirePermission_RecordsTheAuditTargetSetByTheHandler(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid, sid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{sessionResult: &authUseCase.SessionAuthResult{Claims: authModel.AuthClaims{SessionID: sid, UserID: uid, Role: rbac.RoleSuperAdmin}}}
	p := newProt(uc)
	r := gin.New()
	r.POST("/stands/:id", p.RequirePermission(rbac.PermInfrastructureWrite), func(c *gin.Context) {
		audit.SetTarget(c, "event:"+c.Param("id"))
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(http.MethodPost, "/stands/abc", nil)
	req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "session"})
	r.ServeHTTP(httptest.NewRecorder(), req)
	if len(uc.auditEntries) != 1 || uc.auditEntries[0].Target != "event:abc" {
		t.Fatalf("audit entries = %+v, want the handler target", uc.auditEntries)
	}
}

// TestAuthenticate_Attributes verifies that Authenticate writes a Set-Cookie
// header for __Host-session with Secure, HttpOnly, and SameSite=Strict
// attributes.
func TestAuthenticate_Attributes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	p := newProt(&fakeUseCase{})

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)

	p.Authenticate(ctx, "session-value")

	setCookieHeader := w.Header().Get("Set-Cookie")
	if setCookieHeader == "" {
		t.Fatal("SetSessionCookie must write a Set-Cookie header, got none")
	}

	sc := findCookie(w, authModel.SessionCookie)
	if sc == nil {
		t.Fatalf(
			"Set-Cookie header %q does not contain %q",
			setCookieHeader,
			authModel.SessionCookie+"=",
		)
	}
	if sc.Value != "session-value" {
		t.Errorf("session cookie value: want %q, got %q", "session-value", sc.Value)
	}
	if !sc.Secure {
		t.Error("session cookie Secure: want true, got false")
	}
	if !sc.HttpOnly {
		t.Error("session cookie HttpOnly: want true, got false")
	}
	if sc.SameSite != http.SameSiteStrictMode {
		t.Errorf(
			"session cookie SameSite: want %v (Strict), got %v",
			http.SameSiteStrictMode,
			sc.SameSite,
		)
	}
	if sc.Domain != "" {
		t.Errorf("session cookie Domain: want empty (host-only, __Host- prefix), got %q", sc.Domain)
	}
}

// TestDeAuthenticate_ClearsOnlySessionCookie verifies DeAuthenticate clears
// exactly one cookie (__Host-session) now that the local-token cookie is
// gone — previously it cleared both "session" and "token".
func TestDeAuthenticate_ClearsOnlySessionCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	sid := uuid.Must(uuid.NewV7())
	p := newProt(&fakeUseCase{})

	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx.Request = req.WithContext(rbac.ContextWithCurrentUserSession(req.Context(), rbac.Claims{SessionID: sid, Role: rbac.RoleUser}))

	p.DeAuthenticate(ctx)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}

	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("want exactly 1 cleared cookie, got %d: %+v", len(cookies), cookies)
	}
	if cookies[0].Name != authModel.SessionCookie {
		t.Errorf("cleared cookie name: want %q, got %q", authModel.SessionCookie, cookies[0].Name)
	}
	if cookies[0].MaxAge >= 0 {
		t.Errorf("cleared cookie MaxAge: want <0 (clear), got %d", cookies[0].MaxAge)
	}
}

// auditRun serves one request through RequirePermission and returns the audit
// entries and the status the client received. The error handler is the outer
// middleware, as in the real router.
func auditRun(t *testing.T, role rbac.Role, perm rbac.Permission, method, route, url string, handler gin.HandlerFunc) ([]adminAuditUseCase.Entry, int) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	uid, sid := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	uc := &fakeUseCase{sessionResult: &authUseCase.SessionAuthResult{Claims: authModel.AuthClaims{SessionID: sid, UserID: uid, Role: role}}}
	p := newProt(uc)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	r.Handle(method, route, p.RequirePermission(perm), handler)
	req := httptest.NewRequest(method, url, nil)
	req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "session"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return uc.auditEntries, w.Code
}

// M4 PoC: the client got 409 while the audit log said 200, because a handler
// error is written by the outer error handler AFTER the audit middleware ran.
func TestAudit_RecordsTheStatusTheClientGets(t *testing.T) {
	entries, code := auditRun(t, rbac.RoleAdmin, rbac.PermUsersStatusWrite, http.MethodPost, "/users/:id/status", "/users/abc/status",
		func(c *gin.Context) { response.AbortWithError(c, authModel.ErrLastSuperAdmin.Err()) })
	if code != http.StatusBadRequest {
		t.Fatalf("setup: client status %d", code)
	}
	if len(entries) != 1 || entries[0].ResponseStatus != code {
		t.Fatalf("audit must record the final status %d, got %+v", code, entries)
	}
}

func TestAudit_InternalErrorIsRecordedAs500(t *testing.T) {
	entries, code := auditRun(t, rbac.RoleAdmin, rbac.PermUsersStatusWrite, http.MethodPost, "/users/:id/status", "/users/abc/status",
		func(c *gin.Context) { response.AbortWithError(c, errors.New("boom")) })
	if code != http.StatusInternalServerError || len(entries) != 1 || entries[0].ResponseStatus != http.StatusInternalServerError {
		t.Fatalf("client %d, audit %+v", code, entries)
	}
}

// A refused administrative action is recorded too.
func TestAudit_RecordsForbiddenAdministrativeAttempt(t *testing.T) {
	entries, code := auditRun(t, rbac.RoleUser, rbac.PermUsersDelete, http.MethodDelete, "/users/:userID", "/users/abc",
		func(c *gin.Context) { c.Status(http.StatusOK) })
	if code != http.StatusForbidden {
		t.Fatalf("setup: want 403, got %d", code)
	}
	if len(entries) != 1 || entries[0].ResponseStatus != http.StatusForbidden || entries[0].Permission != string(rbac.PermUsersDelete) {
		t.Fatalf("want one 403 entry, got %+v", entries)
	}
}

// The exercise catalog is written under PermSelf: staff work, so audited.
func TestAudit_RecordsPermSelfExerciseWrites(t *testing.T) {
	id := uuid.Must(uuid.NewV7()).String()
	entries, _ := auditRun(t, rbac.RoleUser, rbac.PermSelf, http.MethodPost, "/exercises/:id/publish", "/exercises/"+id+"/publish",
		func(c *gin.Context) { c.Status(http.StatusOK) })
	if len(entries) != 1 || entries[0].Target != "id:"+id {
		t.Fatalf("want an audited exercise write with its target, got %+v", entries)
	}
}

func TestAudit_PlainSelfServiceIsNotAudited(t *testing.T) {
	for name, tc := range map[string]struct {
		perm          rbac.Permission
		method, route string
	}{
		"profile":       {rbac.PermSelf, http.MethodPatch, "/auth/account/profile"},
		"inbox read":    {rbac.PermNotificationsSelf, http.MethodPatch, "/inbox/:id/read"},
		"admin read":    {rbac.PermUsersRead, http.MethodGet, "/users"},
		"exercise read": {rbac.PermSelf, http.MethodGet, "/exercises/:id"},
	} {
		t.Run(name, func(t *testing.T) {
			entries, _ := auditRun(t, rbac.RoleSuperAdmin, tc.perm, tc.method, tc.route, tc.route, func(c *gin.Context) { c.Status(http.StatusOK) })
			if len(entries) != 0 {
				t.Fatalf("must not be audited: %+v", entries)
			}
		})
	}
}

func TestAudit_ExportsAreAudited(t *testing.T) {
	entries, _ := auditRun(t, rbac.RoleSuperAdmin, rbac.PermAnalyticsRead, http.MethodGet, "/analytics/users/export.csv", "/analytics/users/export.csv",
		func(c *gin.Context) { c.Status(http.StatusOK) })
	if len(entries) != 1 {
		t.Fatalf("a PII export must be audited, got %+v", entries)
	}
}

// Without a handler-set target the UUID route params name the object.
func TestAudit_DefaultTargetFromRouteIDs(t *testing.T) {
	user := uuid.Must(uuid.NewV7()).String()
	entries, _ := auditRun(t, rbac.RoleAdmin, rbac.PermUsersDelete, http.MethodDelete, "/users/:userID", "/users/"+user,
		func(c *gin.Context) { c.Status(http.StatusOK) })
	if len(entries) != 1 || entries[0].Target != "userID:"+user {
		t.Fatalf("target = %+v", entries)
	}
}
