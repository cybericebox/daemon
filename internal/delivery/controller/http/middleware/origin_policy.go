package middleware

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

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

// EventTagCache answers TagChecker from a small in-memory cache over a lookup: a known tag is
// remembered for a minute, an unknown one for ten seconds (a new event is accepted within that, and
// a flood of random subdomains costs one lookup per tag per ten seconds, not one per request).
// The cache is bounded: past maxEventTags entries it starts over.
type EventTagCache struct {
	lookup func(ctx context.Context, tag string) (exists bool, err error)
	now    func() time.Time

	mu      sync.Mutex
	entries map[string]tagEntry
}

type tagEntry struct {
	exists  bool
	expires time.Time
}

const (
	knownTagTTL   = time.Minute
	unknownTagTTL = 10 * time.Second
	maxEventTags  = 10_000
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

// EventTagExists is false when the lookup fails: an origin that cannot be vouched for is refused.
func (c *EventTagCache) EventTagExists(ctx context.Context, tag string) bool {
	now := c.now()
	c.mu.Lock()
	if e, ok := c.entries[tag]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e.exists
	}
	c.mu.Unlock()
	exists, err := c.lookup(ctx, tag)
	if err != nil {
		return false
	}
	ttl := unknownTagTTL
	if exists {
		ttl = knownTagTTL
	}
	c.mu.Lock()
	if len(c.entries) >= maxEventTags {
		c.entries = map[string]tagEntry{}
	}
	c.entries[tag] = tagEntry{exists: exists, expires: now.Add(ttl)}
	c.mu.Unlock()
	return exists
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
