// Package mail is the HTTP layer of the W7 mail settings: the platform SMTP
// providers (admin «Налаштування → Пошта») and the Event mail section (sender, Reply-To,
// optional Event SMTP, delivery journal). Both levels share one model:
// sender + Reply-To + SMTP (the platform has a prioritized list of providers,
// an Event at most one server).
package mail

import (
	"context"
	"encoding/json"
	"errors"
	"io"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/notification/stats"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/internal/model/rbac"
	mailUseCase "github.com/cybericebox/daemon/internal/useCase/mail"
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
		GetPlatformMailSettings(ctx context.Context) (mailUseCase.PlatformSettingsView, error)
		UpdatePlatformIdentity(ctx context.Context, in mailModel.Identity, sendingDomain string, by uuid.UUID) (mailUseCase.PlatformSettingsView, error)
		UpdatePlatformFooter(ctx context.Context, content json.RawMessage, by uuid.UUID) (mailUseCase.PlatformSettingsView, error)
		PreviewPlatformFooter(ctx context.Context, content json.RawMessage) (mailUseCase.FooterPreview, error)
		CreatePlatformProvider(ctx context.Context, in mailModel.ProviderInput, by uuid.UUID) (mailUseCase.PlatformSettingsView, error)
		UpdatePlatformProvider(ctx context.Context, id uuid.UUID, in mailModel.ProviderInput, by uuid.UUID) (mailUseCase.PlatformSettingsView, error)
		SetPlatformProviderEnabled(ctx context.Context, id uuid.UUID, enabled bool, by uuid.UUID) (mailUseCase.PlatformSettingsView, error)
		DeletePlatformProvider(ctx context.Context, id uuid.UUID) (mailUseCase.PlatformSettingsView, error)
		ReorderPlatformProviders(ctx context.Context, ids []uuid.UUID) (mailUseCase.PlatformSettingsView, error)
		TestPlatformProvider(ctx context.Context, id *uuid.UUID, in *mailModel.SMTPInput, userID uuid.UUID) (mailUseCase.TestResult, error)
		GetEventMailSettings(ctx context.Context, eventID uuid.UUID) (mailUseCase.EventSettingsView, error)
		UpdateEventIdentity(ctx context.Context, eventID uuid.UUID, in mailModel.Identity, by uuid.UUID) (mailUseCase.EventSettingsView, error)
		UpdateEventSMTP(ctx context.Context, eventID uuid.UUID, in mailModel.SMTPInput, by uuid.UUID) (mailUseCase.EventSettingsView, error)
		DeleteEventSMTP(ctx context.Context, eventID uuid.UUID) (mailUseCase.EventSettingsView, error)
		TestEventSMTP(ctx context.Context, eventID uuid.UUID, in *mailModel.SMTPInput, userID uuid.UUID) (mailUseCase.TestResult, error)
		ListEventMailJournal(ctx context.Context, eventID uuid.UUID, f dispatchModel.ListDispatchesFilter) ([]dispatchModel.DispatchDetail, int64, error)
		RequireManageEvent(ctx context.Context, eventID, userID uuid.UUID) error
		RequireReadEvent(ctx context.Context, eventID, userID uuid.UUID) error
	}
)

// smtpRequest is the SMTP form. Password is write-only: empty keeps the
// stored one, ClearPassword removes it.
type smtpRequest struct {
	Host          string `json:"Host"`
	Port          int    `json:"Port"`
	TLSMode       string `json:"TLSMode"`
	Username      string `json:"Username"`
	Password      string `json:"Password"`
	ClearPassword bool   `json:"ClearPassword"`
	// Send limits of this server; null or 0 = not set here (env, then none).
	MaxPerSecond *float64 `json:"MaxPerSecond"`
	DailyQuota   *int     `json:"DailyQuota"`
}

func (r smtpRequest) input() mailModel.SMTPInput {
	return mailModel.SMTPInput{
		Host: r.Host, Port: r.Port, TLSMode: mailModel.TLSMode(r.TLSMode), Username: r.Username,
		Password: r.Password, ClearPassword: r.ClearPassword,
		MaxPerSecond: r.MaxPerSecond, DailyQuota: r.DailyQuota,
	}
}

