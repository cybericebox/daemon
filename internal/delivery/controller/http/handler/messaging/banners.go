package messaging

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

func (r bannerRequest) toInput() siteBannerModel.Input {
	return siteBannerModel.Input{
		Text: r.Text, LinkURL: r.LinkURL, LinkLabel: r.LinkLabel, Level: siteBannerModel.Level(r.Level),
		ActiveFrom: r.ActiveFrom, ActiveTo: r.ActiveTo, Dismissible: r.Dismissible,
		Audience: siteBannerModel.Audience(r.Audience), IsActive: r.IsActive,
	}
}

// listBanners godoc
// @Summary  List the site banners of a scope (management)
// @Tags     site-banners
// @Produce  json
// @Success  200  {object}  response.Response{data=[]bannerResponse}
// @Router   /notifications/site-banners [get]
// @Router   /events/{id}/manage/banners [get]
func (h *Handler) listBanners(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
			return
		}
		items, err := h.useCase.ListSiteBanners(ctx.Request.Context(), eventID)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		out := make([]bannerResponse, 0, len(items))
		for _, b := range items {
			out = append(out, toBannerResponse(b))
		}
		response.AbortWithData(ctx, out)
	}
}

// createBanner godoc
// @Summary  Create a site banner
// @Tags     site-banners
// @Accept   json
// @Produce  json
// @Param    body  body  bannerRequest  true  "banner"
// @Success  200  {object}  response.Response{data=bannerResponse}
// @Router   /notifications/site-banners [post]
// @Router   /events/{id}/manage/banners [post]
func (h *Handler) createBanner(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
			return
		}
		claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
		if !ok {
			response.AbortWithUnauthenticated(ctx)
			return
		}
		var req bannerRequest
		if err := ctx.ShouldBindJSON(&req); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		b, err := h.useCase.CreateSiteBanner(ctx.Request.Context(), eventID, claims.UserID, req.toInput())
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, toBannerResponse(b))
	}
}

// updateBanner godoc
// @Summary  Update a site banner (also deactivates it)
// @Tags     site-banners
// @Accept   json
// @Produce  json
// @Param    bannerID  path  string         true  "banner ID"
// @Param    body      body  bannerRequest  true  "banner"
// @Success  200  {object}  response.Response{data=bannerResponse}
// @Router   /notifications/site-banners/{bannerID} [put]
// @Router   /events/{id}/manage/banners/{bannerID} [put]
func (h *Handler) updateBanner(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
			return
		}
		id, err := uuid.FromString(ctx.Param("bannerID"))
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		var req bannerRequest
		if err = ctx.ShouldBindJSON(&req); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		b, err := h.useCase.UpdateSiteBanner(ctx.Request.Context(), eventID, id, req.toInput())
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, toBannerResponse(b))
	}
}

// deleteBanner godoc
// @Summary  Delete a site banner
// @Tags     site-banners
// @Param    bannerID  path  string  true  "banner ID"
// @Success  200  {object}  response.Response
// @Router   /notifications/site-banners/{bannerID} [delete]
// @Router   /events/{id}/manage/banners/{bannerID} [delete]
func (h *Handler) deleteBanner(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
			return
		}
		id, err := uuid.FromString(ctx.Param("bannerID"))
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		if err = h.useCase.DeleteSiteBanner(ctx.Request.Context(), eventID, id); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, struct{}{})
	}
}

// visibleBannerResponse is what apps render: no scope internals.
type visibleBannerResponse struct {
	ID          uuid.UUID `json:"ID"`
	Text        string    `json:"Text"`
	LinkURL     string    `json:"LinkURL"`
	LinkLabel   string    `json:"LinkLabel"`
	Level       string    `json:"Level"`
	Dismissible bool      `json:"Dismissible"`
	// Version changes on every edit, so a dismissal of the old text does not
	// hide the edited banner.
	Version int64 `json:"Version"`
}

// listVisibleBanners godoc
// @Summary  Banners the current viewer sees (public, optional session)
// @Tags     site-banners
// @Produce  json
// @Param    event  query  string  false  "Event id of an Event site"
// @Success  200  {object}  response.Response{data=[]visibleBannerResponse}
// @Router   /banners [get]
func (h *Handler) listVisibleBanners(ctx *gin.Context) {
	var eventID *uuid.UUID
	if raw := ctx.Query("event"); raw != "" {
		id, err := uuid.FromString(raw)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		eventID = &id
	}
	var userID *uuid.UUID
	if claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context()); ok {
		userID = &claims.UserID
	}
	items, err := h.useCase.ListVisibleSiteBanners(ctx.Request.Context(), eventID, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]visibleBannerResponse, 0, len(items))
	for _, b := range items {
		out = append(out, visibleBannerResponse{
			ID: b.ID, Text: b.Text, LinkURL: b.LinkURL, LinkLabel: b.LinkLabel, Level: string(b.Level),
			Dismissible: b.Dismissible, Version: b.UpdatedAt.UnixMilli(),
		})
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, out)
}
