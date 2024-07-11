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

type (
	Handler struct {
		useCase IUseCase
	}

	IUseCase interface {
		IChallengeUseCase
		IChallengeCategoryUseCase
		ITeamUseCase
		IParticipantUseCase
		IScoreUseCase
		ISingleEventUseCase

		GetEvents(ctx context.Context) ([]*model.Event, error)
		GetEventsInfo(ctx context.Context) ([]*model.EventInfo, error)
		CreateEvent(ctx context.Context, event model.Event) error

		GetEventIDByTag(ctx context.Context, eventTag string) (uuid.UUID, error)
	}
)

func NewEventAPIHandler(useCase IUseCase) *Handler {
	return &Handler{useCase: useCase}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	eventAPI := router.Group("events")
	{
		eventAPI.GET("", protection.RequireProtection(), h.getEvents)    // get all events
		eventAPI.GET("info", h.getEventsInfo)                            // get all events info only
		eventAPI.POST("", protection.RequireProtection(), h.createEvent) // create event

		singleEventAPI := eventAPI.Group(":eventIDOrTag", h.setEventIDToContext)
		h.initSingleEventAPIHandler(singleEventAPI)

	}
}

func (h *Handler) getEvents(ctx *gin.Context) {
	events, err := h.useCase.GetEvents(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, events)
}

func (h *Handler) getEventsInfo(ctx *gin.Context) {
	events, err := h.useCase.GetEventsInfo(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, events)
}

func (h *Handler) createEvent(ctx *gin.Context) {
	var inp model.Event
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.CreateEvent(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) setEventIDToContext(ctx *gin.Context) {
	eventIDOrTag := ctx.Param("eventIDOrTag")

	eventID, err := uuid.FromString(eventIDOrTag)
	if err != nil {
		if eventIDOrTag == "self" {
			eventIDOrTag = ctx.GetString(tools.SubdomainCtxKey)
		}

		eventID, err = h.useCase.GetEventIDByTag(ctx, eventIDOrTag)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
	}

	ctx.Set(tools.EventIDCtxKey, eventID.String())
}
