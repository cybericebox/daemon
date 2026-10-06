package event

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type eventContentResponse struct {
	Landing      eventContentModel.Document   `json:"Landing"`
	LandingDraft *eventContentModel.Document  `json:"LandingDraft"`
	Live         eventContentModel.LiveLayout `json:"Live"`
	Variables    map[string]any               `json:"Variables"`
}

type updateLandingContentRequest struct {
	Document eventContentModel.Document `json:"Document"`
}

type updateLiveLayoutRequest struct {
	Layout eventContentModel.LiveLayout `json:"Layout"`
}

// Swagger's parser does not traverse this event-content model package. These
// two types describe its JSON envelope without weakening the handler DTOs.
type eventContentSwaggerResponse struct {
	Landing      any            `json:"Landing"`
	LandingDraft any            `json:"LandingDraft"`
	Live         any            `json:"Live"`
	Variables    map[string]any `json:"Variables"`
}

type updateLandingContentSwaggerRequest struct {
	Document any `json:"Document"`
}

type updateLiveLayoutSwaggerRequest struct {
	Layout any `json:"Layout"`
}

// eventPageRequest is a page draft. NavigationAfter is the navbar placement
// applied on publish: "first" | "challenges" | "results" | page ID | "".
type eventPageRequest struct {
	Slug            string                           `json:"Slug"`
	Title           string                           `json:"Title"`
	Document        eventContentModel.Document       `json:"Document"`
	Visibility      eventContentModel.PageVisibility `json:"Visibility"`
	Navigation      eventContentModel.PageNavigation `json:"Navigation"`
	NavigationAfter string                           `json:"NavigationAfter"`
}

func (r eventPageRequest) toInput() eventUseCase.EventPageInput {
	return eventUseCase.EventPageInput{
		Slug:            r.Slug,
		Title:           r.Title,
		Document:        r.Document,
		Visibility:      r.Visibility,
		Navigation:      r.Navigation,
		NavigationAfter: r.NavigationAfter,
	}
}

// The Swagger parser cannot traverse eventContent.Document. These envelopes
// keep the published JSON shape accurate while handlers retain typed DTOs.
type eventPageSwaggerRequest struct {
	Slug            string `json:"Slug"`
	Title           string `json:"Title"`
	Document        any    `json:"Document"`
	Visibility      int32  `json:"Visibility"`
	Navigation      int32  `json:"Navigation"`
	NavigationAfter string `json:"NavigationAfter"`
}

type eventPageSwaggerResponse struct {
	ID              uuid.UUID  `json:"ID"`
	Slug            string     `json:"Slug"`
	Title           string     `json:"Title"`
	Document        any        `json:"Document"`
	Visibility      int32      `json:"Visibility"`
	Navigation      int32      `json:"Navigation"`
	NavigationOrder int32      `json:"NavigationOrder"`
	Draft           any        `json:"Draft"`
	PublishedAt     *time.Time `json:"PublishedAt"`
	CreatedAt       time.Time  `json:"CreatedAt"`
	UpdatedAt       time.Time  `json:"UpdatedAt"`
}

// getContent godoc
// @Summary Read event landing and live content settings
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=eventContentSwaggerResponse}
// @Router /events/{id}/manage/content [get]
func (h *Handler) getContent(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	content, err := h.useCase.GetEventContent(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventContentResponse(content))
}

// getContentVariables returns the variable names, formats and minimum page
// audiences supported by this event's saved visibility policy.
func (h *Handler) getContentVariables(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	config, err := h.useCase.GetEventConfig(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, eventUseCase.ContentVariableCatalog(config.ScoreboardVisibility))
}

