package template

import (
	"context"
	"encoding/json"
	"io"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	"github.com/cybericebox/daemon/internal/model/rbac"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	// IProtection is the subset of the protection middleware this handler needs.
	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		// Email templates
		GetEmailTemplate(ctx context.Context, id uuid.UUID) (emailModel.EmailTemplate, error)
		CreateEmailTemplate(
			ctx context.Context,
			in emailModel.CreateTemplateInput,
		) (emailModel.EmailTemplate, error)
		UpdateEmailTemplate(
			ctx context.Context,
			in emailModel.UpdateTemplateInput,
		) (emailModel.EmailTemplate, error)
		DeleteEmailTemplate(ctx context.Context, id uuid.UUID) error
		ListEmailTemplates(
			ctx context.Context,
			filter emailModel.ListFilter,
		) (emailModel.ListResult, error)
		LatestEmailTemplates(ctx context.Context) ([]emailModel.TypeVersions, error)
		PublishEmailTemplate(
			ctx context.Context,
			id, updatedBy uuid.UUID,
		) (emailModel.EmailTemplate, error)
		RollbackEmailTemplate(
			ctx context.Context,
			sourceID, updatedBy uuid.UUID,
		) (emailModel.EmailTemplate, error)

		// Email template images
		MaxEmailImageUploadBytes() int64
		UploadEmailImage(ctx context.Context, r io.Reader, name string, userID uuid.UUID) (mediaModel.File, error)
		StreamEmailImage(ctx context.Context, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error)

		// Email template preview
		PreviewEmail(ctx context.Context, in emailUseCase.PreviewInput) (emailUseCase.PreviewOutput, error)

		// In-app templates
		GetInAppTemplate(ctx context.Context, id uuid.UUID) (inAppModel.InAppTemplate, error)
		CreateInAppTemplate(
			ctx context.Context,
			in inAppModel.CreateTemplateInput,
		) (inAppModel.InAppTemplate, error)
		UpdateInAppTemplate(
			ctx context.Context,
			in inAppModel.UpdateTemplateInput,
		) (inAppModel.InAppTemplate, error)
		DeleteInAppTemplate(ctx context.Context, id uuid.UUID) error
		ListInAppTemplates(
			ctx context.Context,
			filter inAppModel.ListFilter,
		) (inAppModel.ListResult, error)
		LatestInAppTemplates(ctx context.Context) ([]inAppModel.TypeVersions, error)
		PublishInAppTemplate(
			ctx context.Context,
			id, updatedBy uuid.UUID,
		) (inAppModel.InAppTemplate, error)
		RollbackInAppTemplate(
			ctx context.Context,
			sourceID, updatedBy uuid.UUID,
		) (inAppModel.InAppTemplate, error)

		// Email block presets
		ListEmailBlockPresets(ctx context.Context) ([]emailModel.BlockPreset, error)
		GetEmailBlockPreset(ctx context.Context, id uuid.UUID) (emailModel.BlockPreset, error)
		CreateEmailBlockPreset(
			ctx context.Context,
			in emailModel.PresetInput,
		) (emailModel.BlockPreset, error)
		UpdateEmailBlockPreset(
			ctx context.Context,
			id uuid.UUID,
			in emailModel.PresetInput,
		) (emailModel.BlockPreset, error)
		DeleteEmailBlockPreset(ctx context.Context, id uuid.UUID) error
	}
)

// ── in-app DTOs ──

type inAppTemplateResponse struct {
	ID               uuid.UUID       `json:"ID"`
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
	Actions          json.RawMessage `json:"Actions"          swaggertype:"object"`
	Dismissible      bool            `json:"Dismissible"`
	PublishedAt      *time.Time      `json:"PublishedAt"`
	UpdatedByUserID  *uuid.UUID      `json:"UpdatedByUserID"`
	CreatedAt        time.Time       `json:"CreatedAt"`
	UpdatedAt        time.Time       `json:"UpdatedAt"`
}

type inAppListResponse struct {
	Templates        []inAppTemplateResponse `json:"Templates"`
	MissingActiveFor []string                `json:"MissingActiveFor"`
}

