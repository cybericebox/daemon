package event

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

const eventEmailImageMultipartSlack = 1 << 20

type eventEmailImageUploadResponse struct {
	FileID uuid.UUID `json:"FileID"`
	Url    string    `json:"Url"`
}

// Event templates are managed below the event route rather than the global
// template API. This makes the authorization boundary visible in the URL: an
// Event sees its own overrides plus the platform published versions it
// inherits (Source=platform, read-only), and can only mutate its own rows.
// "customize" turns an inherited platform version into an Event draft.

type eventEmailTemplateRequest struct {
	NotificationType string          `json:"NotificationType"`
	Subject          string          `json:"Subject"`
	Preheader        string          `json:"Preheader"`
	Body             json.RawMessage `json:"Body" swaggertype:"object"`
	Styling          json.RawMessage `json:"Styling" swaggertype:"object"`
}

type eventEmailTemplateResponse struct {
	ID               uuid.UUID       `json:"ID"`
	ScopeEventID     *uuid.UUID      `json:"ScopeEventID"`
	NotificationType string          `json:"NotificationType"`
	Status           string          `json:"Status"`
	Subject          string          `json:"Subject"`
	Preheader        string          `json:"Preheader"`
	Body             json.RawMessage `json:"Body" swaggertype:"object"`
	Styling          json.RawMessage `json:"Styling" swaggertype:"object"`
	PublishedAt      *time.Time      `json:"PublishedAt"`
	UpdatedByUserID  *uuid.UUID      `json:"UpdatedByUserID"`
	CreatedAt        time.Time       `json:"CreatedAt"`
	UpdatedAt        time.Time       `json:"UpdatedAt"`
	// Source is "platform" for an inherited platform version (read-only) and
	// "event" for a row owned by this Event.
	Source string `json:"Source" enums:"platform,event"`
}

type eventInAppTemplateRequest struct {
	NotificationType string          `json:"NotificationType"`
	Title            string          `json:"Title"`
	Body             string          `json:"Body"`
	Link             string          `json:"Link"`
	Icon             string          `json:"Icon"`
	Tone             string          `json:"Tone"`
	AccentColor      string          `json:"AccentColor"`
	Surface          string          `json:"Surface"`
	AutoDismissMs    *int32          `json:"AutoDismissMs"`
	Actions          json.RawMessage `json:"Actions" swaggertype:"object"`
	Dismissible      bool            `json:"Dismissible"`
}

type eventInAppTemplateResponse struct {
	ID               uuid.UUID       `json:"ID"`
	ScopeEventID     *uuid.UUID      `json:"ScopeEventID"`
	NotificationType string          `json:"NotificationType"`
	Status           string          `json:"Status"`
	Title            string          `json:"Title"`
	Body             string          `json:"Body"`
	Link             string          `json:"Link"`
	Icon             string          `json:"Icon"`
	Tone             string          `json:"Tone"`
	AccentColor      string          `json:"AccentColor"`
	Surface          string          `json:"Surface"`
	AutoDismissMs    *int32          `json:"AutoDismissMs"`
	Actions          json.RawMessage `json:"Actions" swaggertype:"object"`
	Dismissible      bool            `json:"Dismissible"`
	PublishedAt      *time.Time      `json:"PublishedAt"`
	UpdatedByUserID  *uuid.UUID      `json:"UpdatedByUserID"`
	CreatedAt        time.Time       `json:"CreatedAt"`
	UpdatedAt        time.Time       `json:"UpdatedAt"`
	// Source is "platform" for an inherited platform version (read-only) and
	// "event" for a row owned by this Event.
	Source string `json:"Source" enums:"platform,event"`
}

func eventEmailTemplateToResponse(t emailModel.EmailTemplate, source string) eventEmailTemplateResponse {
	return eventEmailTemplateResponse{ID: t.ID, ScopeEventID: t.ScopeEventID, NotificationType: t.NotificationType, Status: string(t.Status), Subject: t.Subject, Preheader: t.Preheader, Body: t.Body, Styling: t.Styling, PublishedAt: t.PublishedAt, UpdatedByUserID: t.UpdatedByUserID, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Source: source}
}

// ownedEventEmailTemplateResponse renders a row returned by a mutation, which
// is always owned by the Event.
func ownedEventEmailTemplateResponse(t emailModel.EmailTemplate) eventEmailTemplateResponse {
	return eventEmailTemplateToResponse(t, eventUseCase.TemplateSourceEvent)
}

