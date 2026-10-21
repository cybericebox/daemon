// Package pulse is the HTTP delivery of the cheap per-tab call every site makes (the pulse) and of the
// banner list it points at.
package pulse

import (
	"context"
	"errors"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	siteModel "github.com/cybericebox/daemon/internal/model/site"
	pulseUseCase "github.com/cybericebox/daemon/internal/useCase/notification/pulse"
)

// guestFreshFor is how long a guest's pulse may be reused without asking: the answer is the same for
// every guest of the site, so shared caches absorb most of the load.
const guestFreshFor = 15 * time.Second

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
		// site resolves the requesting site into the request context.
		site gin.HandlerFunc
	}

	IUseCase interface {
		Pulse(ctx context.Context, site siteModel.Site, viewer pulseUseCase.Viewer) (pulseUseCase.View, error)
		Banners(ctx context.Context, site siteModel.Site, viewer pulseUseCase.Viewer) (pulseUseCase.BannerList, error)
		DismissBanner(ctx context.Context, userID, id uuid.UUID) error
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}
)

type (
	inboxPulseResponse struct {
		// Cursor is the newest inbox item of the site's scope, empty when there is none. A change means
		// new items: fetch the inbox with since.
		Cursor string `json:"Cursor"`
		// Unread is the unread count of the site's scope.
		Unread int64 `json:"Unread"`
	}

	pulseResponse struct {
		// Banners is the version of the banner list; a change means: fetch the banners.
		Banners string `json:"Banners"`
		// Inbox is null for a guest.
		Inbox *inboxPulseResponse `json:"Inbox"`
	}

	bannerResponse struct {
		// ID is a UUID, or system-results-freeze:<event id> for the results-freeze banner.
		ID string `json:"ID"`
		// Kind is site (authored), system (raised by the platform, the freeze) or personal (a banner
		// notification of the signed-in user; only these can be dismissed on the server).
		Kind        string `json:"Kind" enums:"site,system,personal"`
		Title       string `json:"Title"`
		Text        string `json:"Text"`
		LinkURL     string `json:"LinkURL"`
		LinkLabel   string `json:"LinkLabel"`
		Level       string `json:"Level" enums:"info,warning,critical"`
		Dismissible bool   `json:"Dismissible"`
		// Version changes on every edit, so a dismissal of the old text does not hide the edited banner.
		Version int64 `json:"Version"`
	}
)

func NewPulseAPIHandler(useCase IUseCase, prot IProtection, site gin.HandlerFunc) *Handler {
	return &Handler{useCase: useCase, prot: prot, site: site}
}

// Init mounts the pulse and the banners under <notifications>/.
func (h *Handler) Init(router *gin.RouterGroup) {
	router.GET("pulse", h.prot.RequirePermission(rbac.PermBannersView), h.site, h.pulse)
	router.GET("banners", h.prot.RequirePermission(rbac.PermBannersView), h.site, h.banners)
	router.PATCH("banners/:id/dismiss", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.dismissBanner)
}

// viewer is who asks; a guest has no UserID.
func viewer(ctx *gin.Context) pulseUseCase.Viewer {
	if claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context()); ok {
		return pulseUseCase.Viewer{UserID: &claims.UserID, Role: claims.Role}
	}
	return pulseUseCase.Viewer{Role: rbac.RolePublic}
}

func requestSite(ctx *gin.Context) (siteModel.Site, bool) {
	site, ok := middleware.SiteFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithBadRequest(ctx, errors.New("unknown site"))
	}
	return site, ok
}

// pulse godoc
// @Summary  The cheap per-tab pulse: the banner version and the inbox state
// @Description  Call it about every 30 seconds per tab. The site is the site query parameter (with event for an event site), or the request Origin when it is missing; neither is a 400. Banners is the version of the banner list (site banners, the results-freeze banner of the event and the user's personal banners): when it differs from the one the client holds, fetch GET /notifications/banners. Inbox (null for a guest) has the cursor of the newest inbox item and the unread count: when the cursor or Unread changed, fetch GET /notifications/inbox?since=<cursor>. A guest's answer is the same for everyone of the site (Cache-Control public, max-age 15, Vary Origin and Cookie, ETag); a signed-in answer is private, no-cache, with an ETag and 304.
// @Tags     notification-pulse
// @Produce  json
// @Param    site  query  string  false  "Site; the request Origin when missing"  Enums(main,id,admin,exercises,event)
// @Param    event  query  string  false  "Event id of an event site"
// @Success  200  {object}  response.Response{data=pulseResponse}
// @Success  304
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Router   /notifications/pulse [get]
func (h *Handler) pulse(ctx *gin.Context) {
	site, ok := requestSite(ctx)
	if !ok {
		return
	}
	who := viewer(ctx)
	view, err := h.useCase.Pulse(ctx.Request.Context(), site, who)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := pulseResponse{Banners: view.Banners}
	if view.Inbox != nil {
		out.Inbox = &inboxPulseResponse{Cursor: view.Inbox.Cursor, Unread: view.Inbox.Unread}
	}
	if who.UserID == nil {
		response.AbortWithPublicETagData(ctx, out, guestFreshFor)
		return
	}
	response.AbortWithETagData(ctx, out, 0)
}

// banners godoc
// @Summary  The banners the viewer sees on the site
// @Description  The site banners of the platform and of the event site, filtered by window, targeting and audience; on an event site the results-freeze banner (Kind system, ID system-results-freeze:<event id>, text and level from the event's freeze settings) only while the freeze applies to this viewer, never an error; and, for a signed-in user, the user's own banner notifications (Kind personal). The ETag is the Banners version of the pulse, so the client can tell whether it holds the version the pulse reported. Sorted by severity, then platform before event, then newest.
// @Tags     notification-pulse
// @Produce  json
// @Param    site  query  string  false  "Site; the request Origin when missing"  Enums(main,id,admin,exercises,event)
// @Param    event  query  string  false  "Event id of an event site"
// @Success  200  {object}  response.Response{data=[]bannerResponse}
// @Success  304
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Router   /notifications/banners [get]
func (h *Handler) banners(ctx *gin.Context) {
	site, ok := requestSite(ctx)
	if !ok {
		return
	}
	who := viewer(ctx)
	list, err := h.useCase.Banners(ctx.Request.Context(), site, who)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]bannerResponse, 0, len(list.Items))
	for _, b := range list.Items {
		out = append(out, bannerResponse{
			ID: b.ID, Kind: b.Kind, Title: b.Title, Text: b.Text, LinkURL: b.LinkURL, LinkLabel: b.LinkLabel,
			Level: string(b.Level), Dismissible: b.Dismissible, Version: b.Version,
		})
	}
	response.AbortWithTaggedData(ctx, out, list.Version, who.UserID == nil)
}

// dismissBanner godoc
// @Summary  Dismiss a personal banner of the current user
// @Tags     notification-pulse
// @Param    id   path      string  true  "banner ID (Kind personal)"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Router   /notifications/banners/{id}/dismiss [patch]
func (h *Handler) dismissBanner(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.DismissBanner(ctx, claims.UserID, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
