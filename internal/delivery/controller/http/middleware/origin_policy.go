package middleware

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/cybericebox/daemon/internal/config"
)

// TagChecker says whether an event with the tag exists (not archived).
type TagChecker interface {
	EventTagExists(ctx context.Context, tag string) bool
}

// OriginPolicy is the one allow-list of browser origins, shared by CORS and the origin guard: the
// platform hosts, and an event site <tag>.<EVENT_DOMAIN> only if an event with that tag exists
// (vpn., ctl., labs. and other unknown subdomains are refused). Tags nil: every one-label host
// under EVENT_DOMAIN counts (tests, tools).
type OriginPolicy struct {
	Hosts config.HostsConfig
	Tags  TagChecker
}

// Allowed reports whether origin may talk to the API; reason is for logs only.
func (p OriginPolicy) Allowed(ctx context.Context, origin string) (reason string, ok bool) {
	if reason, ok = p.Hosts.OriginAllowed(origin); !ok {
		return reason, false
	}
	if p.Tags == nil {
		return "", true
	}
	host := originHost(origin)
	if tag, isEventSite := p.Hosts.EventTag(host); isEventSite && !p.Tags.EventTagExists(ctx, tag) {
		return "no event has the tag " + tag, false
	}
	return "", true
}

// UnknownEventSite reports whether origin is a well-formed event site (<tag>.<EVENT_DOMAIN>) of an event
// that does not exist (deleted, or never was). Such a caller addressed an event tenant that is not there,
// which the API answers like every other missing event: 404, not the 403 of a foreign origin.
func (p OriginPolicy) UnknownEventSite(ctx context.Context, origin string) bool {
	if p.Tags == nil {
		return false
	}
	if _, ok := p.Hosts.OriginAllowed(origin); !ok {
		return false
	}
	tag, isEventSite := p.Hosts.EventTag(originHost(origin))
	return isEventSite && !p.Tags.EventTagExists(ctx, tag)
}

// EventTagCache answers TagChecker from a small in-memory cache over a lookup: a known tag is
// remembered for a minute, an unknown one for ten seconds (a new event is accepted within that, and
// a flood of random subdomains costs at most one lookup per tag per ten seconds, not one per request).
//
// The lookups themselves are bounded, because the origin check runs before any rate limiter and a
// flood of unique subdomains is otherwise a flood of database queries: a tag that cannot be an event
// tag is refused without a lookup, concurrent lookups of one tag share one query, and the lookups
// draw on a small budget; when it is empty a known tag is answered from its (possibly stale) entry and
// any other is refused for the moment. Past maxEventTags entries the cache drops the unknown ones
// first, so a flood does not evict the real event sites.
type EventTagCache struct {
	lookup func(ctx context.Context, tag string) (exists bool, err error)
	now    func() time.Time
	flight singleflight.Group

	mu      sync.Mutex
	entries map[string]tagEntry
	budget  lookupBudget
}

type tagEntry struct {
	exists  bool
	expires time.Time
}

// lookupBudget is a token bucket of database lookups.
type lookupBudget struct {
	tokens float64
	last   time.Time
}