func eventInAppTemplateToResponse(t inAppModel.InAppTemplate, source string) eventInAppTemplateResponse {
	return eventInAppTemplateResponse{ID: t.ID, ScopeEventID: t.ScopeEventID, NotificationType: t.NotificationType, Status: string(t.Status), Title: t.Title, Body: t.Body, Link: t.Link, Icon: t.Icon, Tone: t.Tone, AccentColor: t.AccentColor, Surface: t.Surface, AutoDismissMs: t.AutoDismissMs, Actions: t.Actions, Dismissible: t.Dismissible, PublishedAt: t.PublishedAt, UpdatedByUserID: t.UpdatedByUserID, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt, Source: source}
}

// ownedEventInAppTemplateResponse renders a row returned by a mutation, which
// is always owned by the Event.
func ownedEventInAppTemplateResponse(t inAppModel.InAppTemplate) eventInAppTemplateResponse {
	return eventInAppTemplateToResponse(t, eventUseCase.TemplateSourceEvent)
}

func eventTemplateIDs(ctx *gin.Context) (uuid.UUID, uuid.UUID, bool) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return uuid.Nil, uuid.Nil, false
	}
	templateID, err := uuid.FromString(ctx.Param("templateID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, false
	}
	return eventID, templateID, true
}

func eventTemplateActor(ctx *gin.Context) (uuid.UUID, bool) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return uuid.Nil, false
	}
	return claims.UserID, true
}

// @Summary List event-scoped email templates
// @Tags events
// @Param id path string true "event ID"
// @Param type query string false "notification type"
// @Param status query string false "template status"
// @Success 200 {object} response.Response{data=[]eventEmailTemplateResponse}
// @Router /events/{id}/manage/notification-templates/email [get]
func (h *Handler) listEventEmailTemplates(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListEventEmailTemplates(ctx, eventID, emailModel.ListFilter{Type: ctx.Query("type"), Status: ctx.Query("status")})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]eventEmailTemplateResponse, 0, len(items))
	for _, item := range items {
		out = append(out, eventEmailTemplateToResponse(item.EmailTemplate, item.Source))
	}
	response.AbortWithData(ctx, out)
}

// @Summary Create an event-scoped email template draft
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param body body eventEmailTemplateRequest true "template fields"
// @Success 200 {object} response.Response{data=eventEmailTemplateResponse}
// @Router /events/{id}/manage/notification-templates/email [post]
func (h *Handler) createEventEmailTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req eventEmailTemplateRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.CreateEventEmailTemplate(ctx, eventID, emailModel.CreateTemplateInput{NotificationType: req.NotificationType, Subject: req.Subject, Preheader: req.Preheader, Body: req.Body, Styling: req.Styling, UpdatedBy: actor})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventEmailTemplateResponse(t))
}

// @Summary Get an event-scoped email template
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Success 200 {object} response.Response{data=eventEmailTemplateResponse}
// @Router /events/{id}/manage/notification-templates/email/{templateID} [get]
func (h *Handler) getEventEmailTemplate(ctx *gin.Context) {
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	t, err := h.useCase.GetEventEmailTemplate(ctx, eventID, templateID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, eventEmailTemplateToResponse(t.EmailTemplate, t.Source))
}

// @Summary Update an event-scoped email template draft
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Param body body eventEmailTemplateRequest true "template fields"
// @Success 200 {object} response.Response{data=eventEmailTemplateResponse}
// @Router /events/{id}/manage/notification-templates/email/{templateID} [put]
func (h *Handler) updateEventEmailTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	var req eventEmailTemplateRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.UpdateEventEmailTemplate(ctx, eventID, emailModel.UpdateTemplateInput{ID: templateID, Subject: req.Subject, Preheader: req.Preheader, Body: req.Body, Styling: req.Styling, UpdatedBy: actor})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventEmailTemplateResponse(t))
}

