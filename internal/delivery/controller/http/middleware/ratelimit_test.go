package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// signedIn is a stand-in for the authentication middleware: the user comes from the X-User header.
func signedIn(c *gin.Context) {
	if id := c.GetHeader("X-User"); id != "" {
		c.Request = c.Request.WithContext(rbac.ContextWithCurrentUserSession(c.Request.Context(), rbac.Claims{UserID: uuid.FromStringOrNil(id)}))
	}
	c.Next()
}

func TestRateLimitPerUserAllowsTheLimitThenRefuses(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(signedIn)
	r.POST("/preview", middleware.RateLimitPerUser(3, time.Minute), func(c *gin.Context) { c.Status(http.StatusOK) })
	var codes []int
	for i := 0; i < 5; i++ {
		req := httptest.NewRequest(http.MethodPost, "/preview", nil)
		req.RemoteAddr = "203.0.113.5:1234"
		req.Header.Set("X-User", "00000000-0000-7000-8000-000000000001")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		codes = append(codes, w.Code)
	}
	if codes[0] != 200 || codes[2] != 200 || codes[3] != http.StatusTooManyRequests || codes[4] != http.StatusTooManyRequests {
		t.Fatalf("codes = %v", codes)
	}
}

// Nothing is keyed on the client address: a request without a session is not counted, and users
// behind one address are counted separately.
func TestRateLimitPerUserNeverKeysOnTheClientAddress(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(signedIn)
	r.POST("/preview", middleware.RateLimitPerUser(1, time.Minute), func(c *gin.Context) { c.Status(http.StatusOK) })
	post := func(user string) int {
		req := httptest.NewRequest(http.MethodPost, "/preview", nil)
		req.RemoteAddr = "203.0.113.5:1234"
		if user != "" {
			req.Header.Set("X-User", user)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	for i := 0; i < 20; i++ {
		if post("") != http.StatusOK {
			t.Fatal("a request without a session must not be limited")
		}
	}
	a, b := "00000000-0000-7000-8000-00000000000a", "00000000-0000-7000-8000-00000000000b"
	if post(a) != http.StatusOK || post(b) != http.StatusOK {
		t.Fatal("each user has their own budget")
	}
	if post(a) != http.StatusTooManyRequests {
		t.Fatal("the second request of one user is over the limit of 1")
	}
}
