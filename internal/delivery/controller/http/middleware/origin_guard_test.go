package middleware_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
)

// knownTags is the set of event tags that exist in these tests.
type knownTags map[string]bool

func (k knownTags) EventTagExists(_ context.Context, tag string) bool { return k[tag] }

func guardRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	hosts := config.HostsConfig{Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test",
		Exercises: "exercises.example.test", EventDomain: "events.example.test"}
	r := gin.New()
	r.Use(middleware.OriginGuardWith(middleware.OriginPolicy{Hosts: hosts, Tags: knownTags{"ctf": true}}))
	ok := func(c *gin.Context) { c.Status(http.StatusOK) }
	for _, m := range []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"} {
		r.Handle(m, "/x", ok)
	}
	// a public read: PublicReadRoutes has the avatar route
	r.GET("/api/auth/avatar/:id", ok)
	r.GET("/api/events/:id/results", ok)
	return r
}

// send builds a request; a header with the value "<absent>" is left out, any other (even "") is set.
func send(r *gin.Engine, method, body string, headers map[string]string) int {
	req := httptest.NewRequest(method, "/x", strings.NewReader(body))
	for k, v := range headers {
		req.Header[k] = []string{v}
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w.Code
}

var (
	allMethods   = []string{"GET", "HEAD", "OPTIONS", "POST", "PUT", "PATCH", "DELETE"}
	writeMethods = []string{"POST", "PUT", "PATCH", "DELETE"}
	jsonType     = "application/json"
)

func bodyFor(method string) string {
	for _, w := range writeMethods {
		if method == w {
			return `{}`
		}
	}
	return ""
}

// The allow-list: MAIN, ID, ADMIN, EXERCISES, API and one-label event sites, https only. Every method.
func TestOriginGuard_AllowedOriginsPassForEveryMethod(t *testing.T) {
	r := guardRouter()
	for _, origin := range []string{
		"https://example.test", "https://id.example.test", "https://admin.example.test", "https://exercises.example.test",
		"https://api.example.test", "https://ctf.events.example.test", // the tag exists
	} {
		for _, m := range allMethods {
			if got := send(r, m, bodyFor(m), map[string]string{"Origin": origin, "Content-Type": jsonType}); got != http.StatusOK {
				t.Errorf("%s from %s: %d", m, origin, got)
			}
		}
	}
}

func TestOriginGuard_RefusesEveryOtherOriginForEveryMethod(t *testing.T) {
	r := guardRouter()
	for name, origin := range map[string]string{
		"lab device page":               "https://web-x.labs.example.test",
		"labs domain itself":            "https://labs.example.test",
		"two labels under EVENT_DOMAIN": "https://a.b.events.example.test",
		"EVENT_DOMAIN itself":           "https://events.example.test",
		"lookalike":                     "https://evil-example.test",
		"suffix trick":                  "https://id.example.test.evil.com",
		"prefix trick":                  "https://xid.example.test",
		"http, not https":               "http://id.example.test",
		"http event site":               "http://ctf.events.example.test",
		"credentials":                   "https://user@id.example.test",
		"unrelated":                     "https://evil.test",
	} {
		for _, m := range allMethods {
			if got := send(r, m, bodyFor(m), map[string]string{"Origin": origin, "Content-Type": jsonType}); got != http.StatusForbidden {
				t.Errorf("%s: %s from %s: %d, want 403", name, m, origin, got)
			}
		}
	}
}

// "null" (sandboxed frames, data: and file: pages, some redirects) names a source: present and never allowed.
func TestOriginGuard_NullOriginIsPresentAndRefused(t *testing.T) {
	r := guardRouter()
	for _, m := range allMethods {
		if got := send(r, m, bodyFor(m), map[string]string{"Origin": "null", "Content-Type": jsonType}); got != http.StatusForbidden {
			t.Errorf("%s with Origin: null: %d, want 403 (it must not fall into the 'absent' path)", m, got)
		}
		// even a GET that would pass without an Origin, and even with an allowed Referer next to it
		if got := send(r, m, bodyFor(m), map[string]string{"Origin": "null", "Referer": "https://id.example.test/x", "Content-Type": jsonType}); got != http.StatusForbidden {
			t.Errorf("%s with Origin: null and a good Referer: %d, want 403", m, got)
		}
	}
	if got := send(r, "GET", "", map[string]string{"Origin": ""}); got != http.StatusForbidden {
		t.Errorf("an empty Origin header is present, not absent: %d", got)
	}
}

// No Origin: the Referer names the source.
func TestOriginGuard_RefererIsCheckedWhenOriginIsAbsent(t *testing.T) {
	r := guardRouter()
	for _, m := range allMethods {
		good := map[string]string{"Referer": "https://id.example.test/profile?x=1", "Content-Type": jsonType}
		if got := send(r, m, bodyFor(m), good); got != http.StatusOK {
			t.Errorf("%s with an allowed Referer: %d", m, got)
		}
		for _, bad := range []string{"https://web-x.labs.example.test/page", "http://id.example.test/", "https://evil.test/", "https://a.b.events.example.test/"} {
			if got := send(r, m, bodyFor(m), map[string]string{"Referer": bad, "Content-Type": jsonType}); got != http.StatusForbidden {
				t.Errorf("%s with Referer %s: %d, want 403", m, bad, got)
			}
		}
	}
	// the Origin wins over the Referer
	if got := send(r, "POST", `{}`, map[string]string{"Origin": "https://evil.test", "Referer": "https://id.example.test/", "Content-Type": jsonType}); got != http.StatusForbidden {
		t.Errorf("a bad Origin with a good Referer: %d", got)
	}
}

// Neither header: navigations, the event frontend's server-side fetches (INTERNAL_API_ORIGIN) and health checks pass;
// writes do not.
func TestOriginGuard_NamelessRequests(t *testing.T) {
	r := guardRouter()
	for _, m := range []string{"GET", "HEAD", "OPTIONS"} {
		if got := send(r, m, "", nil); got != http.StatusOK {
			t.Errorf("%s with neither Origin nor Referer: %d, want pass", m, got)
		}
	}
	for _, m := range writeMethods {
		if got := send(r, m, bodyFor(m), map[string]string{"Content-Type": jsonType}); got != http.StatusForbidden {
			t.Errorf("%s with neither Origin nor Referer: %d, want 403", m, got)
		}
	}
}

// A cross-origin form post cannot send JSON: a write with a body in another type never reaches a JSON handler.
func TestOriginGuard_WriteBodyMustBeJSONOrMultipart(t *testing.T) {
	r := guardRouter()
	origin := "https://id.example.test"
	for _, ct := range []string{"text/plain", "application/x-www-form-urlencoded", "", "application/jsonx", "text/html"} {
		h := map[string]string{"Origin": origin}
		if ct != "" {
			h["Content-Type"] = ct
		}
		if got := send(r, "POST", `{"a":1}`, h); got != http.StatusUnsupportedMediaType {
			t.Errorf("content type %q: %d, want 415", ct, got)
		}
	}
	if got := send(r, "POST", `{"a":1}`, map[string]string{"Origin": origin, "Content-Type": "application/json; charset=utf-8"}); got != http.StatusOK {
		t.Errorf("json with a charset: %d", got)
	}
	if got := send(r, "POST", "--b\r\n--b--", map[string]string{"Origin": origin, "Content-Type": "multipart/form-data; boundary=b"}); got != http.StatusOK {
		t.Errorf("an upload is multipart: %d", got)
	}
	if got := send(r, "DELETE", "", map[string]string{"Origin": origin}); got != http.StatusOK {
		t.Errorf("a bodiless write needs no content type: %d", got)
	}
}

// Public reads (gate "-" or public gates: public event info, the scoreboard, public media, avatars)
// are not checked: embeds and referrers must never break.
func TestOriginGuard_PublicReadsAreNotChecked(t *testing.T) {
	r := guardRouter()
	for _, path := range []string{"/api/auth/avatar/x", "/api/events/e1/results"} {
		for name, h := range map[string]map[string]string{
			"foreign referer": {"Referer": "https://mail.google.com/mail/u/0/"},
			"lab referer":     {"Referer": "https://web-x.labs.example.test/"},
			"no headers":      nil,
			"no-cors embed":   {"Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"},
			"foreign origin":  {"Origin": "https://evil.test"}, // the CORS gate, an earlier middleware, decides what a foreign page may READ
			"unknown tag":     {"Origin": "https://nosuch.events.example.test"},
			"origin null":     {"Origin": "null"},
		} {
			for _, m := range []string{"GET"} { // gin mounts no HEAD for these routes
				req := httptest.NewRequest(m, path, nil)
				for k, v := range h {
					req.Header[k] = []string{v}
				}
				w := httptest.NewRecorder()
				r.ServeHTTP(w, req)
				if w.Code != http.StatusOK {
					t.Errorf("%s %s (%s): %d, want pass", m, path, name, w.Code)
				}
			}
		}
	}
	// a public route's WRITE is still checked
	if got := send(r, "POST", `{}`, map[string]string{"Referer": "https://mail.google.com/", "Content-Type": jsonType}); got != http.StatusForbidden {
		t.Errorf("a write is never public: %d", got)
	}
}

// An authenticated GET from a lab page is refused, whatever the way it names its source.
func TestOriginGuard_AuthenticatedReadFromALabPageIsRefused(t *testing.T) {
	r := guardRouter()
	for _, h := range []map[string]string{
		{"Origin": "https://web-x.labs.example.test"},
		{"Referer": "https://web-x.labs.example.test/page"},
		{"Origin": "null"},
		{"Origin": "https://nosuch.events.example.test"},
	} {
		if got := send(r, "GET", "", h); got != http.StatusForbidden {
			t.Errorf("%v: %d, want 403", h, got)
		}
	}
}

// An authenticated URL cannot be loaded as an <img>/<script> by a page that names no source;
// top-level navigations, downloads and server-side fetches pass.
func TestOriginGuard_AuthenticatedNoCorsLoadIsRefused(t *testing.T) {
	r := guardRouter()
	if got := send(r, "GET", "", map[string]string{"Sec-Fetch-Mode": "no-cors", "Sec-Fetch-Dest": "image"}); got != http.StatusForbidden {
		t.Errorf("no-cors image load: %d, want 403", got)
	}
	for name, h := range map[string]map[string]string{
		"navigation":      {"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document"},
		"download":        {"Sec-Fetch-Mode": "navigate", "Sec-Fetch-Dest": "document", "Sec-Fetch-Site": "same-site"},
		"same-origin xhr": {"Sec-Fetch-Mode": "cors", "Sec-Fetch-Site": "same-origin"},
		"server fetch":    nil,
	} {
		if got := send(r, "GET", "", h); got != http.StatusOK {
			t.Errorf("%s: %d, want pass", name, got)
		}
	}
	// a no-cors load that names a platform page as its Referer is that page's own load
	if got := send(r, "GET", "", map[string]string{"Sec-Fetch-Mode": "no-cors", "Referer": "https://id.example.test/profile"}); got != http.StatusOK {
		t.Errorf("our own page's image load: %d", got)
	}
}

func TestEventTagCache_CachesAndRefusesWhenTheLookupFails(t *testing.T) {
	calls := 0
	exists := map[string]bool{"ctf": true}
	failing := false
	c := middleware.NewEventTagCache(func(_ context.Context, tag string) (bool, error) {
		calls++
		if failing {
			return false, context.DeadlineExceeded
		}
		return exists[tag], nil
	})
	ctx := context.Background()
	if !c.EventTagExists(ctx, "ctf") || !c.EventTagExists(ctx, "ctf") || calls != 1 {
		t.Fatalf("a known tag is looked up once, calls=%d", calls)
	}
	if c.EventTagExists(ctx, "nosuch") || c.EventTagExists(ctx, "nosuch") || calls != 2 {
		t.Fatalf("an unknown tag is remembered briefly, calls=%d", calls)
	}
	exists["nosuch"] = true // an event with that tag is created
	c.Invalidate()
	if !c.EventTagExists(ctx, "nosuch") {
		t.Fatal("after an invalidation a new event is accepted")
	}
	failing = true
	c.Invalidate()
	if c.EventTagExists(ctx, "ctf") {
		t.Fatal("a lookup error must refuse the origin")
	}
}

// M7: a flood of unique subdomains does not become a flood of lookups.
func TestEventTagCache_BoundsTheLookups(t *testing.T) {
	calls := 0
	c := middleware.NewEventTagCache(func(_ context.Context, tag string) (bool, error) {
		calls++
		return tag == "ctf", nil
	})
	ctx := context.Background()
	for _, tag := range []string{"", "ab", "UPPER", "with-dash", "a.b", strings.Repeat("a", 65)} {
		if c.EventTagExists(ctx, tag) {
			t.Fatalf("%q cannot be an event tag", tag)
		}
	}
	if calls != 0 {
		t.Fatalf("a label that cannot be a tag costs no lookup, calls=%d", calls)
	}
	if !c.EventTagExists(ctx, "ctf") {
		t.Fatal("a known tag is accepted")
	}
	before := calls
	for i := range 1000 {
		c.EventTagExists(ctx, fmt.Sprintf("flood%d", i))
	}
	if calls-before > 40 {
		t.Fatalf("1000 unique tags started %d lookups, the budget caps them", calls-before)
	}
	// With the budget spent a known tag still answers from its entry.
	if !c.EventTagExists(ctx, "ctf") {
		t.Fatal("a known tag must survive a flood")
	}
}
