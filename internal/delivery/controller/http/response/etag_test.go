package response

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func etagGet(ifNoneMatch string, freshFor time.Duration, data any) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(w)
	ctx.Request = httptest.NewRequest(http.MethodGet, "/", nil)
	if ifNoneMatch != "" {
		ctx.Request.Header.Set("If-None-Match", ifNoneMatch)
	}
	AbortWithETagData(ctx, data, freshFor)
	return w
}

func TestAbortWithETagDataRevalidates(t *testing.T) {
	first := etagGet("", 0, map[string]int{"a": 1})
	tag := first.Header().Get("ETag")
	if first.Code != http.StatusOK || tag == "" || first.Header().Get("Cache-Control") != "private, no-cache" {
		t.Fatalf("first: %d etag=%q cache=%q", first.Code, tag, first.Header().Get("Cache-Control"))
	}
	if same := etagGet(tag, 0, map[string]int{"a": 1}); same.Code != http.StatusNotModified || same.Body.Len() != 0 {
		t.Fatalf("unchanged: %d body=%q", same.Code, same.Body.String())
	}
	if weak := etagGet(`"x", W/`+tag, 0, map[string]int{"a": 1}); weak.Code != http.StatusNotModified {
		t.Fatalf("weak list: %d", weak.Code)
	}
	if changed := etagGet(tag, 0, map[string]int{"a": 2}); changed.Code != http.StatusOK || changed.Header().Get("ETag") == tag {
		t.Fatalf("changed: %d", changed.Code)
	}
}

func TestAbortWithETagDataFreshForAnonymous(t *testing.T) {
	w := etagGet("", 30*time.Second, []int{1})
	if got := w.Header().Get("Cache-Control"); got != "private, max-age=30" {
		t.Fatalf("cache-control = %q", got)
	}
	if again := etagGet(w.Header().Get("ETag"), 30*time.Second, []int{1}); again.Code != http.StatusNotModified || again.Header().Get("Cache-Control") != "private, max-age=30" {
		t.Fatalf("revalidated: %d %q", again.Code, again.Header().Get("Cache-Control"))
	}
}
