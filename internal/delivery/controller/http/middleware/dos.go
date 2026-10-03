package middleware

import (
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/clienttoken"
)

// maxEventBuckets bounds the per-event buckets: public reads do not check the Origin against the existing
// events, so a made-up tag must not grow the map without limit. Past the cap new tags share one bucket.
const maxEventBuckets = 4096

const otherEventKey = "\x00other"

// authGroupRoutes are the credential entry routes: they get a shared bucket of their own.
var authGroupRoutes = map[string]bool{
	"/api/auth/sign-in":                true,
	"/api/auth/sign-up":                true,
	"/api/auth/password/reset-request": true,
	"/api/auth/password/reset":         true,
}

// openWithoutClientToken are the anonymous routes a browser without the client cookie may still call: the
// token endpoint itself, public media (an <img> cannot fetch a token first) and the Google sign-in
// navigations (a redirect, not a fetch).
var openWithoutClientToken = map[string]bool{
	clienttoken.Path:            true,
	"/api/auth/google":          true,
	"/api/auth/google/register": true,
	"/api/auth/google/setup":    true,
	"/api/auth/google/callback": true,
}

func openWithoutToken(route string) bool {
	return openWithoutClientToken[route] || PublicMediaRoutes[route]
}

// dosLimiter is the anonymous limiter of DOS_PROTECTION=on. A request is counted:
//   - the token endpoint, and any request without a valid client cookie: in the small shared no-token
//     bucket; without the cookie only the routes in openWithoutToken pass at all;
//   - with a valid cookie: in that browser's own bucket, then in the shared bucket of its group (the
//     credential entry routes) or of its event (public pages of an event site, by the Origin's tag).
//
// Nothing is keyed on a client address.
type dosLimiter struct {
	cfg    config.DOSConfig
	hosts  config.HostsConfig
	signer *clienttoken.Signer

	mu        sync.Mutex
	noToken   bucket
	auth      bucket
	clients   map[string]*bucket
	events    map[string]*bucket
	lastSweep time.Time
}

// EnableDOSProtection switches the anonymous limiter to the client-token mode.
func (l *RateLimiter) EnableDOSProtection(cfg config.DOSConfig, hosts config.HostsConfig, signer *clienttoken.Signer) {
	l.dos = &dosLimiter{cfg: cfg, hosts: hosts, signer: signer,
		clients: map[string]*bucket{}, events: map[string]*bucket{}}
}

// admitAnonymous counts an anonymous request and aborts it with 429 when it is not admitted.
func (l *RateLimiter) admitAnonymous(ctx *gin.Context) bool {
	if l.dos == nil {
		ok, wait := l.allow(nil)
		if !ok {
			l.refuse(ctx, "anonymous", wait)
		}
		return ok
	}
	name, wait := l.dos.admit(ctx, l.now())
	if name != "" {
		if name == "client-token-required" {
			ctx.Header(clienttoken.RequiredHeader, "required")
		}
		l.refuse(ctx, name, wait)
		return false
	}
	return true
}

// admit returns the name of the limiter that refuses the request, "" when it is admitted.
func (d *dosLimiter) admit(ctx *gin.Context, now time.Time) (string, time.Duration) {
	route := ctx.FullPath()
	id, hasToken := d.signer.FromRequest(ctx)

	d.mu.Lock()
	defer d.mu.Unlock()
	d.sweep(now)

	if route == clienttoken.Path || !hasToken {
		if !hasToken && !openWithoutToken(route) {
			return "client-token-required", time.Second
		}
		if ok, wait := d.noToken.take(now, d.cfg.NoTokenPerMinute, d.cfg.NoTokenBurst); !ok {
			return "no-client-token", wait
		}
		if !hasToken {
			return "", 0
		}
	}

	b := d.clients[id]
	if b == nil {
		b = &bucket{}
		d.clients[id] = b
	}
	if ok, wait := b.take(now, d.cfg.ClientPerMinute, d.cfg.ClientBurst); !ok {
		return "client", wait
	}
	if authGroupRoutes[route] {
		if ok, wait := d.auth.take(now, d.cfg.AuthPerMinute, d.cfg.AuthBurst); !ok {
			return "auth-group", wait
		}
		return "", 0
	}
	if tag := d.eventTag(ctx); tag != "" {
		eb := d.events[tag]
		if eb == nil {
			if len(d.events) >= maxEventBuckets {
				tag = otherEventKey
				eb = d.events[tag]
			}
			if eb == nil {
				eb = &bucket{}
				d.events[tag] = eb
			}
		}
		if ok, wait := eb.take(now, d.cfg.EventPerMinute, d.cfg.EventBurst); !ok {
			return "event", wait
		}
	}
	return "", 0
}

// eventTag is the tag of the event site the request comes from (its Origin), "" for a platform host.
func (d *dosLimiter) eventTag(ctx *gin.Context) string {
	origin := ctx.GetHeader("Origin")
	if origin == "" || ctx.Request.Method == http.MethodOptions {
		return ""
	}
	u, err := url.Parse(origin)
	if err != nil {
		return ""
	}
	tag, _ := d.hosts.EventTag(u.Hostname())
	return tag
}

// sweep forgets the buckets that are full again, once a minute, so the maps stay small.
func (d *dosLimiter) sweep(now time.Time) {
	if now.Sub(d.lastSweep) <= time.Minute {
		return
	}
	d.lastSweep = now
	idle := func(burst, perMinute int) time.Duration {
		return time.Duration(float64(burst) / float64(perMinute) * float64(time.Minute))
	}
	for id, b := range d.clients {
		if now.Sub(b.last) > idle(d.cfg.ClientBurst, d.cfg.ClientPerMinute) {
			delete(d.clients, id)
		}
	}
	for tag, b := range d.events {
		if now.Sub(b.last) > idle(d.cfg.EventBurst, d.cfg.EventPerMinute) {
			delete(d.events, tag)
		}
	}
}
