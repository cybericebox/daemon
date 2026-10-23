package messaging

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type (
	// templateRequest is a broadcast template as authored: a name and the
	// message fields of a broadcast (no audience).
	templateRequest struct {
		contentDTO
		Name string `json:"Name"`
	}

	templateResponse struct {
		ID            uuid.UUID  `json:"ID"`
		ScopeEventID  *uuid.UUID `json:"ScopeEventID"`
		Name          string     `json:"Name"`
		Channels      []string   `json:"Channels"`
		Subject       string     `json:"Subject"`
		Preheader     string     `json:"Preheader"`
		EmailBody     any        `json:"EmailBody"`
		EmailStyling  any        `json:"EmailStyling"`
		InAppTitle    string     `json:"InAppTitle"`
		InAppBody     string     `json:"InAppBody"`
		InAppLink     string     `json:"InAppLink"`
		UpdatedBy     *uuid.UUID `json:"UpdatedBy"`
		UpdatedByName string     `json:"UpdatedByName"`
		CreatedAt     time.Time  `json:"CreatedAt"`
		UpdatedAt     time.Time  `json:"UpdatedAt"`
	}
)

func toTemplateResponse(t broadcastModel.Template) templateResponse {
	channels := make([]string, 0, len(t.Content.Channels))
	for _, ch := range t.Content.Channels {
		channels = append(channels, string(ch))
	}
	return templateResponse{
		ID: t.ID, ScopeEventID: t.ScopeEventID, Name: t.Name, Channels: channels,
		Subject: t.Content.Subject, Preheader: t.Content.Preheader,
		EmailBody: rawOrNil(t.Content.EmailBody), EmailStyling: rawOrNil(t.Content.EmailStyling),
		InAppTitle: t.Content.InAppTitle, InAppBody: t.Content.InAppBody, InAppLink: t.Content.InAppLink,
		UpdatedBy: t.UpdatedBy, UpdatedByName: t.UpdatedByName, CreatedAt: t.CreatedAt, UpdatedAt: t.UpdatedAt,
	}
}

func (r templateRequest) toModel() (broadcastModel.TemplateInput, error) {
	content, err := r.contentDTO.toModel()
	if err != nil {
		return broadcastModel.TemplateInput{}, err
	}
	return broadcastModel.TemplateInput{Name: r.Name, Content: content}, nil
}

// listTemplates godoc
// @Summary  Saved broadcast templates of the scope, recently edited first
// @Tags     notification-broadcasts
// @Produce  json
// @Success  200  {object}  response.Response{data=[]templateResponse}
// @Router   /notifications/broadcast-templates [get]
// @Router   /events/{id}/manage/broadcast-templates [get]
func (h *Handler) listTemplates(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
			return
		}
		items, err := h.useCase.ListBroadcastTemplates(ctx.Request.Context(), eventID)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		out := make([]templateResponse, 0, len(items))
		for _, t := range items {
			out = append(out, toTemplateResponse(t))
		}
		response.AbortWithData(ctx, out)
	}
}

// getTemplate godoc
// @Summary  Get a broadcast template
// @Tags     notification-broadcasts
// @Produce  json
// @Param    templateID  path  string  true  "template ID"
// @Success  200  {object}  response.Response{data=templateResponse}
// @Failure  404  {object}  response.Response
// @Router   /notifications/broadcast-templates/{templateID} [get]
// @Router   /events/{id}/manage/broadcast-templates/{templateID} [get]
func (h *Handler) getTemplate(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, eventID, ok := templateRef(ctx, scope)
		if !ok {
			return
		}
		t, err := h.useCase.GetBroadcastTemplate(ctx.Request.Context(), eventID, id)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, toTemplateResponse(t))
	}
}

// createTemplate godoc
// @Summary  Save a broadcast template
// @Tags     notification-broadcasts
// @Accept   json
// @Produce  json
// @Param    body  body  templateRequest  true  "name and message"
// @Success  200  {object}  response.Response{data=templateResponse}
// @Failure  400  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /notifications/broadcast-templates [post]
// @Router   /events/{id}/manage/broadcast-templates [post]
func (h *Handler) createTemplate(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		eventID, ok := scope(ctx)
		if !ok {
			return
		}
		actor, in, ok := templateInput(ctx)
		if !ok {
			return
		}
		t, err := h.useCase.CreateBroadcastTemplate(ctx.Request.Context(), eventID, actor, in)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, toTemplateResponse(t))
	}
}

// updateTemplate godoc
// @Summary  Update a broadcast template
// @Tags     notification-broadcasts
// @Accept   json
// @Produce  json
// @Param    templateID  path  string           true  "template ID"
// @Param    body        body  templateRequest  true  "name and message"
// @Success  200  {object}  response.Response{data=templateResponse}
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /notifications/broadcast-templates/{templateID} [put]
// @Router   /events/{id}/manage/broadcast-templates/{templateID} [put]
func (h *Handler) updateTemplate(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, eventID, ok := templateRef(ctx, scope)
		if !ok {
			return
		}
		actor, in, ok := templateInput(ctx)
		if !ok {
			return
		}
		t, err := h.useCase.UpdateBroadcastTemplate(ctx.Request.Context(), eventID, id, actor, in)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, toTemplateResponse(t))
	}
}

// deleteTemplate godoc
// @Summary  Delete a broadcast template
// @Tags     notification-broadcasts
// @Produce  json
// @Param    templateID  path  string  true  "template ID"
// @Success  200  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Router   /notifications/broadcast-templates/{templateID} [delete]
// @Router   /events/{id}/manage/broadcast-templates/{templateID} [delete]
func (h *Handler) deleteTemplate(scope scopeFn) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, eventID, ok := templateRef(ctx, scope)
		if !ok {
			return
		}
		if err := h.useCase.DeleteBroadcastTemplate(ctx.Request.Context(), eventID, id); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		response.AbortWithData(ctx, struct{}{})
	}
}

func templateRef(ctx *gin.Context, scope scopeFn) (uuid.UUID, *uuid.UUID, bool) {
	eventID, ok := scope(ctx)
	if !ok {
		return uuid.Nil, nil, false
	}
	id, err := uuid.FromString(ctx.Param("templateID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, nil, false
	}
	return id, eventID, true
}

func templateInput(ctx *gin.Context) (uuid.UUID, broadcastModel.TemplateInput, bool) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return uuid.Nil, broadcastModel.TemplateInput{}, false
	}
	var req templateRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, broadcastModel.TemplateInput{}, false
	}
	in, err := req.toModel()
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, broadcastModel.TemplateInput{}, false
	}
	return claims.UserID, in, true
}