// @Summary Delete an event-scoped email template draft
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/notification-templates/email/{templateID} [delete]
func (h *Handler) deleteEventEmailTemplate(ctx *gin.Context) {
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DeleteEventEmailTemplate(ctx, eventID, templateID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// resetEventEmailTemplateType godoc
// @Summary Reset an event email template type to the platform template
// @Description Deletes every Event-owned row (draft, published and history) of one Event-scoped notification type, so the Event inherits the platform template again. Idempotent.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param notificationType path string true "Event-scoped notification type"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/notification-templates/email/type/{notificationType} [delete]
func (h *Handler) resetEventEmailTemplateType(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.ResetEventEmailTemplateType(ctx, eventID, ctx.Param("notificationType")); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// @Summary Publish an event-scoped email template draft
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Success 200 {object} response.Response{data=eventEmailTemplateResponse}
// @Router /events/{id}/manage/notification-templates/email/{templateID}/publish [post]
func (h *Handler) publishEventEmailTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	t, err := h.useCase.PublishEventEmailTemplate(ctx, eventID, templateID, actor)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventEmailTemplateResponse(t))
}

// @Summary Create an event-scoped email draft from an unpublished version
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Success 200 {object} response.Response{data=eventEmailTemplateResponse}
// @Router /events/{id}/manage/notification-templates/email/{templateID}/rollback [post]
func (h *Handler) rollbackEventEmailTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	t, err := h.useCase.RollbackEventEmailTemplate(ctx, eventID, templateID, actor)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventEmailTemplateResponse(t))
}

// @Summary Customize an inherited email template for the event
// @Description Copies the platform published version of an Event-scoped type
// @Description into a new Event draft (same subject, preheader, body and
// @Description styling). From then on the type is overridden for this Event.
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "platform published template ID (Source=platform)"
// @Success 200 {object} response.Response{data=eventEmailTemplateResponse}
// @Router /events/{id}/manage/notification-templates/email/{templateID}/customize [post]
func (h *Handler) customizeEventEmailTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	t, err := h.useCase.CustomizeEventEmailTemplate(ctx, eventID, templateID, actor)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventEmailTemplateResponse(t))
}

type templateTestResponse struct {
	Recipient string `json:"Recipient"`
}

// @Summary Send an Event email template to yourself (sample data, real Event)
// @Description Queues the template (Event row or inherited platform row) to the current user through the normal dispatcher: Event sender, Event SMTP with platform fallback, delivery journal.
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Success 200 {object} response.Response{data=templateTestResponse}
// @Router /events/{id}/manage/notification-templates/email/{templateID}/test [post]
func (h *Handler) testEventEmailTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	recipient, err := h.useCase.SendEventEmailTemplateTest(ctx, eventID, templateID, actor)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, templateTestResponse{Recipient: recipient})
}

// @Summary List event-scoped in-app templates
// @Tags events
// @Param id path string true "event ID"
// @Param type query string false "notification type"
// @Param status query string false "template status"
// @Success 200 {object} response.Response{data=[]eventInAppTemplateResponse}
// @Router /events/{id}/manage/notification-templates/in-app [get]
func (h *Handler) listEventInAppTemplates(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListEventInAppTemplates(ctx, eventID, inAppModel.ListFilter{Type: ctx.Query("type"), Status: ctx.Query("status")})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]eventInAppTemplateResponse, 0, len(items))
	for _, item := range items {
		out = append(out, eventInAppTemplateToResponse(item.InAppTemplate, item.Source))
	}
	response.AbortWithData(ctx, out)
}

// @Summary Create an event-scoped in-app template draft
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param body body eventInAppTemplateRequest true "template fields"
// @Success 200 {object} response.Response{data=eventInAppTemplateResponse}
// @Router /events/{id}/manage/notification-templates/in-app [post]
func (h *Handler) createEventInAppTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req eventInAppTemplateRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.CreateEventInAppTemplate(ctx, eventID, inAppModel.CreateTemplateInput{NotificationType: req.NotificationType, Title: req.Title, Body: req.Body, Link: req.Link, Icon: req.Icon, Tone: req.Tone, AccentColor: req.AccentColor, Surface: req.Surface, AutoDismissMs: req.AutoDismissMs, Actions: req.Actions, Dismissible: req.Dismissible, UpdatedBy: actor})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventInAppTemplateResponse(t))
}

// @Summary Get an event-scoped in-app template
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Success 200 {object} response.Response{data=eventInAppTemplateResponse}
// @Router /events/{id}/manage/notification-templates/in-app/{templateID} [get]
func (h *Handler) getEventInAppTemplate(ctx *gin.Context) {
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	t, err := h.useCase.GetEventInAppTemplate(ctx, eventID, templateID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, eventInAppTemplateToResponse(t.InAppTemplate, t.Source))
}