// saveLandingDraft godoc
// @Summary Save the event landing block document as a draft
// @Description The draft is not public until it is published.
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body updateLandingContentSwaggerRequest true "landing document"
// @Success 204
// @Router /events/{id}/manage/content/landing [put]
func (h *Handler) saveLandingDraft(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var request updateLandingContentRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.SaveLandingDraft(ctx, eventID, request.Document); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// publishLanding godoc
// @Summary Publish the event landing draft
// @Tags events
// @Param id path string true "event ID"
// @Success 204
// @Router /events/{id}/manage/content/landing/publish [post]
func (h *Handler) publishLanding(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.PublishLanding(ctx, eventID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// discardLandingDraft godoc
// @Summary Discard the unpublished event landing draft
// @Tags events
// @Param id path string true "event ID"
// @Success 204
// @Router /events/{id}/manage/content/landing/draft [delete]
func (h *Handler) discardLandingDraft(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DiscardLandingDraft(ctx, eventID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// saveLiveLayoutDraft godoc
// @Summary Save an event live-screen layout draft
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body updateLiveLayoutSwaggerRequest true "live layout"
// @Success 204
// @Router /events/{id}/manage/content/live [put]
func (h *Handler) saveLiveLayoutDraft(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var request updateLiveLayoutRequest
	if err := ctx.ShouldBindJSON(&request); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.SaveLiveLayoutDraft(ctx, eventID, request.Layout); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Status(204)
}

// getLiveLayoutEditor godoc
// @Summary Read published and draft live-screen layouts
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/content/live [get]
func (h *Handler) getLiveLayoutEditor(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetLiveLayoutEditor(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, view)
}

type liveLayoutVersionResponse struct {
	Version int64 `json:"Version"`
}

// getLiveLayoutVersion godoc
// @Summary Read the published live-screen layout version
// @Description A light check for open live screens: reload the layout only when the version changes.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=liveLayoutVersionResponse}
// @Router /events/{id}/manage/content/live/version [get]
func (h *Handler) getLiveLayoutVersion(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetLiveLayoutEditor(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, liveLayoutVersionResponse{Version: view.Published.Version})
}

// publishLiveLayout godoc
// @Summary Publish an event live-screen layout draft
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/content/live/publish [post]
func (h *Handler) publishLiveLayout(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	layout, err := h.useCase.PublishLiveLayout(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, layout)
}

func toEventContentResponse(value eventUseCase.EventContentView) eventContentResponse {
	return eventContentResponse{Landing: value.Landing, LandingDraft: value.LandingDraft, Live: value.Live, Variables: value.Variables}
}

// listEventPages godoc
// @Summary List configurable static event pages
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]eventPageSwaggerResponse}
// @Router /events/{id}/manage/pages [get]
func (h *Handler) listEventPages(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	pages, err := h.useCase.ListEventPages(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, pages)
}

// createEventPage godoc
// @Summary Create an unpublished static event page (draft)
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body eventPageSwaggerRequest true "page configuration"
// @Success 200 {object} response.Response{data=eventPageSwaggerResponse}
// @Router /events/{id}/manage/pages [post]
func (h *Handler) createEventPage(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req eventPageRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	page, err := h.useCase.CreateEventPage(ctx, eventID, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, page)
}

// getEventPage godoc
// @Summary Read a configurable static event page by slug
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param slug path string true "page slug"
// @Success 200 {object} response.Response{data=eventPageSwaggerResponse}
// @Router /events/{id}/manage/pages/{slug} [get]
func (h *Handler) getEventPage(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	page, err := h.useCase.GetEventPage(ctx, eventID, ctx.Param("slug"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, page)
}

func pageIDParam(ctx *gin.Context) (uuid.UUID, bool) {
	pageID, err := uuid.FromString(ctx.Param("pageID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return pageID, true
}

// saveEventPageDraft godoc
// @Summary Save a static event page draft
// @Description The draft (content, settings and navbar placement) is not public until it is published.
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param pageID path string true "page ID"
// @Param body body eventPageSwaggerRequest true "page draft"
// @Success 200 {object} response.Response{data=eventPageSwaggerResponse}
// @Router /events/{id}/manage/pages/{pageID} [put]
func (h *Handler) saveEventPageDraft(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	pageID, ok := pageIDParam(ctx)
	if !ok {
		return
	}
	var req eventPageRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	page, err := h.useCase.SaveEventPageDraft(ctx, eventID, pageID, req.toInput())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, page)
}

// publishEventPage godoc
// @Summary Publish a static event page draft
// @Description Content, settings and the navbar order become public in one statement.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param pageID path string true "page ID"
// @Success 200 {object} response.Response{data=eventPageSwaggerResponse}
// @Router /events/{id}/manage/pages/{pageID}/publish [post]
func (h *Handler) publishEventPage(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	pageID, ok := pageIDParam(ctx)
	if !ok {
		return
	}
	page, err := h.useCase.PublishEventPage(ctx, eventID, pageID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, page)
}

// discardEventPageDraft godoc
// @Summary Discard unpublished changes of a published static event page
// @Tags events
// @Param id path string true "event ID"
// @Param pageID path string true "page ID"
// @Success 204
// @Router /events/{id}/manage/pages/{pageID}/draft [delete]
func (h *Handler) discardEventPageDraft(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	pageID, ok := pageIDParam(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DiscardEventPageDraft(ctx, eventID, pageID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// deleteEventPage godoc
// @Summary Delete a configurable static event page
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param pageID path string true "page ID"
// @Success 204
// @Router /events/{id}/manage/pages/{pageID} [delete]
func (h *Handler) deleteEventPage(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	pageID, err := uuid.FromString(ctx.Param("pageID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.DeleteEventPage(ctx, eventID, pageID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Status(204)
}