type inAppLatestResponse struct {
	NotificationType string                 `json:"NotificationType"`
	Draft            *inAppTemplateResponse `json:"Draft"`
	Published        *inAppTemplateResponse `json:"Published"`
	Unpublished      *inAppTemplateResponse `json:"Unpublished"`
}

type createInAppRequest struct {
	NotificationType string          `json:"NotificationType"`
	Title            string          `json:"Title"`
	Body             string          `json:"Body"`
	Link             string          `json:"Link"`
	Icon             string          `json:"Icon"`
	Tone             string          `json:"Tone"`
	AccentColor      string          `json:"AccentColor"`
	Surface          string          `json:"Surface"`
	AutoDismissMs    *int32          `json:"AutoDismissMs"`
	Actions          json.RawMessage `json:"Actions"          swaggertype:"object"`
	Dismissible      bool            `json:"Dismissible"`
}

type updateInAppRequest struct {
	Title         string          `json:"Title"`
	Body          string          `json:"Body"`
	Link          string          `json:"Link"`
	Icon          string          `json:"Icon"`
	Tone          string          `json:"Tone"`
	AccentColor   string          `json:"AccentColor"`
	Surface       string          `json:"Surface"`
	AutoDismissMs *int32          `json:"AutoDismissMs"`
	Actions       json.RawMessage `json:"Actions"       swaggertype:"object"`
	Dismissible   bool            `json:"Dismissible"`
}

// ── email DTOs ──

type emailTemplateResponse struct {
	ID               uuid.UUID       `json:"ID"`
	NotificationType string          `json:"NotificationType"`
	Status           string          `json:"Status"`
	Subject          string          `json:"Subject"`
	Preheader        string          `json:"Preheader"`
	Body             json.RawMessage `json:"Body"             swaggertype:"object"`
	Styling          json.RawMessage `json:"Styling"          swaggertype:"object"`
	PublishedAt      *time.Time      `json:"PublishedAt"`
	UpdatedByUserID  *uuid.UUID      `json:"UpdatedByUserID"`
	CreatedAt        time.Time       `json:"CreatedAt"`
	UpdatedAt        time.Time       `json:"UpdatedAt"`
}

type emailListResponse struct {
	Templates        []emailTemplateResponse `json:"Templates"`
	MissingActiveFor []string                `json:"MissingActiveFor"`
}

type emailLatestResponse struct {
	NotificationType string                 `json:"NotificationType"`
	Draft            *emailTemplateResponse `json:"Draft"`
	Published        *emailTemplateResponse `json:"Published"`
	Unpublished      *emailTemplateResponse `json:"Unpublished"`
}

type createEmailRequest struct {
	NotificationType string          `json:"NotificationType"`
	Subject          string          `json:"Subject"`
	Preheader        string          `json:"Preheader"`
	Body             json.RawMessage `json:"Body"             swaggertype:"object"`
	Styling          json.RawMessage `json:"Styling"          swaggertype:"object"`
}

type updateEmailRequest struct {
	Subject   string          `json:"Subject"`
	Preheader string          `json:"Preheader"`
	Body      json.RawMessage `json:"Body"      swaggertype:"object"`
	Styling   json.RawMessage `json:"Styling"   swaggertype:"object"`
}

// ── preset DTOs ──

type presetResponse struct {
	ID          uuid.UUID       `json:"ID"`
	Name        string          `json:"Name"`
	Description string          `json:"Description"`
	Blocks      json.RawMessage `json:"Blocks"      swaggertype:"object"`
	CreatedAt   time.Time       `json:"CreatedAt"`
	UpdatedAt   time.Time       `json:"UpdatedAt"`
}

type presetRequest struct {
	Name        string          `json:"Name"`
	Description string          `json:"Description"`
	Blocks      json.RawMessage `json:"Blocks"      swaggertype:"object"`
}

// ── mappers ──

