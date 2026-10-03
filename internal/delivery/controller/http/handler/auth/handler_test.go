package auth_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

type fakeUC struct {
	cookie, redirect  string
	sessions          []authUseCase.SessionInfo
	revokeErr         error
	capturedRedirect  string
	reauthPassword    string
	beginErr          error
	changeErr         error
	trustedRedirect   bool
	googleAuthErr     error
	linkErr           error
	policy            authUseCase.PasswordPolicy
	googleRegResult   *authUseCase.GoogleRegistrationResult
	googleRegErr      error
	signUpRedirect    string
	loginRedirect     string
	linkSetupReturnTo string
	linkSetupErr      error
	avatarBody        string
	avatarType        string
}

func (f *fakeUC) SignIn(
	_ context.Context,
	_, _, redirect string,
	_ authModel.SessionMetadata,
) (string, string, error) {
	f.capturedRedirect = redirect
	return f.cookie, f.redirect, nil
}
func (f *fakeUC) IsTrustedRedirect(_ string) bool { return f.trustedRedirect }
func (f *fakeUC) ListSessions(_ context.Context, _, _ uuid.UUID) ([]authUseCase.SessionInfo, error) {
	return f.sessions, nil
}
func (f *fakeUC) RevokeSession(_ context.Context, _, _ uuid.UUID) error       { return f.revokeErr }
func (f *fakeUC) RevokeOtherSessions(_ context.Context, _, _ uuid.UUID) error { return nil }

// slice 2 fakeUC method stubs
func (f *fakeUC) BeginEmailRegistration(_ context.Context, _, redirect string) error {
	f.signUpRedirect = redirect
	return f.beginErr
}
func (f *fakeUC) GetSetupContext(_ context.Context, _ string) (*authUseCase.SetupContext, error) {
	return &authUseCase.SetupContext{Email: "a@b.test"}, nil
}

func (f *fakeUC) CompleteRegistration(
	_ context.Context,
	_, _, _, _ string,
	_ int32,
	_ string,
	_ authModel.SessionMetadata,
) (string, string, error) {
	return "c", "l", nil
}
func (f *fakeUC) PasswordPolicy() authUseCase.PasswordPolicy         { return f.policy }
func (f *fakeUC) ForgotPassword(_ context.Context, _ string) error   { return nil }
func (f *fakeUC) ResetPassword(_ context.Context, _, _ string) error { return nil }
func (f *fakeUC) SetAccountPassword(_ context.Context, _ uuid.UUID, _, _ string) error {
	return f.changeErr
}

// slice 3 fakeUC method stubs — Google OAuth
func (f *fakeUC) GetGoogleLoginURL(redirect string) (string, string, error) {
	f.loginRedirect = redirect
	return "https://accounts.google/x", "state-xyz", nil
}

func (f *fakeUC) GoogleAuth(
	_ context.Context,
	_, _ string,
	_ authModel.SessionMetadata,
) (string, string, error) {
	if f.googleAuthErr != nil {
		return "", "", f.googleAuthErr
	}
	return "c", "l", nil
}

func (f *fakeUC) BeginGoogleRegistration(
	_ context.Context,
	_, _ string,
	_ authModel.SessionMetadata,
) (authUseCase.GoogleRegistrationResult, error) {
	if f.googleRegErr != nil {
		return authUseCase.GoogleRegistrationResult{}, f.googleRegErr
	}
	if f.googleRegResult != nil {
		return *f.googleRegResult, nil
	}
	return authUseCase.GoogleRegistrationResult{SetupToken: "setuptok"}, nil
}
func (f *fakeUC) LinkGoogleToSetupFromOAuth(_ context.Context, _, _, _ string) (string, error) {
	return f.linkSetupReturnTo, f.linkSetupErr
}

// slice 4a fakeUC method stubs — account self-management
func (f *fakeUC) GetAccount(_ context.Context, _ uuid.UUID) (*authUseCase.AccountInfo, error) {
	return &authUseCase.AccountInfo{
		Email:       "a@b.test",
		Providers:   []string{"google"},
		HasPassword: true,
	}, nil
}
func (f *fakeUC) GetSelfProfile(_ context.Context, _ uuid.UUID) (*authUseCase.UserInfo, error) {
	return &authUseCase.UserInfo{Email: "a@b.test"}, nil
}