func (b *lookupBudget) take(now time.Time) bool {
	if b.last.IsZero() {
		b.tokens = tagLookupBurst
	} else {
		b.tokens = min(tagLookupBurst, b.tokens+now.Sub(b.last).Seconds()*tagLookupsPerSecond)
	}
	b.last = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

const (
	knownTagTTL   = time.Minute
	unknownTagTTL = 10 * time.Second
	maxEventTags  = 10_000

	// tagLookupBurst and tagLookupsPerSecond are the database lookups the origin check may start: real
	// event sites are few and cached, so the budget is small.
	tagLookupBurst      = 30
	tagLookupsPerSecond = 10

	// An event tag is 3 to 64 characters of [a-z0-9] (the event model's rule).
	tagMinLen = 3
	tagMaxLen = 64
)

func NewEventTagCache(lookup func(ctx context.Context, tag string) (bool, error)) *EventTagCache {
	return &EventTagCache{lookup: lookup, now: time.Now, entries: map[string]tagEntry{}}
}

// Invalidate forgets everything (an event was created, deleted or retagged).
func (c *EventTagCache) Invalidate() {
	c.mu.Lock()
	c.entries = map[string]tagEntry{}
	c.mu.Unlock()
}

// possibleEventTag is the cheap pre-check: whether the label can be the tag of an event at all.
func possibleEventTag(tag string) bool {
	if len(tag) < tagMinLen || len(tag) > tagMaxLen {
		return false
	}
	for i := range len(tag) {
		if ch := tag[i]; (ch < 'a' || ch > 'z') && (ch < '0' || ch > '9') {
			return false
		}
	}
	return true
}

// EventTagExists is false when the lookup fails or its budget is spent: an origin that cannot be
// vouched for is refused.
func (c *EventTagCache) EventTagExists(ctx context.Context, tag string) bool {
	if !possibleEventTag(tag) {
		return false
	}
	now := c.now()
	c.mu.Lock()
	e, cached := c.entries[tag]
	if cached && now.Before(e.expires) {
		c.mu.Unlock()
		return e.exists
	}
	if !c.budget.take(now) {
		c.mu.Unlock()
		// Over budget: a tag seen before keeps its last answer, anything else waits.
		return cached && e.exists
	}
	c.mu.Unlock()
	v, err, _ := c.flight.Do(tag, func() (any, error) { return c.lookup(ctx, tag) })
	if err != nil {
		return false
	}
	exists, _ := v.(bool)
	ttl := unknownTagTTL
	if exists {
		ttl = knownTagTTL
	}
	c.mu.Lock()
	if _, present := c.entries[tag]; !present && len(c.entries) >= maxEventTags {
		c.evict(now)
	}
	c.entries[tag] = tagEntry{exists: exists, expires: now.Add(ttl)}
	c.mu.Unlock()
	return exists
}

// evict makes room: the expired and unknown entries go first, and only when the known ones alone fill
// the cache does it start over. The caller holds the mutex.
func (c *EventTagCache) evict(now time.Time) {
	for tag, e := range c.entries {
		if !e.exists || !now.Before(e.expires) {
			delete(c.entries, tag)
		}
	}
	if len(c.entries) >= maxEventTags {
		c.entries = map[string]tagEntry{}
	}
}

// PublicReadRoutes are the route templates whose GET/HEAD serve PUBLIC, unauthenticated data (public
// event info and content, the public scoreboard, public media, avatars, the OAuth and setup
// redirects): they are read by embeds, link previews and redirects from anywhere, so the origin guard
// does not look at their Origin or Referer (the CORS gate still refuses a foreign Origin: a foreign
// page cannot READ them with fetch). Everything else is authenticated and gets the full check. The
// set is the routes with no gate or only public gates; a test in the handler package compares it
// with the mounted router.
var PublicReadRoutes = map[string]bool{
	"/api/auth/avatar/:id":                         true,
	"/api/auth/google":                             true,
	"/api/auth/google/callback":                    true,
	"/api/auth/google/register":                    true,
	"/api/auth/google/setup":                       true,
	"/api/auth/password/policy":                    true,
	"/api/auth/setup":                              true,
	"/api/banners":                                 true,
	"/api/events/:id/content":                      true,
	"/api/events/:id/content-images/:fileID":       true,
	"/api/events/:id/content/document":             true,
	"/api/events/:id/content/pages":                true,
	"/api/events/:id/content/pages/:slug":          true,
	"/api/events/:id/content/pages/:slug/access":   true,
	"/api/events/:id/content/pages/:slug/document": true,
	"/api/events/:id/content/pages/:slug/values":   true,
	"/api/events/:id/content/values":               true,
	"/api/events/:id/favicon/:fileID":              true,
	"/api/events/:id/logo/:fileID":                 true,
	"/api/events/:id/preview-picture/:fileID":      true,
	"/api/events/:id/results":                      true,
	"/api/events/:id/results/changes":              true,
	"/api/events/:id/results/live":                 true,
	"/api/events/self/live-screen":                 true,
	"/api/events/self/live-screen/results":         true,
	"/api/events/self/live-screen/results/live":    true,
	"/api/events/self/public-info":                 true,
	"/api/events/upcoming":                         true,
	"/api/settings/:key/value":                     true,
}

func originHost(origin string) string {
	parsed, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
}
