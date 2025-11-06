package event

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
)

type IScoreUseCase interface {
	GetScore(ctx context.Context, eventID uuid.UUID) (*eventModel.EventScore, error)
	ProtectScore(ctx context.Context, eventID uuid.UUID) (bool, error)
}

func (h *Handler) initScoreAPIHandler(router *gin.RouterGroup) {
	scoreAPI := router.Group("score", protection.DynamicallyRequireProtection(h.scoreNeedProtection))
	{
		scoreAPI.GET("", h.getScore)
	}
}

func (h *Handler) getScore(ctx *gin.Context) {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	score, err := h.useCase.GetScore(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, score)
}

func (h *Handler) scoreNeedProtection(ctx *gin.Context) bool {
	eventID, err := uuid.FromString(ctx.GetString(tools.EventIDCtxKey))
	if err != nil {
		response.AbortWithError(ctx, err)
		return true
	}

	needProtection, err := h.useCase.ProtectScore(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return true
	}

	return needProtection
}
