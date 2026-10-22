package event

import (
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// Live screens of an event («Екран 1», «Екран 2», …, at most 10): each has
// its own name, layout with draft, and screen link.

type liveScreenSummaryResponse struct {
	ID       uuid.UUID `json:"ID"`
	Name     string    `json:"Name"`
	Position int32     `json:"Position"`
	// Version is the published layout version the open screens poll for.
	Version   int64     `json:"Version"`
	HasDraft  bool      `json:"HasDraft"`
	UpdatedAt time.Time `json:"UpdatedAt"`
	// Link is the working screen link (never its token); null when the
	// screen has none or the caller may not manage the event.
	Link *liveScreenLinkResponse `json:"Link"`
}

type liveScreensResponse struct {
	Screens []liveScreenSummaryResponse `json:"Screens"`
	// Max is the screen limit of an event.
	Max int `json:"Max"`
	// LinksVisible: the caller manages the event, so Link is filled in.
	LinksVisible bool `json:"LinksVisible"`
}

type liveScreenEditorResponse struct {
	liveScreenSummaryResponse
	Published eventContentModel.LiveLayout  `json:"Published"`
	Draft     *eventContentModel.LiveLayout `json:"Draft"`
}

// liveScreenEditorSwaggerResponse describes liveScreenEditorResponse (the
// swagger parser does not traverse the layout model).
type liveScreenEditorSwaggerResponse struct {
	ID        uuid.UUID               `json:"ID"`
	Name      string                  `json:"Name"`
	Position  int32                   `json:"Position"`
	Version   int64                   `json:"Version"`
	HasDraft  bool                    `json:"HasDraft"`
	UpdatedAt time.Time               `json:"UpdatedAt"`
	Link      *liveScreenLinkResponse `json:"Link"`
	Published any                     `json:"Published"`
	Draft     any                     `json:"Draft"`
}

// createLiveScreenRequest: Name empty = «Екран N». CopyFrom (a screen ID)
// copies that screen's published layout and draft; Layout (a template from
// the editor) becomes the published layout. At most one of the two.
type createLiveScreenRequest struct {
	Name     string                        `json:"Name"`
	CopyFrom *uuid.UUID                    `json:"CopyFrom"`
	Layout   *eventContentModel.LiveLayout `json:"Layout"`
}

type createLiveScreenSwaggerRequest struct {
	Name     string     `json:"Name"`
	CopyFrom *uuid.UUID `json:"CopyFrom"`
	Layout   any        `json:"Layout"`
}

type renameLiveScreenRequest struct {
	Name string `json:"Name"`
}

type duplicateLiveScreenRequest struct {
	// Name empty = «Екран N».
	Name string `json:"Name"`
}

type reorderLiveScreensRequest struct {
	// ScreenIDs lists every screen of the event once, in the new order.
	ScreenIDs []uuid.UUID `json:"ScreenIDs" binding:"required"`
}

func toLiveScreenSummaryResponse(v eventUseCase.LiveScreenSummaryView) liveScreenSummaryResponse {
	out := liveScreenSummaryResponse{ID: v.ID, Name: v.Name, Position: v.Position, Version: v.Version, HasDraft: v.HasDraft, UpdatedAt: v.UpdatedAt}
	if v.Link != nil {
		link := toLiveScreenLinkResponse(*v.Link, "")
		out.Link = &link
	}
	return out
}

func toLiveScreensResponse(values []eventUseCase.LiveScreenSummaryView, linksVisible bool) liveScreensResponse {
	out := liveScreensResponse{Screens: make([]liveScreenSummaryResponse, 0, len(values)), Max: eventContentModel.LiveDisplaysMax, LinksVisible: linksVisible}
	for _, value := range values {
		out.Screens = append(out.Screens, toLiveScreenSummaryResponse(value))
	}
	return out
}

func parseScreenID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("screenID"))
	if err != nil || id == uuid.Nil {
		response.AbortWithBadRequest(ctx, errors.New("invalid screen ID"))
		return uuid.Nil, false
	}
	return id, true
}

// parseScreenQuery reads ?screen (a screen ID); without it uuid.Nil, the
// event's first screen.
func parseScreenQuery(ctx *gin.Context) (uuid.UUID, bool) {
	raw := ctx.Query("screen")
	if raw == "" {
		return uuid.Nil, true
	}
	id, err := uuid.FromString(raw)
	if err != nil || id == uuid.Nil {
		response.AbortWithBadRequest(ctx, errors.New("invalid screen ID"))
		return uuid.Nil, false
	}
	return id, true
}

// canManage tells a reader that may also manage the event (links are shown
// only to them, like the link routes).
func (h *Handler) canManage(ctx *gin.Context, eventID uuid.UUID) bool {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	return ok && h.useCase.RequireManageEvent(ctx, eventID, claims.UserID) == nil
}

// listLiveScreens godoc
// @Summary List the event's live screens (the editor's screen switcher)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=liveScreensResponse}
// @Router /events/{id}/manage/content/live/screens [get]
func (h *Handler) listLiveScreens(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	links := h.canManage(ctx, eventID)
	values, err := h.useCase.ListLiveScreens(ctx, eventID, links)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toLiveScreensResponse(values, links))
}

