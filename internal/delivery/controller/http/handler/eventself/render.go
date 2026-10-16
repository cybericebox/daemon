package eventself

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/eventrender"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUC "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type EventRenderUseCase interface {
	ResolveEventByTag(context.Context, string, time.Time) (eventUC.EventTenantView, error)
	RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error
	GetEventRenderManifest(context.Context, uuid.UUID, string, bool, time.Time) (eventUC.EventRenderManifestView, error)
	GetEventRenderPage(context.Context, uuid.UUID, string, string, bool, time.Time) (eventUC.EventRenderPageView, error)
}
type EventRenderHandler struct {
	uc        EventRenderUseCase
	prot      IProtection
	hosts     config.HostsConfig
	signer    *eventrender.Signer
	now       func() time.Time
	wait      func(context.Context, time.Duration) error
	admission chan struct{}
}

func NewEventRenderAPIHandler(uc EventRenderUseCase, prot IProtection, cfg config.AuthConfig) *EventRenderHandler {
	signer, err := eventrender.New(cfg.SessionEncryptionKey)
	if err != nil {
		panic(fmt.Errorf("configure event render signer: %w", err))
	}
	return &EventRenderHandler{uc: uc, prot: prot, hosts: cfg.Hosts, signer: signer, now: time.Now, wait: waitForRenderDenial, admission: make(chan struct{}, renderMaxInFlight)}
}
func (h *EventRenderHandler) Init(router *gin.RouterGroup) {
	router.GET("auth/event-render-key", h.noStore, h.prot.RequirePermission(rbac.PermEventContentRead), h.key)
	// Issuance retains the ordinary API session gate and tenant/staff policy.
	router.POST("events/self/render-grant", h.noStore, h.prot.RequirePermission(rbac.PermSelf), h.resolveGrantTenant, h.grant)
	// Grants are consumed only inside these handlers. They never inject rbac.Claims,
	// so even a valid render token cannot authenticate any other route or mutation.
	reads := router.Group("events/self", h.noStore, h.prot.RequirePermission(rbac.PermEventContentRead))
	reads.GET("render-manifest", h.manifest)
	reads.GET("render-page", h.page)
}

