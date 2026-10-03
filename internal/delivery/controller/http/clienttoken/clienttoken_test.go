package clienttoken

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestIssuedValueVerifiesAndCarriesAnId(t *testing.T) {
	s := NewSigner("secret", "", time.Hour)
	a, _, err := s.Issue()
	if err != nil {
		t.Fatal(err)
	}
	b, _, _ := s.Issue()
	idA, okA := s.Verify(a)
	idB, okB := s.Verify(b)
	if !okA || !okB || idA == "" || idA == idB {
		t.Fatalf("a: %v %q, b: %v %q", okA, idA, okB, idB)
	}
}

func TestExpiredValueIsRefused(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	s := NewSigner("secret", "", time.Hour)
	s.now = func() time.Time { return now }
	v, expires, _ := s.Issue()
	if !expires.Equal(now.Add(time.Hour)) {
		t.Fatalf("expires %v", expires)
	}
	now = now.Add(time.Hour - time.Second)
	if _, ok := s.Verify(v); !ok {
		t.Fatal("still valid just before the expiry")
	}
	now = now.Add(time.Second)
	if _, ok := s.Verify(v); ok {
		t.Fatal("expired value accepted")
	}
}

func TestTamperedOrForeignValuesAreRefused(t *testing.T) {
	s := NewSigner("secret", "", time.Hour)
	other := NewSigner("other", "", time.Hour)
	v, _, _ := s.Issue()
	parts := strings.Split(v, ".")
	flipped := []byte(parts[1])
	if flipped[0] == 'A' {
		flipped[0] = 'B'
	} else {
		flipped[0] = 'A'
	}
	cases := map[string]string{
		"payload changed": parts[0] + "." + string(flipped) + "." + parts[2],
		"signature cut":   parts[0] + "." + parts[1] + "." + parts[2][:10],
		"other key":       mustIssue(other),
		"wrong version":   "v2." + parts[1] + "." + parts[2],
		"empty":           "",
		"garbage":         "a.b.c",
		"two parts":       parts[1] + "." + parts[2],
	}
	for name, value := range cases {
		if _, ok := s.Verify(value); ok {
			t.Errorf("%s accepted", name)
		}
	}
}

func mustIssue(s *Signer) string {
	v, _, _ := s.Issue()
	return v
}

func TestTheFallbackKeyIsDerivedFromTheJWTSecret(t *testing.T) {
	a := NewSigner("", "jwt-secret", time.Hour)
	b := NewSigner("", "jwt-secret", time.Hour)
	c := NewSigner("", "another", time.Hour)
	v, _, _ := a.Issue()
	if _, ok := b.Verify(v); !ok {
		t.Fatal("replicas with the same JWT secret must agree")
	}
	if _, ok := c.Verify(v); ok {
		t.Fatal("a different JWT secret must not")
	}
}

func TestSetWritesAnHttpOnlySecureLaxCookieThatFromRequestReads(t *testing.T) {
	gin.SetMode(gin.TestMode)
	s := NewSigner("secret", "", 2*time.Hour)
	r := gin.New()
	r.GET("/set", func(c *gin.Context) { _, _ = s.Set(c); c.Status(http.StatusOK) })
	var got string
	r.GET("/read", func(c *gin.Context) {
		id, ok := s.FromRequest(c)
		if ok {
			got = id
		}
		c.Status(http.StatusOK)
	})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/set", nil))
	cookies := w.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("cookies %v", cookies)
	}
	ck := cookies[0]
	if ck.Name != Cookie || !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteLaxMode || ck.MaxAge != 7200 || ck.Path != "/" || ck.Domain != "" {
		t.Fatalf("cookie %+v", ck)
	}
	req := httptest.NewRequest(http.MethodGet, "/read", nil)
	req.AddCookie(ck)
	r.ServeHTTP(httptest.NewRecorder(), req)
	if got == "" {
		t.Fatal("FromRequest did not accept the cookie it issued")
	}
}