func (f *fakeUC) UpdateAccountProfile(
	_ context.Context,
	_ uuid.UUID,
	_, _ string,
) error {
	return nil
}

func (f *fakeUC) RequestEmailChange(
	_ context.Context,
	_ uuid.UUID,
	_, _ string,
) error {
	return nil
}

func (f *fakeUC) ConfirmEmailChange(
	_ context.Context,
	_ string,
) error {
	return nil
}

func (f *fakeUC) UnlinkGoogle(
	_ context.Context,
	_ uuid.UUID,
	password string,
) error {
	f.reauthPassword = password
	return nil
}

func (f *fakeUC) DeleteAccount(
	_ context.Context,
	_ uuid.UUID,
	password string,
) error {
	f.reauthPassword = password
	return nil
}
func (f *fakeUC) LinkGoogleToAccountFromOAuth(_ context.Context, _, _, _ string) error {
	return f.linkErr
}

func (f *fakeUC) UploadAvatar(
	_ context.Context,
	_ uuid.UUID,
	_ io.Reader,
	_ int64,
	_ string,
) error {
	return nil
}
func (f *fakeUC) RemoveAvatar(_ context.Context, _ uuid.UUID) error { return nil }
func (f *fakeUC) GetAvatar(_ context.Context, _ uuid.UUID) (io.ReadCloser, string, error) {
	return io.NopCloser(strings.NewReader(f.avatarBody)), f.avatarType, nil
}

type fakeProt struct {
	// capturedSessionCookie records the value passed to Authenticate.
	capturedSessionCookie string
}

func (f *fakeProt) Authenticate(ctx *gin.Context, value string) {
	f.capturedSessionCookie = value
	if value != "" {
		ctx.SetCookie(authModel.SessionCookie, value, 3600, "/", "", true, true)
	}
}

func (fakeProt) DeAuthenticate(ctx *gin.Context) { ctx.Status(http.StatusOK) }

// slice 3 fakeProt addition — pass-through recaptcha middleware
func (fakeProt) RequireCaptcha(string) gin.HandlerFunc {
	return func(c *gin.Context) { c.Next() }
}

// RequirePermission mirrors the real middleware's behavior for the case the
// tests exercise: an authentication-requiring permission with no session in
// the context aborts with 401. Role/permission matching is not simulated.
func (fakeProt) RequirePermission(required rbac.Permission) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := rbac.CurrentUserSessionFromContext(c.Request.Context()); !ok && required.RequireAuthentication() {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Next()
	}
}

// injectIdentity simulates RequireAuthentication for the protected routes.
func injectIdentity(uid, sid uuid.UUID) gin.HandlerFunc {
	return func(c *gin.Context) {
		rc := rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: uid, SessionID: sid, Role: rbac.RoleUser})
		c.Request = c.Request.WithContext(rc)
		c.Next()
	}
}