func inAppToResponse(t inAppModel.InAppTemplate) inAppTemplateResponse {
	return inAppTemplateResponse{
		ID:               t.ID,
		NotificationType: t.NotificationType,
		Status:           string(t.Status),
		Title:            t.Title,
		Body:             t.Body,
		Link:             t.Link,
		Icon:             t.Icon,
		Tone:             t.Tone,
		AccentColor:      t.AccentColor,
		Surface:          t.Surface,
		AutoDismissMs:    t.AutoDismissMs,
		Actions:          t.Actions,
		Dismissible:      t.Dismissible,
		PublishedAt:      t.PublishedAt,
		UpdatedByUserID:  t.UpdatedByUserID,
		CreatedAt:        t.CreatedAt,
		UpdatedAt:        t.UpdatedAt,
	}
}

func emailToResponse(t emailModel.EmailTemplate) emailTemplateResponse {
	return emailTemplateResponse{
		ID:               t.ID,
		NotificationType: t.NotificationType,
		Status:           string(t.Status),
		Subject:          t.Subject,
		Preheader:        t.Preheader,
		Body:             t.Body,
		Styling:          t.Styling,
		PublishedAt:      t.PublishedAt,
		UpdatedByUserID:  t.UpdatedByUserID,
		CreatedAt:        t.CreatedAt,
		UpdatedAt:        t.UpdatedAt,
	}
}

func presetToResponse(p emailModel.BlockPreset) presetResponse {
	return presetResponse{
		ID:          p.ID,
		Name:        p.Name,
		Description: p.Description,
		Blocks:      p.Blocks,
		CreatedAt:   p.CreatedAt,
		UpdatedAt:   p.UpdatedAt,
	}
}

func NewTemplateAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	inapp := router.Group("templates/inapp")
	{
		inapp.GET("", h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead), h.listInApp)
		inapp.GET(
			"latest",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead),
			h.latestInApp,
		)
		inapp.GET(":id", h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead), h.getInApp)
		inapp.POST(
			"",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.createInApp,
		)
		inapp.PUT(
			":id",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.updateInApp,
		)
		inapp.DELETE(
			":id",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.deleteInApp,
		)
		inapp.POST(
			":id/publish",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.publishInApp,
		)
		inapp.POST(
			":id/rollback",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.rollbackInApp,
		)
	}
	emailG := router.Group("templates/email")
	{
		emailG.GET("", h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead), h.listEmail)
		emailG.GET(
			"latest",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead),
			h.latestEmail,
		)
		// Static image/brand segments are registered before the ":id" param
		// routes (see the exercise "files" routes).
		emailG.POST(
			"images",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.uploadEmailImage,
		)
		emailG.GET(
			"images/:fileID",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead),
			h.streamEmailImage,
		)
		emailG.GET(
			"brand/logo",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead),
			h.brandLogo,
		)
		emailG.POST(
			"preview",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead),
			h.previewEmail,
		)
		emailG.GET(":id", h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead), h.getEmail)
		emailG.POST(
			"",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.createEmail,
		)
		emailG.PUT(
			":id",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.updateEmail,
		)
		emailG.DELETE(
			":id",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.deleteEmail,
		)
		emailG.POST(
			":id/publish",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.publishEmail,
		)
		emailG.POST(
			":id/rollback",
			h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
			h.rollbackEmail,
		)

		presets := emailG.Group("block-presets")
		{
			presets.GET(
				"",
				h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead),
				h.listPresets,
			)
			presets.POST(
				"",
				h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
				h.createPreset,
			)
			presets.GET(
				":id",
				h.prot.RequirePermission(rbac.PermNotificationsTemplatesRead),
				h.getPreset,
			)
			presets.PUT(
				":id",
				h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
				h.updatePreset,
			)
			presets.DELETE(
				":id",
				h.prot.RequirePermission(rbac.PermNotificationsTemplatesWrite),
				h.deletePreset,
			)
		}
	}
}

// ── in-app routes ──

