package auth_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"

	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
	authUseCase "github.com/cybericebox/daemon/internal/useCase/auth"
)

func TestPasswordPolicy_PublicReturnsThresholds(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New() // no identity injected: the route must be public
	uc := &fakeUC{policy: authUseCase.PasswordPolicy{
		MinLength:            8,
		MaxLength:            72,
		MinCapitalLetters:    1,
		MinSmallLetters:      1,
		MinDigits:            1,
		MinSpecialCharacters: 0,
		SpecialCharacters:    "!\"#$%&'()*+,-./",
	}}
	h := authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig)
	h.Init(r.Group("api"), r.Group("api"))

	req := httptest.NewRequest(http.MethodGet, "/api/auth/password/policy", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body.String())
	}
	var env struct {
		Data map[string]any
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	want := map[string]any{
		"MinLength":            float64(8),
		"MaxLength":            float64(72),
		"MinCapitalLetters":    float64(1),
		"MinSmallLetters":      float64(1),
		"MinDigits":            float64(1),
		"MinSpecialCharacters": float64(0),
		"SpecialCharacters":    "!\"#$%&'()*+,-./",
	}
	if len(env.Data) != len(want) {
		t.Fatalf("Data keys: got %v, want %v", env.Data, want)
	}
	for k, v := range want {
		if env.Data[k] != v {
			t.Fatalf("Data[%q] = %#v, want %#v", k, env.Data[k], v)
		}
	}
}