// createLiveScreen godoc
// @Summary Add a live screen (at most 10): empty, a copy of a screen or from a template layout
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body createLiveScreenSwaggerRequest true "screen"
// @Success 201 {object} response.Response{data=liveScreenSummaryResponse}
// @Router /events/{id}/manage/content/live/screens [post]
func (h *Handler) createLiveScreen(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var request createLiveScreenRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	value, err := h.useCase.CreateLiveScreen(ctx, eventID, eventUseCase.CreateLiveScreenInput{Name: request.Name, CopyFrom: request.CopyFrom, Layout: request.Layout})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithDataStatus(ctx, http.StatusCreated, toLiveScreenSummaryResponse(value))
}

// getLiveScreenEditor godoc
// @Summary Read a live screen with its published layout and draft
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Success 200 {object} response.Response{data=liveScreenEditorSwaggerResponse}
// @Router /events/{id}/manage/content/live/screens/{screenID} [get]
func (h *Handler) getLiveScreenEditor(ctx *gin.Context) {
	if screenID, ok := parseScreenID(ctx); ok {
		h.serveLiveScreenEditor(ctx, screenID)
	}
}

func (h *Handler) serveLiveScreenEditor(ctx *gin.Context, screenID uuid.UUID) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetLiveScreenEditor(ctx, eventID, screenID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, liveScreenEditorResponse{liveScreenSummaryResponse: toLiveScreenSummaryResponse(view.LiveScreenSummaryView), Published: view.Published, Draft: view.Draft})
}

// renameLiveScreen godoc
// @Summary Rename a live screen (1 to 40 characters)
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Param body body renameLiveScreenRequest true "name"
// @Success 200 {object} response.Response{data=liveScreenSummaryResponse}
// @Router /events/{id}/manage/content/live/screens/{screenID} [patch]
func (h *Handler) renameLiveScreen(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	screenID, ok := parseScreenID(ctx)
	if !ok {
		return
	}
	var request renameLiveScreenRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	value, err := h.useCase.RenameLiveScreen(ctx, eventID, screenID, request.Name)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toLiveScreenSummaryResponse(value))
}

// duplicateLiveScreen godoc
// @Summary Duplicate a live screen (its published layout and draft, not its link)
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Param body body duplicateLiveScreenRequest false "name of the copy"
// @Success 201 {object} response.Response{data=liveScreenSummaryResponse}
// @Router /events/{id}/manage/content/live/screens/{screenID}/duplicate [post]
func (h *Handler) duplicateLiveScreen(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	screenID, ok := parseScreenID(ctx)
	if !ok {
		return
	}
	var request duplicateLiveScreenRequest
	if ctx.Request.ContentLength != 0 {
		if err := ctx.ShouldBindJSON(&request); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
	}
	value, err := h.useCase.DuplicateLiveScreen(ctx, eventID, screenID, request.Name)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithDataStatus(ctx, http.StatusCreated, toLiveScreenSummaryResponse(value))
}

// reorderLiveScreens godoc
// @Summary Set the order of the event's live screens
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body reorderLiveScreensRequest true "every screen ID once, in the new order"
// @Success 200 {object} response.Response{data=liveScreensResponse}
// @Router /events/{id}/manage/content/live/screen-order [put]
func (h *Handler) reorderLiveScreens(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var request reorderLiveScreensRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	values, err := h.useCase.ReorderLiveScreens(ctx, eventID, request.ScreenIDs)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	// Reordering needs manage access, so the links are not shown here
	// (one less read); the switcher keeps those it has.
	response.AbortWithData(ctx, toLiveScreensResponse(values, false))
}

// deleteLiveScreen godoc
// @Summary Delete a live screen and its link (not the event's only screen)
// @Tags events
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Success 204
// @Router /events/{id}/manage/content/live/screens/{screenID} [delete]
func (h *Handler) deleteLiveScreen(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	screenID, ok := parseScreenID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DeleteLiveScreen(ctx, eventID, screenID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithNoContent(ctx)
}

// getLiveScreenVersion godoc
// @Summary Read a live screen's published layout version
// @Description A light check for an open screen: reload the layout only when the version changes.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Success 200 {object} response.Response{data=liveLayoutVersionResponse}
// @Router /events/{id}/manage/content/live/screens/{screenID}/version [get]
func (h *Handler) getLiveScreenVersion(ctx *gin.Context) {
	if screenID, ok := parseScreenID(ctx); ok {
		h.serveLiveScreenVersion(ctx, screenID)
	}
}

// saveLiveScreenDraft godoc
// @Summary Save a live screen's layout draft
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Param body body updateLiveLayoutSwaggerRequest true "live layout"
// @Success 204
// @Router /events/{id}/manage/content/live/screens/{screenID}/draft [put]
func (h *Handler) saveLiveScreenDraft(ctx *gin.Context) {
	if screenID, ok := parseScreenID(ctx); ok {
		h.saveLiveDraft(ctx, screenID)
	}
}

// publishLiveScreen godoc
// @Summary Publish a live screen's layout draft
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param screenID path string true "screen ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/content/live/screens/{screenID}/publish [post]
func (h *Handler) publishLiveScreen(ctx *gin.Context) {
	if screenID, ok := parseScreenID(ctx); ok {
		h.publishLive(ctx, screenID)
	}
}