// listInApp godoc
// @Summary  List in-app notification templates
// @Tags     notification-templates
// @Produce  json
// @Param    type    query     string  false  "filter by notification type"
// @Param    status  query     string  false  "filter by status"
// @Success  200  {object}  response.Response{data=inAppListResponse}
// @Router   /notifications/templates/inapp [get]
func (h *Handler) listInApp(ctx *gin.Context) {
	filter := inAppModel.ListFilter{
		Type:   ctx.Query("type"),
		Status: ctx.Query("status"),
	}
	res, err := h.useCase.ListInAppTemplates(ctx, filter)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := inAppListResponse{
		Templates:        make([]inAppTemplateResponse, 0, len(res.Templates)),
		MissingActiveFor: res.MissingActiveFor,
	}
	if out.MissingActiveFor == nil {
		out.MissingActiveFor = []string{}
	}
	for _, t := range res.Templates {
		out.Templates = append(out.Templates, inAppToResponse(t))
	}
	response.AbortWithData(ctx, out)
}

// latestInApp godoc
// @Summary  Latest in-app notification template versions per type
// @Tags     notification-templates
// @Produce  json
// @Success  200  {object}  response.Response{data=[]inAppLatestResponse}
// @Router   /notifications/templates/inapp/latest [get]
func (h *Handler) latestInApp(ctx *gin.Context) {
	versions, err := h.useCase.LatestInAppTemplates(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]inAppLatestResponse, 0, len(versions))
	for _, v := range versions {
		entry := inAppLatestResponse{NotificationType: v.NotificationType}
		if v.Draft != nil {
			entry.Draft = new(inAppToResponse(*v.Draft))
		}
		if v.Published != nil {
			entry.Published = new(inAppToResponse(*v.Published))
		}
		if v.Unpublished != nil {
			entry.Unpublished = new(inAppToResponse(*v.Unpublished))
		}
		out = append(out, entry)
	}
	response.AbortWithData(ctx, out)
}

// getInApp godoc
// @Summary  Get a single in-app notification template by ID
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "template ID"
// @Success  200  {object}  response.Response{data=inAppTemplateResponse}
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/inapp/{id} [get]
func (h *Handler) getInApp(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.GetInAppTemplate(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, inAppToResponse(t))
}

