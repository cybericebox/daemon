package auth_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	authHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/auth"
)

// The session keeps the client address only when it is trusted and public: X-Forwarded-For counts only from a
// TRUSTED_PROXIES peer (gin's ClientIP), and a private, CGNAT, loopback or link-local result is dropped.
func TestSignIn_SessionIPOnlyFromATrustedSourceAndPublic(t *testing.T) {
	cases := []struct {
		name    string
		trusted []string
		peer    string
		xff     string
		wantIP  string
	}{
		{
			"no trusted proxy: the header is ignored, the pod peer is dropped",
			nil,
			"10.64.3.2:5000",
			"203.0.113.9",
			"",
		},
		{
			"no trusted proxy: a public peer is the client, a spoofed header is ignored",
			nil,
			"198.51.100.4:5000",
			"203.0.113.9",
			"198.51.100.4",
		},
		{
			"trusted ingress: the forwarded public client",
			[]string{"10.0.0.0/8"},
			"10.64.3.2:5000",
			"203.0.113.9",
			"203.0.113.9",
		},
		{
			"trusted ingress: a forwarded private address is dropped",
			[]string{"10.0.0.0/8"},
			"10.64.3.2:5000",
			"192.168.1.5",
			"",
		},
		{
			"trusted ingress: a forwarded CGNAT address is dropped",
			[]string{"10.0.0.0/8"},
			"10.64.3.2:5000",
			"100.64.2.3",
			"",
		},
		{
			"trusted ingress: a public IPv6 client",
			[]string{"10.0.0.0/8"},
			"10.64.3.2:5000",
			"2001:4860:4860::8888",
			"2001:4860:4860::8888",
		},
		{
			"trusted ingress: a link-local IPv6 is dropped",
			[]string{"10.0.0.0/8"},
			"10.64.3.2:5000",
			"fe80::1",
			"",
		},
		{
			"an untrusted peer between: its own address, private, is dropped",
			[]string{"10.0.0.0/8"},
			"172.20.0.4:5000",
			"203.0.113.9",
			"",
		},
		{"loopback peer without a proxy", nil, "127.0.0.1:5000", "", ""},
		{"IPv6 loopback peer", nil, "[::1]:5000", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			r := gin.New()
			if err := r.SetTrustedProxies(c.trusted); err != nil {
				t.Fatal(err)
			}
			uc := &fakeUC{cookie: "c", redirect: "l"}
			h := authHandler.NewAuthAPIHandler(uc, &fakeProt{}, testAuthConfig)
			api := r.Group("api")
			h.Init(api, api)

			req := httptest.NewRequest(
				http.MethodPost,
				"/api/auth/sign-in",
				strings.NewReader(`{"Email":"a@b.test","Password":"Secret!1"}`),
			)
			req.RemoteAddr = c.peer
			if c.xff != "" {
				req.Header.Set("X-Forwarded-For", c.xff)
			}
			req.Header.Set("User-Agent", "ua")
			w := httptest.NewRecorder()
			r.ServeHTTP(w, req)
			if w.Code != http.StatusOK {
				t.Fatalf("want 200, got %d", w.Code)
			}
			if uc.capturedMeta.IP != c.wantIP || uc.capturedMeta.UserAgent != "ua" {
				t.Fatalf("session metadata = %+v, want IP %q", uc.capturedMeta, c.wantIP)
			}
		})
	}
}
