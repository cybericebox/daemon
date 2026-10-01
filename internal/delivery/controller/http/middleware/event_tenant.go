// Package middleware also implements Origin-based event-tenant resolution:
// event-participant routes live on a per-event subdomain (https://<tag>.<apex>)
// rather than a fixed host, so the tenant (which live event a request belongs
// to) cannot be read off the request path the way api.<domain> is. Instead it
// is resolved from the Origin header — the same header CORS (cors.go) already
// validates as a platform subdomain for credentialed cross-origin calls — by
// reading its subdomain label as the event tag and loading the live event for
// it. The resolved tenant travels through the request context as ONE bundle
// (EventTenant), mirroring rbac.Claims.
package middleware

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// EventTenant is the resolved tenant event for a request — the one bundle
// carried through the request context. There are deliberately no per-field
// producers/getters (mirrors rbac.Claims).
type EventTenant struct {
	EventID       uuid.UUID
	Tag           string
	Public        bool
	AvailableFrom time.Time
	ArchiveAt     time.Time
}

type eventTenantCtxKey struct{}

// ContextWithEventTenant stores the resolved tenant event in ctx.
func ContextWithEventTenant(ctx context.Context, tenant EventTenant) context.Context {
	return context.WithValue(ctx, eventTenantCtxKey{}, tenant)
}

// EventTenantFromContext returns the request's resolved tenant event and
// whether one is present (fail-closed — callers reject when absent).
func EventTenantFromContext(ctx context.Context) (EventTenant, bool) {
	tenant, ok := ctx.Value(eventTenantCtxKey{}).(EventTenant)
	return tenant, ok
}

// EventResolver resolves the live tenant event for a subdomain tag. Defined
// here (not reusing a useCase type) so this middleware package stays free of
// an internal/useCase import in its core handler logic; NewEventTenantResolver
// below adapts the event use case to this interface.
type EventResolver interface {
	ResolveEventByTag(ctx context.Context, tag string, now time.Time) (EventTenant, error)
	RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error
}

// ResolveEventTenant returns a gin middleware that resolves the tenant event
// from the request's Origin header (its label under EVENT_DOMAIN is the
// event tag) and stores it in the request context as EventTenant. Requests
// with no/invalid Origin, a reserved or apex subdomain, or an unknown tag are
// rejected with a bare 404 — participant routes never reveal which case
// applied (mirrors the anti-enumeration category-A error convention).
func ResolveEventTenant(resolver EventResolver, hosts config.HostsConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		origin := ctx.GetHeader("Origin")
		if origin == "" {
			response.AbortWithNotFound(ctx)
			return
		}

		parsed, err := url.Parse(origin)
		if err != nil || parsed.Host == "" {
			response.AbortWithNotFound(ctx)
			return
		}
		host := strings.ToLower(parsed.Hostname())

		tag, ok := hosts.EventTag(host)
		if !ok {
			response.AbortWithNotFound(ctx)
			return
		}

		tenant, err := resolver.ResolveEventByTag(ctx.Request.Context(), tag, time.Now())
		if err != nil {
			if errors.Is(err, eventModel.ErrEventNotFound.Err()) {
				response.AbortWithNotFound(ctx)
				return
			}
			response.AbortWithError(ctx, err)
			return
		}
		if !tenant.Public {
			claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
			if !ok {
				response.AbortWithNotFound(ctx)
				return
			}
			if err := resolver.RequireReadEvent(ctx.Request.Context(), tenant.EventID, claims.UserID); err != nil {
				if errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()) {
					response.AbortWithNotFound(ctx)
				} else {
					response.AbortWithError(ctx, err)
				}
				return
			}
		}

		ctx.Request = ctx.Request.WithContext(ContextWithEventTenant(ctx.Request.Context(), tenant))
		ctx.Next()
	}
}

// eventUseCase is the narrow port NewEventTenantResolver needs from the event
// application layer. *event.EventUseCase satisfies it structurally.
type eventUseCase interface {
	ResolveEventByTag(ctx context.Context, tag string, now time.Time) (event.EventTenantView, error)
	RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error
}

// eventUseCaseResolver adapts eventUseCase to EventResolver. It is the "thin
// adapter over EventUseCase" that lets the event use case return its own
// view type (internal/useCase must not depend on this delivery package)
// while still satisfying EventResolver here.
type eventUseCaseResolver struct {
	uc eventUseCase
}

// NewEventTenantResolver builds the EventResolver ResolveEventTenant needs
// from the event use case. Construction lives here (not in the use case) so
// internal/useCase never imports a delivery package.
func NewEventTenantResolver(uc eventUseCase) EventResolver {
	return eventUseCaseResolver{uc: uc}
}

func (r eventUseCaseResolver) ResolveEventByTag(ctx context.Context, tag string, now time.Time) (EventTenant, error) {
	v, err := r.uc.ResolveEventByTag(ctx, tag, now)
	if err != nil {
		return EventTenant{}, err
	}
	return EventTenant{
		EventID:       v.EventID,
		Tag:           v.Tag,
		Public:        v.Public,
		AvailableFrom: v.AvailableFrom,
		ArchiveAt:     v.ArchiveAt,
	}, nil
}

func (r eventUseCaseResolver) RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error {
	return r.uc.RequireReadEvent(ctx, eventID, userID)
}