// providerRequest is the platform SMTP provider form: the SMTP fields plus a
// name, an optional own sender (empty = the platform sender) and the state.
// Enabled omitted keeps the stored state (a new provider is enabled).
type providerRequest struct {
	smtpRequest
	Name    string       `json:"Name"`
	Enabled *bool        `json:"Enabled"`
	Sender  partyRequest `json:"Sender"`
	ReplyTo partyRequest `json:"ReplyTo"`
}

func (r providerRequest) input() mailModel.ProviderInput {
	return mailModel.ProviderInput{
		SMTPInput: r.smtpRequest.input(), Name: r.Name, Enabled: r.Enabled,
		Identity: mailModel.Identity{
			FromName: r.Sender.Name, FromAddress: r.Sender.Address,
			ReplyToName: r.ReplyTo.Name, ReplyToAddress: r.ReplyTo.Address,
		},
	}
}

// providerTestRequest tests a provider: the form values (Host set) with the
// stored password of ID when the password is empty, the stored provider ID, or
// the transport in use (neither).
type providerTestRequest struct {
	smtpRequest
	ID string `json:"ID"`
}

type enabledRequest struct {
	Enabled bool `json:"Enabled"`
}

type orderRequest struct {
	IDs []string `json:"IDs"`
}

// footerRequest is the platform email footer, a Lexical document (the format of
// the email body rich_text blocks); null or empty = default.
type footerRequest struct {
	Content json.RawMessage `json:"Content" swaggertype:"object"`
}

type partyRequest struct {
	Name    string `json:"Name"`
	Address string `json:"Address"`
}

// identityRequest is the sender + Reply-To form; empty fields inherit.
// SendingDomain applies to the platform only (Events send from
// <tag>@SendingDomain) and is ignored on the Event route.
type identityRequest struct {
	Sender        partyRequest `json:"Sender"`
	ReplyTo       partyRequest `json:"ReplyTo"`
	SendingDomain string       `json:"SendingDomain"`
}

func (r identityRequest) input() mailModel.Identity {
	return mailModel.Identity{
		FromName: r.Sender.Name, FromAddress: r.Sender.Address,
		ReplyToName: r.ReplyTo.Name, ReplyToAddress: r.ReplyTo.Address,
	}
}

func NewMailAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	platform := router.Group("mail/settings")
	platform.GET("", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.getPlatform)
	platform.PUT("identity", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.updatePlatformIdentity)
	platform.PUT("footer", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.updatePlatformFooter)
	platform.POST("footer/preview", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.previewPlatformFooter)
	write := h.prot.RequirePermission(rbac.PermPlatformSettingsWrite)
	platform.POST("providers", write, h.createProvider)
	platform.PUT("providers/order", write, h.reorderProviders)
	platform.POST("providers/test", write, h.testProviderForm)
	platform.PUT("providers/:providerId", write, h.updateProvider)
	platform.PATCH("providers/:providerId/enabled", write, h.setProviderEnabled)
	platform.DELETE("providers/:providerId", write, h.deleteProvider)
	platform.POST("providers/:providerId/test", write, h.testProvider)

	manage := router.Group("events/:id/manage/mail", h.prot.RequirePermission(rbac.PermSelf))
	manage.GET("", h.requireRead, h.getEvent)
	manage.PUT("identity", h.requireManage, h.updateEventIdentity)
	manage.PUT("smtp", h.requireManage, h.updateEventSMTP)
	manage.DELETE("smtp", h.requireManage, h.deleteEventSMTP)
	manage.POST("smtp/test", h.requireManage, h.testEventSMTP)
	manage.GET("journal", h.requireRead, h.eventJournal)
}

// getPlatform godoc
// @Summary  Platform mail settings: sender, Reply-To, SMTP; the password is never returned
// @Tags     mail
// @Produce  json
// @Success  200  {object}  response.Response{data=mailUseCase.PlatformSettingsView}
// @Router   /mail/settings [get]
func (h *Handler) getPlatform(ctx *gin.Context) {
	view, err := h.useCase.GetPlatformMailSettings(ctx.Request.Context())
	respond(ctx, view, err)
}