// @Summary Update an event-scoped in-app template draft
// @Tags events
// @Accept json
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Param body body eventInAppTemplateRequest true "template fields"
// @Success 200 {object} response.Response{data=eventInAppTemplateResponse}
// @Router /events/{id}/manage/notification-templates/in-app/{templateID} [put]
func (h *Handler) updateEventInAppTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	var req eventInAppTemplateRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.UpdateEventInAppTemplate(ctx, eventID, inAppModel.UpdateTemplateInput{ID: templateID, Title: req.Title, Body: req.Body, Link: req.Link, Icon: req.Icon, Tone: req.Tone, AccentColor: req.AccentColor, Surface: req.Surface, AutoDismissMs: req.AutoDismissMs, Actions: req.Actions, Dismissible: req.Dismissible, UpdatedBy: actor})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventInAppTemplateResponse(t))
}

// @Summary Delete an event-scoped in-app template draft
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/notification-templates/in-app/{templateID} [delete]
func (h *Handler) deleteEventInAppTemplate(ctx *gin.Context) {
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DeleteEventInAppTemplate(ctx, eventID, templateID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// resetEventInAppTemplateType godoc
// @Summary Reset an event in-app template type to the platform template
// @Description Deletes every Event-owned row (draft, published and history) of one Event-scoped notification type, so the Event inherits the platform template again. Idempotent.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param notificationType path string true "Event-scoped notification type"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/notification-templates/in-app/type/{notificationType} [delete]
func (h *Handler) resetEventInAppTemplateType(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.ResetEventInAppTemplateType(ctx, eventID, ctx.Param("notificationType")); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// @Summary Publish an event-scoped in-app template draft
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Success 200 {object} response.Response{data=eventInAppTemplateResponse}
// @Router /events/{id}/manage/notification-templates/in-app/{templateID}/publish [post]
func (h *Handler) publishEventInAppTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	t, err := h.useCase.PublishEventInAppTemplate(ctx, eventID, templateID, actor)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventInAppTemplateResponse(t))
}

// @Summary Create an event-scoped in-app draft from an unpublished version
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "template ID"
// @Success 200 {object} response.Response{data=eventInAppTemplateResponse}
// @Router /events/{id}/manage/notification-templates/in-app/{templateID}/rollback [post]
func (h *Handler) rollbackEventInAppTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	t, err := h.useCase.RollbackEventInAppTemplate(ctx, eventID, templateID, actor)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventInAppTemplateResponse(t))
}

// @Summary Customize an inherited in-app template for the event
// @Description Copies the platform published version of an Event-scoped type
// @Description into a new Event draft. From then on the type is overridden
// @Description for this Event.
// @Tags events
// @Param id path string true "event ID"
// @Param templateID path string true "platform published template ID (Source=platform)"
// @Success 200 {object} response.Response{data=eventInAppTemplateResponse}
// @Router /events/{id}/manage/notification-templates/in-app/{templateID}/customize [post]
func (h *Handler) customizeEventInAppTemplate(ctx *gin.Context) {
	actor, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	t, err := h.useCase.CustomizeEventInAppTemplate(ctx, eventID, templateID, actor)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, ownedEventInAppTemplateResponse(t))
}

// eventEmailPreviewRequest is an Event template draft to render; it need not
// be saved.
type eventEmailPreviewRequest struct {
	NotificationType string          `json:"NotificationType"`
	Subject          string          `json:"Subject"`
	Preheader        string          `json:"Preheader"`
	Body             json.RawMessage `json:"Body" swaggertype:"object"`
	Styling          json.RawMessage `json:"Styling" swaggertype:"object"`
	// Values are sample variable values; missing variables use the type's
	// defaults.
	Values map[string]string `json:"Values"`
}

// eventEmailPreviewResponse is the rendered draft. HTML is the dispatched HTML
// (hidden preheader span included) with inline images embedded as data: URIs
// instead of cid: parts, so it displays without any image request.
type eventEmailPreviewResponse struct {
	Subject   string `json:"Subject"`
	Preheader string `json:"Preheader"`
	HTML      string `json:"HTML"`
}

