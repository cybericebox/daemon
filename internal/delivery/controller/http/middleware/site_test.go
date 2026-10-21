package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	siteModel "github.com/cybericebox/daemon/internal/model/site"
)

var siteHosts = config.HostsConfig{
	Main: "example.test", API: "api.example.test", ID: "id.example.test", Admin: "admin.example.test",
	Exercises: "exercises.example.test", EventDomain: "example.test",
}

func newSiteResolver(events map[string]uuid.UUID, lookups *int) *SiteResolver {
	return NewSiteResolver(siteHosts, func(_ context.Context, tag string) (uuid.UUID, error) {
		*lookups++
		if id, ok := events[tag]; ok {
			return id, nil
		}
		return uuid.Nil, eventModel.ErrEventNotFound.Err()
	})
}

func TestSiteFromOrigin(t *testing.T) {
	event := uuid.Must(uuid.NewV7())
	lookups := 0
	r := newSiteResolver(map[string]uuid.UUID{"spring-ctf": event}, &lookups)
	for origin, want := range map[string]string{
		"https://example.test":           siteModel.Main,
		"https://id.example.test":        siteModel.ID,
		"https://admin.example.test":     siteModel.Admin,
		"https://exercises.example.test": siteModel.Exercises,
		"https://ID.Example.test:443":    siteModel.ID,
	} {
		site, err := r.Resolve(context.Background(), url.Values{}, origin)
		if err != nil || site.Kind != want || site.EventID != nil {
			t.Fatalf("%s: %+v %v", origin, site, err)
		}
	}
	site, err := r.Resolve(context.Background(), url.Values{}, "https://spring-ctf.example.test")
	if err != nil || site.Kind != siteModel.Event || site.EventID == nil || *site.EventID != event {
		t.Fatalf("event site: %+v %v", site, err)
	}
	// The event tag is looked up once, then served from the cache.
	for range 5 {
		if _, err = r.Resolve(context.Background(), url.Values{}, "https://spring-ctf.example.test"); err != nil {
			t.Fatal(err)
		}
	}
	if lookups != 1 {
		t.Fatalf("lookups = %d, want 1", lookups)
	}
}

func TestSiteFromOriginRefusesUnknownOrigins(t *testing.T) {
	lookups := 0
	r := newSiteResolver(nil, &lookups)
	for _, origin := range []string{"", "null", "http://[bad", "https://api.example.test", "https://evil.test", "https://a.b.example.test", "https://www.example.test"} {
		if _, err := r.Resolve(context.Background(), url.Values{}, origin); err == nil || errors.Is(err, eventModel.ErrEventNotFound.Err()) {
			t.Fatalf("%q: %v", origin, err)
		}
	}
	if _, err := r.Resolve(context.Background(), url.Values{}, "https://gone.example.test"); !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
		t.Fatalf("missing event site: %v", err)
	}
}

func TestSiteQueryWinsOverOriginAndRefererIsIgnored(t *testing.T) {
	event := uuid.Must(uuid.NewV7())
	lookups := 0
	r := newSiteResolver(nil, &lookups)
	site, err := r.Resolve(context.Background(), url.Values{"site": {"admin"}}, "https://id.example.test")
	if err != nil || site.Kind != siteModel.Admin {
		t.Fatalf("site param: %+v %v", site, err)
	}
	site, err = r.Resolve(context.Background(), url.Values{"site": {"event"}, "event": {event.String()}}, "")
	if err != nil || site.Kind != siteModel.Event || *site.EventID != event {
		t.Fatalf("event param: %+v %v", site, err)
	}
	site, err = r.Resolve(context.Background(), url.Values{"event": {event.String()}}, "")
	if err != nil || site.Kind != siteModel.Event {
		t.Fatalf("event alone means the event site: %+v %v", site, err)
	}
	if lookups != 0 {
		t.Fatal("explicit parameters must not look anything up")
	}
	for _, q := range []url.Values{
		{"site": {"nope"}}, {"site": {""}}, {"site": {"main", "id"}}, {"site": {"main"}, "event": {event.String()}},
		{"site": {"event"}}, {"event": {""}}, {"event": {"bad"}}, {"event": {uuid.Nil.String()}}, {"site": {"event"}, "event": {event.String(), event.String()}},
	} {
		if _, err = r.Resolve(context.Background(), q, "https://example.test"); err == nil {
			t.Fatalf("%v accepted", q)
		}
	}

	// The Referer alone never names a site.
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	router.GET("/x", r.ResolveSite(), func(c *gin.Context) {
		got, _ := SiteFromContext(c.Request.Context())
		c.String(http.StatusOK, got.Kind)
	})
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Referer", "https://example.test/page")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("referer only: %d", w.Code)
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://exercises.example.test")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK || w.Body.String() != "exercises" {
		t.Fatalf("origin: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/x", nil)
	req.Header.Set("Origin", "https://gone.example.test")
	router.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("missing event site: %d", w.Code)
	}
}