// updatePlatformIdentity godoc
// @Summary  Save the platform sender, Reply-To and sending domain
// @Tags     mail
// @Accept   json
// @Produce  json
// @Param    body  body  identityRequest  true  "Sender and Reply-To"
// @Success  200  {object}  response.Response{data=mailUseCase.PlatformSettingsView}
// @Router   /mail/settings/identity [put]
func (h *Handler) updatePlatformIdentity(ctx *gin.Context) {
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	var req identityRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.UpdatePlatformIdentity(ctx.Request.Context(), req.input(), req.SendingDomain, userID)
	respond(ctx, view, err)
}

// updatePlatformFooter godoc
// @Summary  Save the platform email footer document (empty restores the default)
// @Tags     mail
// @Accept   json
// @Produce  json
// @Param    body  body  footerRequest  true  "Footer document"
// @Success  200  {object}  response.Response{data=mailUseCase.PlatformSettingsView}
// @Router   /mail/settings/footer [put]
func (h *Handler) updatePlatformFooter(ctx *gin.Context) {
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	var req footerRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.UpdatePlatformFooter(ctx.Request.Context(), req.Content, userID)
	respond(ctx, view, err)
}

// previewPlatformFooter godoc
// @Summary  Render an unsaved footer document as it would be sent
// @Tags     mail
// @Accept   json
// @Produce  json
// @Param    body  body  footerRequest  true  "Footer document"
// @Success  200  {object}  response.Response{data=mailUseCase.FooterPreview}
// @Router   /mail/settings/footer/preview [post]
func (h *Handler) previewPlatformFooter(ctx *gin.Context) {
	var req footerRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	preview, err := h.useCase.PreviewPlatformFooter(ctx.Request.Context(), req.Content)
	respond(ctx, preview, err)
}

// createProvider godoc
// @Summary  Add a platform SMTP provider (last in the priority order)
// @Tags     mail
// @Accept   json
// @Produce  json
// @Param    body  body  providerRequest  true  "Provider"
// @Success  200  {object}  response.Response{data=mailUseCase.PlatformSettingsView}
// @Router   /mail/settings/providers [post]
func (h *Handler) createProvider(ctx *gin.Context) {
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	var req providerRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.CreatePlatformProvider(ctx.Request.Context(), req.input(), userID)
	respond(ctx, view, err)
}

// updateProvider godoc
// @Summary  Save a platform SMTP provider (empty password keeps the stored one)
// @Tags     mail
// @Accept   json
// @Produce  json
// @Param    providerId  path  string           true  "Provider ID"
// @Param    body        body  providerRequest  true  "Provider"
// @Success  200  {object}  response.Response{data=mailUseCase.PlatformSettingsView}
// @Router   /mail/settings/providers/{providerId} [put]
func (h *Handler) updateProvider(ctx *gin.Context) {
	id, ok := parseProviderID(ctx)
	if !ok {
		return
	}
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	var req providerRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.UpdatePlatformProvider(ctx.Request.Context(), id, req.input(), userID)
	respond(ctx, view, err)
}

// setProviderEnabled godoc
// @Summary  Switch a platform SMTP provider on or off
// @Tags     mail
// @Accept   json
// @Produce  json
// @Param    providerId  path  string          true  "Provider ID"
// @Param    body        body  enabledRequest  true  "State"
// @Success  200  {object}  response.Response{data=mailUseCase.PlatformSettingsView}
// @Router   /mail/settings/providers/{providerId}/enabled [patch]
func (h *Handler) setProviderEnabled(ctx *gin.Context) {
	id, ok := parseProviderID(ctx)
	if !ok {
		return
	}
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	var req enabledRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.SetPlatformProviderEnabled(ctx.Request.Context(), id, req.Enabled, userID)
	respond(ctx, view, err)
}