func TestSignIn_Returns200WithRedirectURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(
		&fakeUC{cookie: "c", redirect: "l"},
		&fakeProt{},
		testAuthConfig,
	)
	api := r.Group("api")
	h.Init(api, api)

	body := `{"Email":"a@b.test","Password":"Secret!1","Redirect":"https://id.example.test/dashboard"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/sign-in", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if w.Code == http.StatusTemporaryRedirect {
		t.Fatal("signIn must NOT issue a 307; XHR auto-follows cross-origin redirects → CORS error")
	}
	var env struct {
		Data struct {
			RedirectURL string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.RedirectURL == "" {
		t.Fatal("response Data.RedirectURL must be non-empty")
	}
}

func TestListSessions_Returns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	sentinel := time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC)
	r := gin.New()
	r.Use(injectIdentity(uid, sid))
	h := authHandler.NewAuthAPIHandler(
		&fakeUC{sessions: []authUseCase.SessionInfo{{ID: sid, IsCurrent: true, CreatedAt: sentinel}}},
		&fakeProt{},
		testAuthConfig,
	)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/sessions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var env struct {
		Data []struct {
			ID        uuid.UUID
			IsCurrent bool
			CreatedAt time.Time
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Data) != 1 || !env.Data[0].IsCurrent {
		t.Fatalf("unexpected sessions: %+v", env.Data)
	}
	if !env.Data[0].CreatedAt.Equal(sentinel) {
		t.Fatalf("want CreatedAt %v, got %v", sentinel, env.Data[0].CreatedAt)
	}
}

func TestSessions_NoIdentity_401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/sessions", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestSignUp_Returns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/sign-up",
		strings.NewReader(`{"Email":"a@b.test"}`),
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestCompleteSetup_Returns200WithRedirectURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	body := `{"Token":"t","FirstName":"Jane","LastName":"Doe","Password":"Secret!1","TosVersion":1,"Redirect":"https://id.example.test/dashboard"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/setup", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if w.Code == http.StatusTemporaryRedirect {
		t.Fatal(
			"completeSetup must NOT issue a 307; XHR auto-follows cross-origin redirects → CORS error",
		)
	}
	var env struct {
		Data struct {
			RedirectURL string
		}
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.RedirectURL == "" {
		t.Fatal("response Data.RedirectURL must be non-empty")
	}
}

func TestChangePassword_NoIdentity_401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/password/change",
		strings.NewReader(`{"OldPassword":"a","NewPassword":"b"}`),
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestGoogleRedirect_Returns307(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("want 307, got %d", w.Code)
	}
}

