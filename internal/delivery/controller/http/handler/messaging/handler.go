// Package messaging is the HTTP delivery layer of custom broadcasts and site
// banners: the platform routes (admin), the Event routes (/manage) and the
// public banner read every app polls.
package messaging

import (
	"context"
	"fmt"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/internal/model/rbac"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
	broadcastUseCase "github.com/cybericebox/daemon/internal/useCase/notification/broadcast"
	"github.com/cybericebox/daemon/pkg/pagination"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		RequireManageEvent(ctx context.Context, eventID, userID uuid.UUID) error
		RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error

		CountBroadcastAudience(ctx context.Context, scopeEventID *uuid.UUID, a broadcastModel.Audience) (int, error)
		SendBroadcast(ctx context.Context, in broadcastUseCase.SendInput) (broadcastModel.Broadcast, error)
		ListBroadcasts(ctx context.Context, f broadcastModel.ListFilter) ([]broadcastModel.Broadcast, error)
		GetBroadcast(ctx context.Context, id uuid.UUID, scopeEventID *uuid.UUID) (broadcastModel.Broadcast, error)
		ListBroadcastDeliveries(ctx context.Context, id uuid.UUID, limit, offset int32) ([]broadcastModel.Delivery, error)

		CreateSiteBanner(ctx context.Context, scope *uuid.UUID, actorID uuid.UUID, in siteBannerModel.Input) (siteBannerModel.Banner, error)
		UpdateSiteBanner(ctx context.Context, scope *uuid.UUID, id uuid.UUID, in siteBannerModel.Input) (siteBannerModel.Banner, error)
		GetSiteBanner(ctx context.Context, scope *uuid.UUID, id uuid.UUID) (siteBannerModel.Banner, error)
		DeleteSiteBanner(ctx context.Context, scope *uuid.UUID, id uuid.UUID) error
		ListSiteBanners(ctx context.Context, scope *uuid.UUID) ([]siteBannerModel.Banner, error)
		ListVisibleSiteBanners(ctx context.Context, eventID, userID *uuid.UUID) ([]siteBannerModel.Banner, error)
	}

	// scopeFn resolves the scope of a request: nil is the platform.
	scopeFn func(ctx *gin.Context) (*uuid.UUID, bool)

	audienceDTO struct {
		Kind    string      `json:"Kind"`
		Roles   []string    `json:"Roles"`
		UserIDs []uuid.UUID `json:"UserIDs"`
		TeamIDs []uuid.UUID `json:"TeamIDs"`
	}

	// contentDTO is the authored message of a broadcast. EmailBody is the block
	// array of an email template body.
	contentDTO struct {
		Channels     []string `json:"Channels"`
		Subject      string   `json:"Subject"`
		Preheader    string   `json:"Preheader"`
		EmailBody    any      `json:"EmailBody"`
		EmailStyling any      `json:"EmailStyling"`
		InAppTitle   string   `json:"InAppTitle"`
		InAppBody    string   `json:"InAppBody"`
		InAppLink    string   `json:"InAppLink"`
	}

	sendRequest struct {
		contentDTO
		Audience audienceDTO `json:"Audience"`
	}

	countRequest struct {
		Audience audienceDTO `json:"Audience"`
	}

	countResponse struct {
		Count int `json:"Count"`
	}

	broadcastResponse struct {
		ID             uuid.UUID   `json:"ID"`
		ScopeEventID   *uuid.UUID  `json:"ScopeEventID"`
		EventName      string      `json:"EventName"`
		CreatedBy      *uuid.UUID  `json:"CreatedBy"`
		CreatedByName  string      `json:"CreatedByName"`
		Channels       []string    `json:"Channels"`
		Subject        string      `json:"Subject"`
		Preheader      string      `json:"Preheader"`
		EmailBody      any         `json:"EmailBody"`
		EmailStyling   any         `json:"EmailStyling"`
		InAppTitle     string      `json:"InAppTitle"`
		InAppBody      string      `json:"InAppBody"`
		InAppLink      string      `json:"InAppLink"`
		Audience       audienceDTO `json:"Audience"`
		RecipientCount int32       `json:"RecipientCount"`
		SentCount      int64       `json:"SentCount"`
		FailedCount    int64       `json:"FailedCount"`
		Status         string      `json:"Status"`
		CreatedAt      time.Time   `json:"CreatedAt"`
		FinishedAt     *time.Time  `json:"FinishedAt"`
	}

	deliveryResponse struct {
		DispatchID      uuid.UUID `json:"DispatchID"`
		RecipientUserID uuid.UUID `json:"RecipientUserID"`
		RecipientEmail  string    `json:"RecipientEmail"`
		DispatchStatus  string    `json:"DispatchStatus"`
		Channel         string    `json:"Channel"`
		TargetStatus    string    `json:"TargetStatus"`
		Error           string    `json:"Error"`
	}

	bannerRequest struct {
		Text        string     `json:"Text"`
		LinkURL     string     `json:"LinkURL"`
		LinkLabel   string     `json:"LinkLabel"`
		Level       string     `json:"Level"`
		ActiveFrom  *time.Time `json:"ActiveFrom"`
		ActiveTo    *time.Time `json:"ActiveTo"`
		Dismissible bool       `json:"Dismissible"`
		Audience    string     `json:"Audience"`
		IsActive    bool       `json:"IsActive"`
	}

	bannerResponse struct {
		ID           uuid.UUID  `json:"ID"`
		ScopeEventID *uuid.UUID `json:"ScopeEventID"`
		Text         string     `json:"Text"`
		LinkURL      string     `json:"LinkURL"`
		LinkLabel    string     `json:"LinkLabel"`
		Level        string     `json:"Level"`
		ActiveFrom   *time.Time `json:"ActiveFrom"`
		ActiveTo     *time.Time `json:"ActiveTo"`
		Dismissible  bool       `json:"Dismissible"`
		Audience     string     `json:"Audience"`
		IsActive     bool       `json:"IsActive"`
		CreatedAt    time.Time  `json:"CreatedAt"`
		UpdatedAt    time.Time  `json:"UpdatedAt"`
	}
)

func NewMessagingAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

// Init mounts the platform routes under notifications/, the Event routes under
// events/:id/manage/ and the public banner read under banners.
func (h *Handler) Init(api *gin.RouterGroup) {
	platform := func(ctx *gin.Context) (*uuid.UUID, bool) { return nil, true }

	bc := api.Group("notifications/broadcasts", h.prot.RequirePermission(rbac.PermNotificationsBroadcast))
	bc.POST("audience-count", h.countAudience(platform))
	bc.POST("", h.send(platform))
	bc.GET("", h.listBroadcasts(platform))
	bc.GET(":broadcastID", h.getBroadcast(platform))
	bc.GET(":broadcastID/deliveries", h.listDeliveries(platform))

	bn := api.Group("notifications/site-banners")
	bn.GET("", h.prot.RequirePermission(rbac.PermNotificationsBannersRead), h.listBanners(platform))
	bn.POST("", h.prot.RequirePermission(rbac.PermNotificationsBannersWrite), h.createBanner(platform))
	bn.PUT(":bannerID", h.prot.RequirePermission(rbac.PermNotificationsBannersWrite), h.updateBanner(platform))
	bn.DELETE(":bannerID", h.prot.RequirePermission(rbac.PermNotificationsBannersWrite), h.deleteBanner(platform))

	// Event scope: owners and write moderators send and edit; viewers read.
	manage := api.Group("events/:id/manage", h.prot.RequirePermission(rbac.PermSelf))
	manage.POST("broadcasts/audience-count", h.requireManage, h.countAudience(eventScope))
	manage.POST("broadcasts", h.requireManage, h.send(eventScope))
	manage.GET("broadcasts", h.requireRead, h.listBroadcasts(eventScope))
	manage.GET("broadcasts/:broadcastID", h.requireRead, h.getBroadcast(eventScope))
	manage.GET("broadcasts/:broadcastID/deliveries", h.requireRead, h.listDeliveries(eventScope))
	manage.GET("banners", h.requireRead, h.listBanners(eventScope))
	manage.POST("banners", h.requireManage, h.createBanner(eventScope))
	manage.PUT("banners/:bannerID", h.requireManage, h.updateBanner(eventScope))
	manage.DELETE("banners/:bannerID", h.requireManage, h.deleteBanner(eventScope))

	api.GET("banners", h.prot.RequirePermission(rbac.PermBannersView), h.listVisibleBanners)
}