// deleteProvider godoc
// @Summary  Delete a platform SMTP provider
// @Tags     mail
// @Produce  json
// @Param    providerId  path  string  true  "Provider ID"
// @Success  200  {object}  response.Response{data=mailUseCase.PlatformSettingsView}
// @Router   /mail/settings/providers/{providerId} [delete]
func (h *Handler) deleteProvider(ctx *gin.Context) {
	id, ok := parseProviderID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.DeletePlatformProvider(ctx.Request.Context(), id)
	respond(ctx, view, err)
}

// reorderProviders godoc
// @Summary  Set the provider priority order (listed IDs first, in that order)
// @Tags     mail
// @Accept   json
// @Produce  json
// @Param    body  body  orderRequest  true  "Provider IDs in priority order"
// @Success  200  {object}  response.Response{data=mailUseCase.PlatformSettingsView}
// @Router   /mail/settings/providers/order [put]
func (h *Handler) reorderProviders(ctx *gin.Context) {
	var req orderRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.IDs))
	for _, raw := range req.IDs {
		id, err := uuid.FromString(raw)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		ids = append(ids, id)
	}
	view, err := h.useCase.ReorderPlatformProviders(ctx.Request.Context(), ids)
	respond(ctx, view, err)
}

// testProvider godoc
// @Summary  Send a test email to the current user through a stored provider
// @Tags     mail
// @Produce  json
// @Param    providerId  path  string  true  "Provider ID"
// @Success  200  {object}  response.Response{data=mailUseCase.TestResult}
// @Router   /mail/settings/providers/{providerId}/test [post]
func (h *Handler) testProvider(ctx *gin.Context) {
	id, ok := parseProviderID(ctx)
	if !ok {
		return
	}
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	result, err := h.useCase.TestPlatformProvider(ctx.Request.Context(), &id, nil, userID)
	respond(ctx, result, err)
}

// testProviderForm godoc
// @Summary  Send a test email through unsaved provider form values, or the transport in use
// @Description  With Host: the form values (an empty password uses the stored one of the provider ID, when given). Without Host and ID: the transport platform mail goes out through now (SMTP_* env, or the first available provider).
// @Tags     mail
// @Accept   json
// @Produce  json
// @Param    body  body  providerTestRequest  false  "Form values; empty body tests the transport in use"
// @Success  200  {object}  response.Response{data=mailUseCase.TestResult}
// @Router   /mail/settings/providers/test [post]
func (h *Handler) testProviderForm(ctx *gin.Context) {
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	var req providerTestRequest
	if err := ctx.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var id *uuid.UUID
	if req.ID != "" {
		parsed, err := uuid.FromString(req.ID)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		id = &parsed
	}
	var in *mailModel.SMTPInput
	if req.Host != "" {
		form := req.input()
		in = &form
	}
	result, err := h.useCase.TestPlatformProvider(ctx.Request.Context(), id, in, userID)
	respond(ctx, result, err)
}

// getEvent godoc
// @Summary  Event mail settings: sender, Reply-To, Event SMTP
// @Tags     event-mail
// @Produce  json
// @Param    id  path  string  true  "Event ID"
// @Success  200  {object}  response.Response{data=mailUseCase.EventSettingsView}
// @Router   /events/{id}/manage/mail [get]
func (h *Handler) getEvent(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.GetEventMailSettings(ctx.Request.Context(), eventID)
	respond(ctx, view, err)
}

// updateEventIdentity godoc
// @Summary  Save the Event sender and Reply-To (empty fields inherit)
// @Tags     event-mail
// @Accept   json
// @Produce  json
// @Param    id    path  string           true  "Event ID"
// @Param    body  body  identityRequest  true  "Sender and Reply-To"
// @Success  200  {object}  response.Response{data=mailUseCase.EventSettingsView}
// @Router   /events/{id}/manage/mail/identity [put]
func (h *Handler) updateEventIdentity(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	var req identityRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.UpdateEventIdentity(ctx.Request.Context(), eventID, req.input(), userID)
	respond(ctx, view, err)
}