func TestSignIn_RecaptchaWrapped_Returns200WithRedirectURL(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(
		&fakeUC{cookie: "c", redirect: "l"},
		&fakeProt{},
		testAuthConfig,
	)
	h.Init(r.Group("api"), r.Group("api"))

	body := `{"Email":"a@b.test","Password":"Secret!1","Redirect":"https://id.example.test/dashboard","RecaptchaToken":"t"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/sign-in", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var env struct {
		Data struct{ RedirectURL string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.RedirectURL == "" {
		t.Fatal("response Data.RedirectURL must be non-empty")
	}
}

// TestSignIn_RedirectFlowsToUseCase verifies that the Redirect field from the
// request body is forwarded to the SignIn use case, and that the use case's
// returned redirect value is what comes back in the JSON response — the
// handler no longer builds a callback URL itself (that's the use case's job).
func TestSignIn_RedirectFlowsToUseCase(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uc := &fakeUC{cookie: "sess", redirect: "https://event1.example.test/dashboard"}
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	body := `{"Email":"a@b.test","Password":"Secret!1","Redirect":"https://event1.example.test/dashboard"}`
	req := httptest.NewRequest(http.MethodPost, "/api/auth/sign-in", strings.NewReader(body))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	if uc.capturedRedirect != "https://event1.example.test/dashboard" {
		t.Fatalf(
			"Redirect from request body must flow to SignIn use case, got %q",
			uc.capturedRedirect,
		)
	}
	var env struct {
		Data struct{ RedirectURL string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Data.RedirectURL != "https://event1.example.test/dashboard" {
		t.Fatalf(
			"response Data.RedirectURL must equal the use case's returned redirect, got %q",
			env.Data.RedirectURL,
		)
	}
}

// TestGoogleCallback_StateMismatch_RedirectsToSignInFailed ensures that a callback
// with a mismatched or missing oauth_state cookie redirects to the id frontend
// sign-in page with google_error=failed instead of rendering raw JSON.
func TestGoogleCallback_StateMismatch_RedirectsToSignInFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	h := authHandler.NewAuthAPIHandler(
		&fakeUC{cookie: "c", redirect: "l"},
		&fakeProt{},
		testAuthConfig,
	)
	h.Init(r.Group("api"), r.Group("api"))

	// Request has state=attacker in query but NO matching oauth_state cookie.
	req := httptest.NewRequest(
		http.MethodGet,
		"/api/auth/google/callback?code=c&state=attacker",
		nil,
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("CSRF mismatch should redirect (307), got %d", w.Code)
	}
	loc := w.Header().Get("Location")
	if !strings.Contains(loc, "https://id.example.test/sign-in") {
		t.Fatalf("CSRF mismatch should redirect to id sign-in, got Location: %s", loc)
	}
	if !strings.Contains(loc, "google_error=failed") {
		t.Fatalf("CSRF mismatch should set google_error=failed, got Location: %s", loc)
	}
}

// TestGoogleCallback_StateMatch_SignsIn ensures that when the oauth_state cookie matches
// the query state parameter, sign-in proceeds successfully.
func TestGoogleCallback_StateMatch_SignsIn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	h := authHandler.NewAuthAPIHandler(
		&fakeUC{cookie: "c", redirect: "l"},
		&fakeProt{},
		testAuthConfig,
	)
	h.Init(r.Group("api"), r.Group("api"))

	// No intent cookie → dispatches to sign-in path.
	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("matching state should sign in and return 307, got %d", w.Code)
	}
}

// TestGoogleCallback_NotRegistered_RedirectsToSignUpOffer verifies that when GoogleAuth
// returns ErrAuthGoogleNotRegistered (code 30412), the browser is redirected to the id
// frontend /sign-up page with google_error=not_registered (not raw JSON).
func TestGoogleCallback_NotRegistered_RedirectsToSignUpOffer(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	uc := &fakeUC{googleAuthErr: authModel.ErrAuthGoogleNotRegistered.Err()}
	h := authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("not-registered should redirect (307), got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://id.example.test/sign-up") {
		t.Fatalf("not-registered should redirect to id /sign-up, got Location: %s", loc)
	}
	if !strings.Contains(loc, "google_error=not_registered") {
		t.Fatalf("not-registered should set google_error=not_registered, got Location: %s", loc)
	}
	if w.Body.Len() > 0 && strings.Contains(w.Body.String(), "Status") {
		t.Fatalf("must not render JSON body; got: %s", w.Body.String())
	}
}

// TestGoogleCallback_GoogleAuthGenericError_RedirectsToSignInFailed verifies that when
// GoogleAuth returns a generic (non-sentinel) error, the browser is redirected to the
// id frontend /sign-in page with google_error=failed (not raw JSON).
func TestGoogleCallback_GoogleAuthGenericError_RedirectsToSignInFailed(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	uc := &fakeUC{googleAuthErr: errors.New("some unexpected error")}
	h := authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf(
			"generic GoogleAuth error should redirect (307), got %d body=%s",
			w.Code,
			w.Body.String(),
		)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, "https://id.example.test/sign-in") {
		t.Fatalf("generic error should redirect to id /sign-in, got Location: %s", loc)
	}
	if !strings.Contains(loc, "google_error=failed") {
		t.Fatalf("generic error should set google_error=failed, got Location: %s", loc)
	}
	if w.Body.Len() > 0 && strings.Contains(w.Body.String(), "Status") {
		t.Fatalf("must not render JSON body; got: %s", w.Body.String())
	}
}

// slice 4a tests

func TestGetAccount_Returns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	r := gin.New()
	r.Use(injectIdentity(uid, sid))
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/account", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestAccount_NoIdentity_401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/account", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

func TestConfirmEmailChange_Returns200(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(
		http.MethodPost,
		"/api/auth/account/email/confirm",
		strings.NewReader(`{"Code":"c"}`),
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
}

func TestGoogleLinkRedirect_NoIdentity_401(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/link", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", w.Code)
	}
}

// TestGoogleLinkRedirect_Returns307_SetsLinkSessionCookie asserts that the
// googleLinkRedirect handler redirects to Google (307) and sets the
// oauth_link_sid Lax cookie containing the current session value.
func TestGoogleLinkRedirect_Returns307(t *testing.T) {
	gin.SetMode(gin.TestMode)
	uid := uuid.Must(uuid.NewV7())
	sid := uuid.Must(uuid.NewV7())
	r := gin.New()
	r.Use(injectIdentity(uid, sid))
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/link", nil)
	// Simulate the session cookie being present (same-site request).
	req.AddCookie(&http.Cookie{Name: authModel.SessionCookie, Value: "sess-value"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("want 307, got %d", w.Code)
	}
	// Verify the Lax link-session cookie was set with the session value.
	var linkSIDCookie *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-cib_oauth_link_sid" {
			linkSIDCookie = c
			break
		}
	}
	if linkSIDCookie == nil {
		t.Fatal("oauth_link_sid cookie must be set by googleLinkRedirect")
	}
	if linkSIDCookie.Value != "sess-value" {
		t.Fatalf("oauth_link_sid must carry session value, got %q", linkSIDCookie.Value)
	}
	if linkSIDCookie.MaxAge <= 0 {
		t.Fatalf("oauth_link_sid must have positive MaxAge, got %d", linkSIDCookie.MaxAge)
	}
}

// ---------------------------------------------------------------------------
// googleCallback link-case tests
// ---------------------------------------------------------------------------

// TestGoogleCallback_Link_Success asserts that a valid link flow (intent=link,
// matching state, oauth_link_sid present) results in a 307 to the absolute
// https://id.<domain>/profile?tab=connections URL — this handler runs on
// api.<domain>, so a relative Location would 404 against the API — and that
// the oauth_link_sid cookie is cleared (MaxAge < 0).
func TestGoogleCallback_Link_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_intent", Value: "link"})
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_link_sid", Value: "sess-value"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("link success: want 307, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "https://id.example.test/profile?tab=connections" {
		t.Fatalf("link success: want absolute id Location, got %q", loc)
	}
	// The link cookie must be cleared (MaxAge < 0).
	var cleared bool
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-cib_oauth_link_sid" && c.MaxAge < 0 {
			cleared = true
			break
		}
	}
	if !cleared {
		t.Fatal("oauth_link_sid must be cleared (MaxAge<0) after the link callback")
	}
}

// TestGoogleCallback_Link_MissingLinkCookie asserts that when the oauth_link_sid
// cookie is absent (simulating the cross-site Strict drop), the handler redirects
// to the absolute https://id.<domain>/profile?error=link_failed URL (not 401, not
// raw JSON, not a relative path that would 404 against api.<domain>).
func TestGoogleCallback_Link_MissingLinkCookie(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_intent", Value: "link"})
	// oauth_link_sid is intentionally absent — simulates Strict cookie being dropped.
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("missing link cookie: want 307, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "https://id.example.test/profile?error=link_failed" {
		t.Fatalf("missing link cookie: want absolute id Location, got %q", loc)
	}
}

// TestGoogleCallback_Link_UseCase_Error asserts that when LinkGoogleToAccountFromOAuth
// returns an error, the handler redirects to the absolute
// https://id.<domain>/profile?error=link_failed URL.
func TestGoogleCallback_Link_UseCase_Error(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	uc := &fakeUC{linkErr: errors.New("account already linked")}
	h := authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_intent", Value: "link"})
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_link_sid", Value: "sess-value"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("link usecase error: want 307, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "https://id.example.test/profile?error=link_failed" {
		t.Fatalf("link usecase error: want absolute id Location, got %q", loc)
	}
}

// TestGoogleCallback_Register_Success asserts that the register-intent branch
// redirects to the absolute https://id.<domain>/setup?token=... URL.
func TestGoogleCallback_Register_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_intent", Value: "register"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("register success: want 307, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "https://id.example.test/setup?token=setuptok" {
		t.Fatalf("register success: want absolute id Location, got %q", loc)
	}
}

// TestGoogleCallback_Setup_Success asserts that the setup-intent branch redirects
// to the absolute https://id.<domain>/setup?token=... URL.
func TestGoogleCallback_Setup_Success(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	h := authHandler.NewAuthAPIHandler(&fakeUC{}, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/callback?code=c&state=s", nil)
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_state", Value: "s"})
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_intent", Value: "setup"})
	req.AddCookie(&http.Cookie{Name: "__Host-cib_oauth_setup_token", Value: "existing-setup-tok"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusTemporaryRedirect {
		t.Fatalf("setup success: want 307, got %d body=%s", w.Code, w.Body.String())
	}
	loc := w.Header().Get("Location")
	if loc != "https://id.example.test/setup?token=existing-setup-tok" {
		t.Fatalf("setup success: want absolute id Location, got %q", loc)
	}
}