// createInApp godoc
// @Summary  Create an in-app notification template
// @Tags     notification-templates
// @Accept   json
// @Produce  json
// @Param    body  body      createInAppRequest      true  "template body"
// @Success  200   {object}  response.Response{data=inAppTemplateResponse}
// @Failure  400   {object}  response.Response
// @Router   /notifications/templates/inapp [post]
func (h *Handler) createInApp(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req createInAppRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.CreateInAppTemplate(ctx, inAppModel.CreateTemplateInput{
		NotificationType: req.NotificationType,
		Title:            req.Title,
		Body:             req.Body,
		Link:             req.Link,
		Icon:             req.Icon,
		Tone:             req.Tone,
		AccentColor:      req.AccentColor,
		Surface:          req.Surface,
		AutoDismissMs:    req.AutoDismissMs,
		Actions:          req.Actions,
		Dismissible:      req.Dismissible,
		UpdatedBy:        userID,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, inAppToResponse(t))
}

// updateInApp godoc
// @Summary  Update an in-app notification template
// @Tags     notification-templates
// @Accept   json
// @Produce  json
// @Param    id    path      string              true  "template ID"
// @Param    body  body      updateInAppRequest  true  "updated fields"
// @Success  200   {object}  response.Response{data=inAppTemplateResponse}
// @Failure  400   {object}  response.Response
// @Router   /notifications/templates/inapp/{id} [put]
func (h *Handler) updateInApp(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req updateInAppRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.UpdateInAppTemplate(ctx, inAppModel.UpdateTemplateInput{
		ID:            id,
		Title:         req.Title,
		Body:          req.Body,
		Link:          req.Link,
		Icon:          req.Icon,
		Tone:          req.Tone,
		AccentColor:   req.AccentColor,
		Surface:       req.Surface,
		AutoDismissMs: req.AutoDismissMs,
		Actions:       req.Actions,
		Dismissible:   req.Dismissible,
		UpdatedBy:     userID,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, inAppToResponse(t))
}

// deleteInApp godoc
// @Summary  Delete an in-app notification template
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "template ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/inapp/{id} [delete]
func (h *Handler) deleteInApp(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.DeleteInAppTemplate(ctx, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// publishInApp godoc
// @Summary  Publish an in-app notification template
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "template ID"
// @Success  200  {object}  response.Response{data=inAppTemplateResponse}
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/inapp/{id}/publish [post]
func (h *Handler) publishInApp(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.PublishInAppTemplate(ctx, id, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, inAppToResponse(t))
}

// rollbackInApp godoc
// @Summary  Restore a published or unpublished in-app template as a draft
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "source template ID"
// @Success  200  {object}  response.Response{data=inAppTemplateResponse}
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/inapp/{id}/rollback [post]
func (h *Handler) rollbackInApp(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.RollbackInAppTemplate(ctx, id, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, inAppToResponse(t))
}

// ── email routes ──

// listEmail godoc
// @Summary  List email notification templates
// @Tags     notification-templates
// @Produce  json
// @Param    type    query     string  false  "filter by notification type"
// @Param    status  query     string  false  "filter by status"
// @Success  200  {object}  response.Response{data=emailListResponse}
// @Router   /notifications/templates/email [get]
func (h *Handler) listEmail(ctx *gin.Context) {
	filter := emailModel.ListFilter{
		Type:   ctx.Query("type"),
		Status: ctx.Query("status"),
	}
	res, err := h.useCase.ListEmailTemplates(ctx, filter)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := emailListResponse{
		Templates:        make([]emailTemplateResponse, 0, len(res.Templates)),
		MissingActiveFor: res.MissingActiveFor,
	}
	if out.MissingActiveFor == nil {
		out.MissingActiveFor = []string{}
	}
	for _, t := range res.Templates {
		out.Templates = append(out.Templates, emailToResponse(t))
	}
	response.AbortWithData(ctx, out)
}

// latestEmail godoc
// @Summary  Latest email notification template versions per type
// @Tags     notification-templates
// @Produce  json
// @Success  200  {object}  response.Response{data=[]emailLatestResponse}
// @Router   /notifications/templates/email/latest [get]
func (h *Handler) latestEmail(ctx *gin.Context) {
	versions, err := h.useCase.LatestEmailTemplates(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]emailLatestResponse, 0, len(versions))
	for _, v := range versions {
		entry := emailLatestResponse{NotificationType: v.NotificationType}
		if v.Draft != nil {
			entry.Draft = new(emailToResponse(*v.Draft))
		}
		if v.Published != nil {
			entry.Published = new(emailToResponse(*v.Published))
		}
		if v.Unpublished != nil {
			entry.Unpublished = new(emailToResponse(*v.Unpublished))
		}
		out = append(out, entry)
	}
	response.AbortWithData(ctx, out)
}

// getEmail godoc
// @Summary  Get a single email notification template by ID
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "template ID"
// @Success  200  {object}  response.Response{data=emailTemplateResponse}
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/email/{id} [get]
func (h *Handler) getEmail(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.GetEmailTemplate(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, emailToResponse(t))
}

// createEmail godoc
// @Summary  Create an email notification template
// @Tags     notification-templates
// @Accept   json
// @Produce  json
// @Param    body  body      createEmailRequest        true  "template body"
// @Success  200   {object}  response.Response{data=emailTemplateResponse}
// @Failure  400   {object}  response.Response
// @Router   /notifications/templates/email [post]
func (h *Handler) createEmail(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req createEmailRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.CreateEmailTemplate(ctx, emailModel.CreateTemplateInput{
		NotificationType: req.NotificationType,
		Subject:          req.Subject,
		Preheader:        req.Preheader,
		Body:             req.Body,
		Styling:          req.Styling,
		UpdatedBy:        userID,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, emailToResponse(t))
}

// updateEmail godoc
// @Summary  Update an email notification template
// @Tags     notification-templates
// @Accept   json
// @Produce  json
// @Param    id    path      string               true  "template ID"
// @Param    body  body      updateEmailRequest   true  "updated fields"
// @Success  200   {object}  response.Response{data=emailTemplateResponse}
// @Failure  400   {object}  response.Response
// @Router   /notifications/templates/email/{id} [put]
func (h *Handler) updateEmail(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req updateEmailRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.UpdateEmailTemplate(ctx, emailModel.UpdateTemplateInput{
		ID:        id,
		Subject:   req.Subject,
		Preheader: req.Preheader,
		Body:      req.Body,
		Styling:   req.Styling,
		UpdatedBy: userID,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, emailToResponse(t))
}

// deleteEmail godoc
// @Summary  Delete an email notification template
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "template ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/email/{id} [delete]
func (h *Handler) deleteEmail(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.DeleteEmailTemplate(ctx, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// publishEmail godoc
// @Summary  Publish an email notification template
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "template ID"
// @Success  200  {object}  response.Response{data=emailTemplateResponse}
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/email/{id}/publish [post]
func (h *Handler) publishEmail(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.PublishEmailTemplate(ctx, id, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, emailToResponse(t))
}

// rollbackEmail godoc
// @Summary  Restore a published or unpublished email template as a draft
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "source template ID"
// @Success  200  {object}  response.Response{data=emailTemplateResponse}
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/email/{id}/rollback [post]
func (h *Handler) rollbackEmail(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	t, err := h.useCase.RollbackEmailTemplate(ctx, id, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, emailToResponse(t))
}

// ── email block-preset routes ──

// listPresets godoc
// @Summary  List email block presets
// @Tags     notification-templates
// @Produce  json
// @Success  200  {object}  response.Response{data=[]presetResponse}
// @Router   /notifications/templates/email/block-presets [get]
func (h *Handler) listPresets(ctx *gin.Context) {
	presets, err := h.useCase.ListEmailBlockPresets(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]presetResponse, 0, len(presets))
	for _, p := range presets {
		out = append(out, presetToResponse(p))
	}
	response.AbortWithData(ctx, out)
}

// createPreset godoc
// @Summary  Create an email block preset
// @Tags     notification-templates
// @Accept   json
// @Produce  json
// @Param    body  body      presetRequest  true  "preset body"
// @Success  200   {object}  response.Response{data=presetResponse}
// @Failure  400   {object}  response.Response
// @Router   /notifications/templates/email/block-presets [post]
func (h *Handler) createPreset(ctx *gin.Context) {
	var req presetRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	p, err := h.useCase.CreateEmailBlockPreset(ctx, emailModel.PresetInput{
		Name:        req.Name,
		Description: req.Description,
		Blocks:      req.Blocks,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, presetToResponse(p))
}

// getPreset godoc
// @Summary  Get a single email block preset by ID
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "preset ID"
// @Success  200  {object}  response.Response{data=presetResponse}
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/email/block-presets/{id} [get]
func (h *Handler) getPreset(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	p, err := h.useCase.GetEmailBlockPreset(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, presetToResponse(p))
}

// updatePreset godoc
// @Summary  Update an email block preset
// @Tags     notification-templates
// @Accept   json
// @Produce  json
// @Param    id    path      string         true  "preset ID"
// @Param    body  body      presetRequest  true  "updated fields"
// @Success  200   {object}  response.Response{data=presetResponse}
// @Failure  400   {object}  response.Response
// @Router   /notifications/templates/email/block-presets/{id} [put]
func (h *Handler) updatePreset(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req presetRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	p, err := h.useCase.UpdateEmailBlockPreset(ctx, id, emailModel.PresetInput{
		Name:        req.Name,
		Description: req.Description,
		Blocks:      req.Blocks,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, presetToResponse(p))
}

// deletePreset godoc
// @Summary  Delete an email block preset
// @Tags     notification-templates
// @Produce  json
// @Param    id   path      string  true  "preset ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /notifications/templates/email/block-presets/{id} [delete]
func (h *Handler) deletePreset(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.DeleteEmailBlockPreset(ctx, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
