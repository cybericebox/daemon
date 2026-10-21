package middleware

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	siteModel "github.com/cybericebox/daemon/internal/model/site"
)

// Tags of an event site are resolved to the event id through a lookup that is cached for a short while,
// so the per-tab pulse does not cost a query each time.
const (
	siteTagTTL      = 30 * time.Second
	siteTagMissTTL  = 10 * time.Second
	siteTagMaxItems = 10_000
)

// SiteResolver names the site a request comes from: the explicit site (and event) query parameters
// first, the Origin header when there is no site parameter. The Referer is never used.
type SiteResolver struct {
	hosts  config.HostsConfig
	lookup func(ctx context.Context, tag string) (uuid.UUID, error)
	now    func() time.Time
	flight singleflight.Group

	mu      sync.Mutex
	entries map[string]siteTagEntry
}

type siteTagEntry struct {
	id      uuid.UUID
	found   bool
	expires time.Time
}

// NewSiteResolver builds the resolver. lookup returns the id of the event with the tag, or
// eventModel.ErrEventNotFound.
func NewSiteResolver(hosts config.HostsConfig, lookup func(ctx context.Context, tag string) (uuid.UUID, error)) *SiteResolver {
	return &SiteResolver{hosts: hosts, lookup: lookup, now: time.Now, entries: map[string]siteTagEntry{}}
}

type siteCtxKey struct{}

// SiteFromContext returns the site that ResolveSite stored.
func SiteFromContext(ctx context.Context) (siteModel.Site, bool) {
	site, ok := ctx.Value(siteCtxKey{}).(siteModel.Site)
	return site, ok
}

// ContextWithSite stores the site in ctx.
func ContextWithSite(ctx context.Context, site siteModel.Site) context.Context {
	return context.WithValue(ctx, siteCtxKey{}, site)
}

var (
	errSiteUnknown = errors.New("unknown site: pass the site query parameter or call from a platform site")
	errSiteEvent   = errors.New("invalid site and event context")
)

// Resolve is the pure part: query is the request query, origin the Origin header. An event site
// without a known event is eventModel.ErrEventNotFound; any other failure is a bad request.
func (r *SiteResolver) Resolve(ctx context.Context, query url.Values, origin string) (siteModel.Site, error) {
	kinds, hasSite := query["site"]
	events, hasEvent := query["event"]
	if hasSite && (len(kinds) != 1 || !siteModel.ValidKind(kinds[0])) {
		return siteModel.Site{}, errSiteEvent
	}
	if hasEvent && (len(events) != 1 || events[0] == "") {
		return siteModel.Site{}, errSiteEvent
	}
	if hasSite || hasEvent {
		kind := siteModel.Event
		if hasSite {
			kind = kinds[0]
		}
		return siteFromParams(kind, events, hasEvent)
	}
	return r.fromOrigin(ctx, origin)
}

// siteFromParams is the site named by the site and event parameters: only an event site has an event.
func siteFromParams(kind string, events []string, hasEvent bool) (siteModel.Site, error) {
	if kind != siteModel.Event {
		if hasEvent {
			return siteModel.Site{}, errSiteEvent
		}
		return siteModel.Site{Kind: kind}, nil
	}
	if !hasEvent {
		return siteModel.Site{}, errSiteEvent
	}
	id, err := uuid.FromString(events[0])
	if err != nil || id == uuid.Nil {
		return siteModel.Site{}, errSiteEvent
	}
	return siteModel.Site{Kind: kind, EventID: &id}, nil
}

func (r *SiteResolver) fromOrigin(ctx context.Context, origin string) (siteModel.Site, error) {
	if origin == "" {
		return siteModel.Site{}, errSiteUnknown
	}
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		return siteModel.Site{}, errSiteUnknown
	}
	host := strings.ToLower(strings.TrimSuffix(parsed.Hostname(), "."))
	switch host {
	case r.hosts.Main:
		return siteModel.Site{Kind: siteModel.Main}, nil
	case r.hosts.ID:
		return siteModel.Site{Kind: siteModel.ID}, nil
	case r.hosts.Admin:
		return siteModel.Site{Kind: siteModel.Admin}, nil
	case r.hosts.Exercises:
		return siteModel.Site{Kind: siteModel.Exercises}, nil
	}
	tag, ok := r.hosts.EventTag(host)
	if !ok {
		return siteModel.Site{}, errSiteUnknown
	}
	id, err := r.eventID(ctx, tag)
	if err != nil {
		return siteModel.Site{}, err
	}
	return siteModel.Site{Kind: siteModel.Event, EventID: &id}, nil
}

func (r *SiteResolver) eventID(ctx context.Context, tag string) (uuid.UUID, error) {
	now := r.now()
	r.mu.Lock()
	e, cached := r.entries[tag]
	r.mu.Unlock()
	if cached && now.Before(e.expires) {
		if !e.found {
			return uuid.Nil, eventModel.ErrEventNotFound.Err()
		}
		return e.id, nil
	}
	v, err, _ := r.flight.Do(tag, func() (any, error) {
		return r.lookup(context.WithoutCancel(ctx), tag)
	})
	if err != nil {
		if !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
			return uuid.Nil, err
		}
		r.store(tag, siteTagEntry{expires: now.Add(siteTagMissTTL)})
		return uuid.Nil, err
	}
	id, _ := v.(uuid.UUID)
	r.store(tag, siteTagEntry{id: id, found: true, expires: now.Add(siteTagTTL)})
	return id, nil
}

func (r *SiteResolver) store(tag string, e siteTagEntry) {
	r.mu.Lock()
	if len(r.entries) >= siteTagMaxItems {
		r.entries = map[string]siteTagEntry{}
	}
	r.entries[tag] = e
	r.mu.Unlock()
}

// ResolveSite is the middleware: it stores the site in the request context. A request that names no
// site and has no platform Origin is a 400; an event site of no event is a 404 like every missing event.
func (r *SiteResolver) ResolveSite() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		site, err := r.Resolve(ctx.Request.Context(), ctx.Request.URL.Query(), ctx.GetHeader("Origin"))
		if err != nil {
			switch {
			case errors.Is(err, eventModel.ErrEventNotFound.Err()):
				response.AbortWithNotFound(ctx)
			case errors.Is(err, errSiteUnknown), errors.Is(err, errSiteEvent):
				response.AbortWithBadRequest(ctx, fmt.Errorf("%w", err))
			default:
				response.AbortWithError(ctx, err)
			}
			return
		}
		ctx.Request = ctx.Request.WithContext(ContextWithSite(ctx.Request.Context(), site))
		ctx.Next()
	}
}
