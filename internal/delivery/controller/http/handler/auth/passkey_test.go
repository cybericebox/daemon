package auth_test

import (
	"encoding/json"
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
	passkeyModel "github.com/cybericebox/daemon/internal/model/passkey"
)

func passkeyRouter(uc *fakeUC, prot *fakeProt, identity bool) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(response.WithErrorHandler)
	if identity {
		r.Use(injectIdentity(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())))
	}
	h := authHandler.NewAuthAPIHandler(uc, prot, testAuthConfig)
	api := r.Group("api")
	h.Init(api, api)
	return r
}

func TestPasskeySignInFinish_OpensSessionLikeSignIn(t *testing.T) {
	prot := &fakeProt{}
	uc := &fakeUC{cookie: "sealed", redirect: "https://id.example.test/profile/"}
	r := passkeyRouter(uc, prot, false)

	body := `{"CeremonyID":"cer","Credential":{"id":"x"},"Redirect":"https://id.example.test/x"}`
	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPost,
			"/api/auth/passkeys/sign-in/finish",
			strings.NewReader(body),
		),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if prot.capturedSessionCookie != "sealed" ||
		uc.capturedRedirect != "https://id.example.test/x" {
		t.Fatalf("session cookie %q, redirect %q", prot.capturedSessionCookie, uc.capturedRedirect)
	}
	var env struct {
		Data struct {
			RedirectURL string `json:"RedirectURL"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || env.Data.RedirectURL == "" {
		t.Fatalf("want RedirectURL, got %s (%v)", w.Body, err)
	}
}

func TestPasskeySignInFinish_FailureSetsNoCookie(t *testing.T) {
	prot := &fakeProt{}
	r := passkeyRouter(&fakeUC{passkeyErr: passkeyModel.ErrPasskeySignInFailed.Err()}, prot, false)

	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPost,
			"/api/auth/passkeys/sign-in/finish",
			strings.NewReader(`{"CeremonyID":"c","Credential":{}}`),
		),
	)
	if w.Code != http.StatusUnauthorized || prot.capturedSessionCookie != "" {
		t.Fatalf("want 401 without a cookie, got %d / %q", w.Code, prot.capturedSessionCookie)
	}
}

func TestPasskeySignInBegin_ReturnsCeremony(t *testing.T) {
	r := passkeyRouter(&fakeUC{}, &fakeProt{}, false)
	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPost,
			"/api/auth/passkeys/sign-in/begin",
			strings.NewReader(`{"RecaptchaToken":"t"}`),
		),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var env struct {
		Data struct {
			CeremonyID string `json:"CeremonyID"`
			Options    struct {
				PublicKey struct {
					Challenge string `json:"challenge"`
				} `json:"publicKey"`
			} `json:"Options"`
		} `json:"data"`
	}
	if err := json.Unmarshal(
		w.Body.Bytes(),
		&env,
	); err != nil || env.Data.CeremonyID != "cer" ||
		env.Data.Options.PublicKey.Challenge != "c" {
		t.Fatalf("unexpected body %s (%v)", w.Body, err)
	}
}

func TestPasskeyAccountRoutes_RequireSignIn(t *testing.T) {
	r := passkeyRouter(&fakeUC{}, &fakeProt{}, false)
	id := uuid.Must(uuid.NewV7()).String()
	for _, c := range []struct{ method, path string }{
		{http.MethodGet, "/api/auth/passkeys"},
		{http.MethodPost, "/api/auth/passkeys/registration/begin"},
		{http.MethodPost, "/api/auth/passkeys/registration/finish"},
		{http.MethodPatch, "/api/auth/passkeys/" + id},
		{http.MethodDelete, "/api/auth/passkeys/" + id},
	} {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(c.method, c.path, strings.NewReader(`{}`)))
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s %s without a session: want 401, got %d", c.method, c.path, w.Code)
		}
	}
}

func TestPasskeyList_Fields(t *testing.T) {
	used := time.Date(2026, 10, 1, 8, 0, 0, 0, time.UTC)
	uc := &fakeUC{passkeys: []passkeyModel.Passkey{
		{
			ID: uuid.Must(
				uuid.NewV7(),
			),
			Name:           "Laptop",
			Transports:     []string{"internal"},
			AAGUID:         make([]byte, 16),
			BackupEligible: true,
			BackupState:    true,
			CreatedAt:      used,
			LastUsedAt:     &used,
		},
	}}
	r := passkeyRouter(uc, &fakeProt{}, true)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/passkeys", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", w.Code)
	}
	var env struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil || len(env.Data) != 1 {
		t.Fatalf("unexpected body %s (%v)", w.Body, err)
	}
	for _, key := range []string{"ID", "Name", "Transports", "AAGUID", "BackupEligible", "BackedUp", "CreatedAt", "LastUsedAt"} {
		if _, ok := env.Data[0][key]; !ok {
			t.Errorf("missing field %s in %v", key, env.Data[0])
		}
	}
	for _, secret := range []string{"PublicKey", "CredentialID", "SignCount"} {
		if _, ok := env.Data[0][secret]; ok {
			t.Errorf("%s must not be exposed", secret)
		}
	}
}

func TestPasskeyRegistrationBegin_PassesPassword(t *testing.T) {
	uc := &fakeUC{}
	r := passkeyRouter(uc, &fakeProt{}, true)
	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPost,
			"/api/auth/passkeys/registration/begin",
			strings.NewReader(`{"CurrentPassword":"Secret!1"}`),
		),
	)
	if w.Code != http.StatusOK || uc.reauthPassword != "Secret!1" {
		t.Fatalf("want 200 with the password passed on, got %d / %q", w.Code, uc.reauthPassword)
	}
}

func TestPasskeyRemoveAndRename(t *testing.T) {
	id := uuid.Must(uuid.NewV7()).String()
	r := passkeyRouter(&fakeUC{}, &fakeProt{}, true)

	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodPatch,
			"/api/auth/passkeys/"+id,
			strings.NewReader(`{"Name":"New"}`),
		),
	)
	if w.Code != http.StatusOK {
		t.Fatalf("rename: want 200, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/auth/passkeys/"+id, nil))
	if w.Code != http.StatusOK {
		t.Fatalf("remove: want 200, got %d", w.Code)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, "/api/auth/passkeys/not-a-uuid", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("bad id: want 400, got %d", w.Code)
	}
}

func TestPasskeyRemove_NotFoundIs404(t *testing.T) {
	r := passkeyRouter(
		&fakeUC{passkeyErr: passkeyModel.ErrPasskeyNotFound.Err()},
		&fakeProt{},
		true,
	)
	w := httptest.NewRecorder()
	r.ServeHTTP(
		w,
		httptest.NewRequest(
			http.MethodDelete,
			"/api/auth/passkeys/"+uuid.Must(uuid.NewV7()).String(),
			nil,
		),
	)
	if w.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", w.Code)
	}
}

func TestExportMyData_IsAnAttachmentOfThePersonsOwnJSON(t *testing.T) {
	r := passkeyRouter(&fakeUC{}, &fakeProt{}, true)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/account/export", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	if got := w.Header().
		Get("Content-Disposition"); got != `attachment; filename="cyber-ice-box-my-data-2026-10-31.json"` {
		t.Errorf("Content-Disposition: %q", got)
	}
	if w.Header().Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(w.Header().Get("Content-Type"), "application/json") {
		t.Errorf("headers: %v", w.Header())
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	if _, ok := body["Profile"]; !ok {
		t.Errorf("the file itself, not an envelope: %v", body)
	}
}

func TestExportMyData_RefusalsAndSession(t *testing.T) {
	cases := map[string]struct {
		err  error
		want int
	}{
		"stale sign-in": {authModel.ErrAuthReauthRequired.Err(), http.StatusForbidden},
		"too often":     {authModel.ErrAuthTooManyRequests.Err(), http.StatusTooManyRequests},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			r := passkeyRouter(&fakeUC{exportErr: c.err}, &fakeProt{}, true)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/account/export", nil))
			if w.Code != c.want || w.Header().Get("Content-Disposition") != "" {
				t.Fatalf(
					"want %d without a file, got %d / %q",
					c.want,
					w.Code,
					w.Header().Get("Content-Disposition"),
				)
			}
		})
	}
	w := httptest.NewRecorder()
	passkeyRouter(
		&fakeUC{},
		&fakeProt{},
		false,
	).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/api/auth/account/export", nil))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("without a session: want 401, got %d", w.Code)
	}
}