// key godoc
// @Summary Read the event common-page grant verification key
// @Tags auth
// @Produce json
// @Success 200 {object} response.Response{data=eventrender.Key}
// @Router /auth/event-render-key [get]
func (h *EventRenderHandler) key(ctx *gin.Context) {
	if h.signer == nil {
		ctx.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	response.AbortWithData(ctx, h.signer.PublicKey())
}

type eventRenderGrantRequest struct {
	Challenge string `json:"Challenge"`
}
type eventRenderGrantResponse struct {
	Token     string `json:"Token"`
	ExpiresAt int64  `json:"ExpiresAt"`
}

// grant godoc
// @Summary Issue a 60-second host-bound common-page staff preview grant
// @Tags events-self
// @Accept json
// @Produce json
// @Param body body eventRenderGrantRequest true "event server challenge digest"
// @Success 200 {object} response.Response{data=eventRenderGrantResponse}
// @Router /events/self/render-grant [post]
func (h *EventRenderHandler) grant(ctx *gin.Context) {
	if h.signer == nil {
		ctx.AbortWithStatus(http.StatusServiceUnavailable)
		return
	}
	tenant, ok := middleware.EventTenantFromContext(ctx.Request.Context())
	if !ok || tenant.Public {
		response.AbortWithNotFound(ctx)
		return
	}
	identity, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	audience := strings.TrimSuffix(h.hosts.EventURL(tenant.Tag), "/")
	if ctx.GetHeader("Origin") != audience {
		response.AbortWithNotFound(ctx)
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 256)
	var req eventRenderGrantRequest
	if err := ctx.ShouldBindJSON(&req); err != nil || !eventrender.ValidChallenge(req.Challenge) {
		response.AbortWithBadRequest(ctx)
		return
	}
	token, expires, err := h.signer.Issue(audience, tenant.EventID, identity.UserID, identity.SessionID, true, req.Challenge, time.Now().UTC())
	if err != nil {
		response.AbortWithBadRequest(ctx)
		return
	}
	response.AbortWithData(ctx, eventRenderGrantResponse{Token: token, ExpiresAt: expires})
}

var renderSlug = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

// renderAccess resolves the tenant with exact canonical Origin matching. A
// signed preview flag grants only published common-page reads for that event.
func (h *EventRenderHandler) renderAccess(ctx *gin.Context) (uuid.UUID, bool, bool) {
	origin := ctx.GetHeader("Origin")
	parsed, err := url.Parse(origin)
	if err != nil || parsed.Host == "" {
		response.AbortWithNotFound(ctx)
		return uuid.Nil, false, false
	}
	tag, ok := h.hosts.EventTag(parsed.Hostname())
	if !ok || origin != strings.TrimSuffix(h.hosts.EventURL(tag), "/") {
		response.AbortWithNotFound(ctx)
		return uuid.Nil, false, false
	}
	tenant, err := h.uc.ResolveEventByTag(ctx.Request.Context(), tag, time.Now().UTC())
	if err != nil {
		h.readError(ctx, err)
		return uuid.Nil, false, false
	}
	preview := false
	if authorization := ctx.GetHeader("Authorization"); authorization != "" {
		if h.signer == nil || !strings.HasPrefix(authorization, "EventRender ") {
			response.AbortWithNotFound(ctx)
			return uuid.Nil, false, false
		}
		claims, err := h.signer.Verify(strings.TrimPrefix(authorization, "EventRender "), origin, tenant.EventID, time.Now().UTC())
		if err != nil {
			response.AbortWithNotFound(ctx)
			return uuid.Nil, false, false
		}
		preview = claims.Preview
	}
	if !tenant.Public && !preview {
		response.AbortWithNotFound(ctx)
		return uuid.Nil, false, false
	}
	return tenant.EventID, preview, true
}
func (h *EventRenderHandler) readError(ctx *gin.Context, err error) {
	switch {
	case errors.Is(err, eventUC.ErrRenderRevisionChanged):
		response.AbortWithConflict(ctx)
	case errors.Is(err, eventModel.ErrEventNotFound.Err()), errors.Is(err, eventManagerModel.ErrEventManagementForbidden.Err()):
		response.AbortWithNotFound(ctx)
	default:
		response.AbortWithError(ctx, err)
	}
}
func renderSlugFromQuery(ctx *gin.Context) (string, bool) {
	slug := ctx.Query("page")
	if len(slug) > 80 || (slug != "" && !renderSlug.MatchString(slug)) {
		response.AbortWithNotFound(ctx)
		return "", false
	}
	return slug, true
}

type eventRenderManifestResponse struct {
	Event      publicEventInfoResponse `json:"Event"`
	Revision   string                  `json:"Revision"`
	Audience   string                  `json:"Audience"`
	Page       eventUC.RenderPageInfo  `json:"Page"`
	CacheUntil int64                   `json:"CacheUntil"`
}
type eventRenderPageResponse struct {
	Images    map[string]eventUC.RenderImageDimensions `json:"Images"`
	Revision  string                                   `json:"Revision"`
	Event     publicEventInfoResponse                  `json:"Event"`
	Page      eventRenderDocumentResponse              `json:"Page"`
	Variables map[string]any                           `json:"Variables"`
}
type eventRenderDocumentResponse struct {
	Slug     string                     `json:"Slug"`
	Title    string                     `json:"Title"`
	Document eventContentModel.Document `json:"Document"`
}

// manifest godoc
// @Summary Check common-page visibility and lightweight publication revision
// @Tags events-self
// @Produce json
// @Param page query string false "published public page slug, empty for landing"
// @Success 200 {object} response.Response{data=eventRenderManifestResponse}
// @Router /events/self/render-manifest [get]
func (h *EventRenderHandler) manifest(ctx *gin.Context) {
	slug, ok := renderSlugFromQuery(ctx)
	if !ok {
		return
	}
	id, preview, ok := h.renderAccess(ctx)
	if !ok {
		return
	}
	view, err := h.uc.GetEventRenderManifest(ctx.Request.Context(), id, slug, preview, time.Now().UTC())
	if err != nil {
		h.readError(ctx, err)
		return
	}
	response.AbortWithData(ctx, eventRenderManifestResponse{Event: toPublicEventInfoResponse(view.Event), Revision: view.Revision, Audience: view.Audience, Page: view.Page, CacheUntil: view.CacheUntil})
}

// page godoc
// @Summary Read the published PUBLIC-only document and values for an exact revision
// @Tags events-self
// @Produce json
// @Param page query string false "published public page slug, empty for landing"
// @Param revision query string true "manifest revision"
// @Success 200 {object} response.Response{data=eventRenderPageResponse}
// @Failure 409 {object} response.Response
// @Router /events/self/render-page [get]
func (h *EventRenderHandler) page(ctx *gin.Context) {
	slug, ok := renderSlugFromQuery(ctx)
	if !ok {
		return
	}
	revision := ctx.Query("revision")
	if len(revision) == 0 || len(revision) > 128 {
		response.AbortWithBadRequest(ctx)
		return
	}
	id, preview, ok := h.renderAccess(ctx)
	if !ok {
		return
	}
	view, err := h.uc.GetEventRenderPage(ctx.Request.Context(), id, slug, revision, preview, time.Now().UTC())
	if err != nil {
		h.readError(ctx, err)
		return
	}
	response.AbortWithData(ctx, eventRenderPageResponse{Revision: view.Revision, Event: toPublicEventInfoResponse(view.Event), Page: eventRenderDocumentResponse{Slug: view.Page.Slug, Title: view.Page.Title, Document: view.Document}, Variables: view.Variables, Images: view.Images})
}
