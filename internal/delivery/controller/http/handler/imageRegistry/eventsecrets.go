package imageRegistry

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

// eventSecretRequest is the organizer registry secret form. Token is
// write-only: required on create, empty on update keeps the stored one.
type eventSecretRequest struct {
	Host             string `json:"Host"`
	RepositoryPrefix string `json:"RepositoryPrefix"`
	Username         string `json:"Username"`
	Token            string `json:"Token"`
}

// eventSecretResponse is a secret as «Панель заходу» sees it: never the token.
type eventSecretResponse struct {
	ID               string    `json:"ID"`
	Host             string    `json:"Host"`
	RepositoryPrefix string    `json:"RepositoryPrefix"`
	Username         string    `json:"Username"`
	UpdatedAt        time.Time `json:"UpdatedAt"`
}

func toSecretResponse(v imageRegistryUseCase.EventSecretView) eventSecretResponse {
	return eventSecretResponse{ID: v.ID, Host: v.Host, RepositoryPrefix: v.RepositoryPrefix, Username: v.Username, UpdatedAt: v.UpdatedAt}
}

func (r eventSecretRequest) input() imageRegistryUseCase.EventSecretInput {
	return imageRegistryUseCase.EventSecretInput{Host: r.Host, RepositoryPrefix: r.RepositoryPrefix, Username: r.Username, Token: r.Token}
}

func (h *Handler) initEventSecrets(router *gin.RouterGroup) {
	manage := router.Group("events/:id/manage/registry-secrets", h.prot.RequirePermission(rbac.PermSelf))
	manage.GET("", h.requireRead, h.listEventSecrets)
	manage.POST("", h.requireManage, h.createEventSecret)
	manage.PUT(":secretId", h.requireManage, h.updateEventSecret)
	manage.DELETE(":secretId", h.requireManage, h.deleteEventSecret)
}

// listEventSecrets godoc
// @Summary  List the event's registry secrets (tokens are never returned)
// @Tags     event-registry
// @Produce  json
// @Param    id  path  string  true  "Event ID"
// @Success  200  {object}  response.Response{data=[]eventSecretResponse}
// @Router   /events/{id}/manage/registry-secrets [get]
func (h *Handler) listEventSecrets(ctx *gin.Context) {
	eventID, ok := parseUUID(ctx, "id")
	if !ok {
		return
	}
	views, err := h.useCase.ListEventRegistrySecrets(ctx.Request.Context(), eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]eventSecretResponse, 0, len(views))
	for _, v := range views {
		out = append(out, toSecretResponse(v))
	}
	response.AbortWithData(ctx, out)
}

// createEventSecret godoc
// @Summary  Add a registry secret to the event; used only for this event's labs
// @Tags     event-registry
// @Accept   json
// @Produce  json
// @Param    id    path  string              true  "Event ID"
// @Param    body  body  eventSecretRequest  true  "host, optional repository prefix, username, token"
// @Success  200  {object}  response.Response{data=eventSecretResponse}
// @Failure  400  {object}  response.Response
// @Failure  409  {object}  response.Response  "same host and prefix already saved, or secret storage not configured"
// @Router   /events/{id}/manage/registry-secrets [post]
func (h *Handler) createEventSecret(ctx *gin.Context) {
	eventID, ok := parseUUID(ctx, "id")
	if !ok {
		return
	}
	var req eventSecretRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.CreateEventRegistrySecret(ctx.Request.Context(), eventID, req.input())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toSecretResponse(v))
}

// updateEventSecret godoc
// @Summary  Change a registry secret of the event; an empty token keeps the stored one
// @Tags     event-registry
// @Accept   json
// @Produce  json
// @Param    id        path  string              true  "Event ID"
// @Param    secretId  path  string              true  "Secret ID"
// @Param    body      body  eventSecretRequest  true  "host, optional repository prefix, username, optional new token"
// @Success  200  {object}  response.Response{data=eventSecretResponse}
// @Failure  400  {object}  response.Response
// @Failure  404  {object}  response.Response  "no such secret in this event"
// @Failure  409  {object}  response.Response  "same host and prefix already saved, or secret storage not configured"
// @Router   /events/{id}/manage/registry-secrets/{secretId} [put]
func (h *Handler) updateEventSecret(ctx *gin.Context) {
	eventID, ok := parseUUID(ctx, "id")
	if !ok {
		return
	}
	secretID, ok := parseUUID(ctx, "secretId")
	if !ok {
		return
	}
	var req eventSecretRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.UpdateEventRegistrySecret(ctx.Request.Context(), eventID, secretID, req.input())
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toSecretResponse(v))
}

// deleteEventSecret godoc
// @Summary  Remove a registry secret from the event
// @Tags     event-registry
// @Produce  json
// @Param    id        path  string  true  "Event ID"
// @Param    secretId  path  string  true  "Secret ID"
// @Success  200  {object}  response.Response
// @Failure  404  {object}  response.Response  "no such secret in this event"
// @Router   /events/{id}/manage/registry-secrets/{secretId} [delete]
func (h *Handler) deleteEventSecret(ctx *gin.Context) {
	eventID, ok := parseUUID(ctx, "id")
	if !ok {
		return
	}
	secretID, ok := parseUUID(ctx, "secretId")
	if !ok {
		return
	}
	if err := h.useCase.DeleteEventRegistrySecret(ctx.Request.Context(), eventID, secretID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}

func (h *Handler) requireManage(ctx *gin.Context) {
	h.requireEvent(ctx, func(c context.Context, eventID, userID uuid.UUID) error {
		if err := h.useCase.RequireManageEvent(c, eventID, userID); err != nil {
			return err
		}
		return middleware.CheckEventWritable(ctx, h.useCase, eventID)
	})
}

func (h *Handler) requireRead(ctx *gin.Context) {
	h.requireEvent(ctx, h.useCase.RequireReadEvent)
}

func (h *Handler) requireEvent(ctx *gin.Context, check func(context.Context, uuid.UUID, uuid.UUID) error) {
	eventID, ok := parseUUID(ctx, "id")
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	if err := check(ctx.Request.Context(), eventID, claims.UserID); err != nil {
		response.AbortWithError(ctx, err)
	}
}

func parseUUID(ctx *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param(param))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}