func eventScope(ctx *gin.Context) (*uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return nil, false
	}
	return &id, true
}

func (h *Handler) requireManage(ctx *gin.Context) { h.requireEvent(ctx, true) }
func (h *Handler) requireRead(ctx *gin.Context)   { h.requireEvent(ctx, false) }

func (h *Handler) requireEvent(ctx *gin.Context, manage bool) {
	id, ok := eventScope(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var err error
	if manage {
		err = h.useCase.RequireManageEvent(ctx, *id, claims.UserID)
	} else {
		err = h.useCase.RequireReadEvent(ctx, *id, claims.UserID)
	}
	if err != nil {
		response.AbortWithError(ctx, err)
	}
}

func (a audienceDTO) toModel() broadcastModel.Audience {
	return broadcastModel.Audience{Kind: broadcastModel.AudienceKind(a.Kind), Roles: a.Roles, UserIDs: a.UserIDs, TeamIDs: a.TeamIDs}
}

func audienceToDTO(a broadcastModel.Audience) audienceDTO {
	out := audienceDTO{Kind: string(a.Kind), Roles: a.Roles, UserIDs: a.UserIDs, TeamIDs: a.TeamIDs}
	if out.Roles == nil {
		out.Roles = []string{}
	}
	if out.UserIDs == nil {
		out.UserIDs = []uuid.UUID{}
	}
	if out.TeamIDs == nil {
		out.TeamIDs = []uuid.UUID{}
	}
	return out
}

func (c contentDTO) toModel() (broadcastModel.Content, error) {
	body, err := marshalJSON(c.EmailBody, "[]")
	if err != nil {
		return broadcastModel.Content{}, err
	}
	styling, err := marshalJSON(c.EmailStyling, "{}")
	if err != nil {
		return broadcastModel.Content{}, err
	}
	channels := make([]notificationTypes.NotificationChannel, 0, len(c.Channels))
	for _, ch := range c.Channels {
		channels = append(channels, notificationTypes.NotificationChannel(ch))
	}
	return broadcastModel.Content{
		Channels: channels, Subject: c.Subject, Preheader: c.Preheader, EmailBody: body, EmailStyling: styling,
		InAppTitle: c.InAppTitle, InAppBody: c.InAppBody, InAppLink: c.InAppLink,
	}, nil
}

func toBroadcastResponse(b broadcastModel.Broadcast) broadcastResponse {
	channels := make([]string, 0, len(b.Content.Channels))
	for _, ch := range b.Content.Channels {
		channels = append(channels, string(ch))
	}
	return broadcastResponse{
		ID: b.ID, ScopeEventID: b.ScopeEventID, EventName: b.EventName, CreatedBy: b.CreatedBy, CreatedByName: b.CreatedByName,
		Channels: channels, Subject: b.Content.Subject, Preheader: b.Content.Preheader,
		EmailBody: rawOrNil(b.Content.EmailBody), EmailStyling: rawOrNil(b.Content.EmailStyling),
		InAppTitle: b.Content.InAppTitle, InAppBody: b.Content.InAppBody, InAppLink: b.Content.InAppLink,
		Audience: audienceToDTO(b.Audience), RecipientCount: b.RecipientCount, SentCount: b.SentCount, FailedCount: b.FailedCount,
		Status: string(b.Status), CreatedAt: b.CreatedAt, FinishedAt: b.FinishedAt,
	}
}

func toBannerResponse(b siteBannerModel.Banner) bannerResponse {
	return bannerResponse{
		ID: b.ID, ScopeEventID: b.ScopeEventID, Text: b.Text, LinkURL: b.LinkURL, LinkLabel: b.LinkLabel,
		Level: string(b.Level), ActiveFrom: b.ActiveFrom, ActiveTo: b.ActiveTo, Dismissible: b.Dismissible,
		Audience: string(b.Audience), IsActive: b.IsActive, CreatedAt: b.CreatedAt, UpdatedAt: b.UpdatedAt,
	}
}

func parseLimit(raw string, def int) (int, error) {
	if raw == "" {
		return def, nil
	}
	var n int
	if _, err := fmt.Sscanf(raw, "%d", &n); err != nil || n < 1 || n > pagination.MaxPageSize {
		return 0, fmt.Errorf("limit must be between 1 and %d", pagination.MaxPageSize)
	}
	return n, nil
}