// updateEventSMTP godoc
// @Summary  Save the Event SMTP (participant mail); failures fall back to the platform SMTP
// @Tags     event-mail
// @Accept   json
// @Produce  json
// @Param    id    path  string       true  "Event ID"
// @Param    body  body  smtpRequest  true  "SMTP settings"
// @Success  200  {object}  response.Response{data=mailUseCase.EventSettingsView}
// @Router   /events/{id}/manage/mail/smtp [put]
func (h *Handler) updateEventSMTP(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	var req smtpRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.UpdateEventSMTP(ctx.Request.Context(), eventID, req.input(), userID)
	respond(ctx, view, err)
}

// deleteEventSMTP godoc
// @Summary  Remove the Event SMTP (use the platform SMTP)
// @Tags     event-mail
// @Produce  json
// @Param    id  path  string  true  "Event ID"
// @Success  200  {object}  response.Response{data=mailUseCase.EventSettingsView}
// @Router   /events/{id}/manage/mail/smtp [delete]
func (h *Handler) deleteEventSMTP(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	view, err := h.useCase.DeleteEventSMTP(ctx.Request.Context(), eventID)
	respond(ctx, view, err)
}

// testEventSMTP godoc
// @Summary  Send a test email as the Event through its SMTP to the current user
// @Tags     event-mail
// @Accept   json
// @Produce  json
// @Param    id    path  string       true   "Event ID"
// @Param    body  body  smtpRequest  false  "SMTP settings under test; empty body tests the stored ones"
// @Success  200  {object}  response.Response{data=mailUseCase.TestResult}
// @Router   /events/{id}/manage/mail/smtp/test [post]
func (h *Handler) testEventSMTP(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	in, ok := optionalSMTP(ctx)
	if !ok {
		return
	}
	result, err := h.useCase.TestEventSMTP(ctx.Request.Context(), eventID, in, userID)
	respond(ctx, result, err)
}

// eventJournal godoc
// @Summary  Event delivery journal (same query as /notifications/dispatches, Event forced)
// @Tags     event-mail
// @Produce  json
// @Param    id         path   string  true   "Event ID"
// @Param    type       query  string  false  "notification type"
// @Param    channel    query  string  false  "target channel (email, in_app)"
// @Param    result     query  string  false  "target status (done, error)"
// @Param    transport  query  string  false  "email route (event, platform, env)"
// @Param    limit      query  int     false  "page size (default 50)"
// @Param    cursor     query  string  false  "last dispatch ID from the previous page"
// @Success  200  {object}  response.Response
// @Router   /events/{id}/manage/mail/journal [get]
func (h *Handler) eventJournal(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	f, limit, ok := stats.ParseJournalFilter(ctx)
	if !ok {
		return
	}
	rows, total, err := h.useCase.ListEventMailJournal(ctx.Request.Context(), eventID, f)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	stats.WriteJournalPage(ctx, rows, total, limit)
}

func (h *Handler) requireManage(ctx *gin.Context) {
	h.requireEvent(ctx, h.useCase.RequireManageEvent)
}

func (h *Handler) requireRead(ctx *gin.Context) {
	h.requireEvent(ctx, h.useCase.RequireReadEvent)
}

func (h *Handler) requireEvent(ctx *gin.Context, check func(context.Context, uuid.UUID, uuid.UUID) error) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	userID, ok := currentUser(ctx)
	if !ok {
		return
	}
	if err := check(ctx, eventID, userID); err != nil {
		response.AbortWithError(ctx, err)
	}
}

// optionalSMTP reads an optional SMTP form: an empty body (or {}) means
// "test the stored settings".
func optionalSMTP(ctx *gin.Context) (*mailModel.SMTPInput, bool) {
	var req smtpRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, true
		}
		response.AbortWithBadRequest(ctx, err)
		return nil, false
	}
	if req.Host == "" {
		return nil, true
	}
	in := req.input()
	return &in, true
}

func currentUser(ctx *gin.Context) (uuid.UUID, bool) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return uuid.Nil, false
	}
	return claims.UserID, true
}

func parseProviderID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("providerId"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}

func parseEventID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}

func respond(ctx *gin.Context, data any, err error) {
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, data)
}
