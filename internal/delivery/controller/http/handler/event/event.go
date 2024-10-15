package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type ISingleEventUseCase interface {
	GetEvent(ctx context.Context, eventID uuid.UUID) (*model.Event, error)
	GetEventInfo(ctx context.Context, eventID uuid.UUID) (*model.EventInfo, error)
	GetEventBannerDownloadLink(ctx context.Context, eventID uuid.UUID) (string, error)
	UpdateEvent(ctx context.Context, event model.Event) error
	DeleteEvent(ctx context.Context, eventID uuid.UUID) error

	GetSelfJoinEventStatus(ctx context.Context, eventID uuid.UUID) (int32, error)
	JoinEvent(ctx context.Context, eventID uuid.UUID) error
}

func (h *Handler) initSingleEventAPIHandler(router *gin.RouterGroup) {
	router.GET("", protection.RequireProtection(), h.getEvent) // get event
	router.GET("info", h.getEventInfo)

	router.PUT("", protection.RequireProtection(), h.updateEvent)    // update event
	router.DELETE("", protection.RequireProtection(), h.deleteEvent) // delete event

	joinEventAPI := router.Group("join")
	{
		joinEventAPI.GET("info", protection.RequireProtection(), h.getJoinEventStatus)
		joinEventAPI.GET("", protection.RequireProtection(true), h.joinEvent) // by clicking link
	}

	downloadEventAPI := router.Group("download")
	{
		downloadEventAPI.GET("banner", h.downloadBanner)
	}

	h.initChallengeAPIHandler(router)
	h.initTeamAPIHandler(router)
	h.initScoreAPIHandler(router)
	h.initParticipantAPIHandler(router)
}

func (h *Handler) getEvent(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	event, err := h.useCase.GetEvent(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, event)
}

func (h *Handler) getEventInfo(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	event, err := h.useCase.GetEventInfo(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, event)
}

func (h *Handler) downloadBanner(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	link, err := h.useCase.GetEventBannerDownloadLink(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, link)
}

func (h *Handler) updateEvent(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	var inp model.Event

	if err = ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	inp.ID = eventID

	if err = h.useCase.UpdateEvent(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) deleteEvent(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	if err = h.useCase.DeleteEvent(ctx, eventID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) getJoinEventStatus(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	status, err := h.useCase.GetSelfJoinEventStatus(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, gin.H{"Status": status})
}

func (h *Handler) joinEvent(ctx *gin.Context) {

	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	if err = h.useCase.JoinEvent(ctx, eventID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	if ctx.Query("noRedirect") == "" {
		response.TemporaryRedirect(ctx, "/")
		return
	}

	response.AbortWithSuccess(ctx)
}