// @Summary Preview an event-scoped email template draft
// @Description Renders subject, preheader and body with the dispatch renderer, the Event brand and sample variables (type defaults overlaid by Values). Images are embedded as data: URIs instead of cid: parts; every uploaded image (presets included) must be usable by this Event, and a missing/non-image/foreign file or inline images over the per-email cap are 400. Only Event-scoped types; nothing is persisted.
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body eventEmailPreviewRequest true "template draft"
// @Success 200 {object} response.Response{data=eventEmailPreviewResponse}
// @Failure 400 {object} response.Response
// @Router /events/{id}/manage/notification-templates/email/preview [post]
func (h *Handler) previewEventEmail(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var req eventEmailPreviewRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	out, err := h.useCase.PreviewEventEmail(ctx, eventID, emailUseCase.PreviewInput{
		NotificationType: req.NotificationType,
		Subject:          req.Subject,
		Preheader:        req.Preheader,
		Body:             req.Body,
		Styling:          req.Styling,
		Values:           req.Values,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, eventEmailPreviewResponse{Subject: out.Subject, Preheader: out.Preheader, HTML: out.HTML})
}

// @Summary Upload an image to an Event-owned email draft
// @Tags events
// @Accept multipart/form-data
// @Produce json
// @Param id path string true "event ID"
// @Param templateID path string true "draft template ID"
// @Param file formData file true "image payload"
// @Success 200 {object} response.Response{data=eventEmailImageUploadResponse}
// @Router /events/{id}/manage/notification-templates/email/{templateID}/images [post]
func (h *Handler) uploadEventEmailImage(ctx *gin.Context) {
	eventID, templateID, ok := eventTemplateIDs(ctx)
	if !ok {
		return
	}
	userID, ok := eventTemplateActor(ctx)
	if !ok {
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, emailUseCase.MaxTemplateImageUploadBytes+eventEmailImageMultipartSlack)
	fh, err := ctx.FormFile("file")
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			response.AbortWithError(ctx, notificationModel.ErrTemplateImageTooLarge.WithError(err).Err())
		} else {
			response.AbortWithBadRequest(ctx, err)
		}
		return
	}
	file, err := fh.Open()
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	defer func() { _ = file.Close() }()
	uploaded, err := h.useCase.UploadEventEmailImage(ctx, eventID, templateID, userID, file, fh.Filename)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, eventEmailImageUploadResponse{
		FileID: uploaded.ID,
		Url:    "/api/events/" + eventID.String() + "/manage/notification-templates/email/images/" + uploaded.ID.String(),
	})
}

// @Summary Stream an image used by a saved email template
// @Description Serves PNG/JPEG files referenced by an email template or block preset (404 otherwise).
// @Tags events
// @Produce image/png,image/jpeg
// @Param id path string true "event ID"
// @Param fileID path string true "file ID"
// @Success 200 {file} binary
// @Failure 404 {object} response.Response
// @Router /events/{id}/manage/notification-templates/email/images/{fileID} [get]
func (h *Handler) streamEventEmailImage(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	rc, f, err := h.useCase.StreamEventEmailImage(ctx, eventID, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = rc.Close() }()

	ctx.Header("Content-Disposition", "inline")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Cache-Control", "private, max-age=31536000, immutable")
	ctx.DataFromReader(http.StatusOK, f.SizeBytes, f.ContentType, rc, nil)
}

// @Summary Brand logo used by this Event's email logo block
// @Tags events
// @Produce image/png
// @Param id path string true "event ID"
// @Success 200 {file} binary
// @Failure 404 {object} response.Response
// @Router /events/{id}/manage/notification-templates/email/brand/logo [get]
func (h *Handler) eventEmailBrandLogo(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	data, contentType, err := h.useCase.EventEmailBrandLogo(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Content-Disposition", "inline")
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Cache-Control", "private, max-age=86400")
	ctx.Data(http.StatusOK, contentType, data)
}

type eventEmailPresetResponse struct {
	ID          uuid.UUID       `json:"ID"`
	Name        string          `json:"Name"`
	Description string          `json:"Description"`
	Blocks      json.RawMessage `json:"Blocks" swaggertype:"object"`
}

// @Summary List the shared email block presets
// @Description Read-only: presets are managed by the platform and can be used as blocks or as the footer of an Event email.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]eventEmailPresetResponse}
// @Router /events/{id}/manage/notification-templates/email/presets [get]
func (h *Handler) listEventEmailPresets(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	presets, err := h.useCase.ListEventEmailPresets(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]eventEmailPresetResponse, 0, len(presets))
	for _, p := range presets {
		out = append(out, eventEmailPresetResponse{ID: p.ID, Name: p.Name, Description: p.Description, Blocks: p.Blocks})
	}
	response.AbortWithData(ctx, out)
}
