package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tool"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type IScoreUseCase interface {
	GetScore(ctx context.Context, eventID uuid.UUID) (*model.EventScore, error)
	ProtectScore(ctx context.Context, eventID uuid.UUID) (bool, error)
}

func (h *Handler) initScoreAPIHandler(router *gin.RouterGroup) {
	scoreAPI := router.Group("score", protection.DynamicallyRequireProtection(h.scoreNeedProtection))
	{
		scoreAPI.GET("", h.getScore)
	}
}

func (h *Handler) getScore(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tool.EventIDCtxKey))
	score, err := h.useCase.GetScore(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithContent(ctx, score)
}

func (h *Handler) scoreNeedProtection(ctx *gin.Context) bool {
	eventID := uuid.FromStringOrNil(ctx.GetString(tool.EventIDCtxKey))
	needProtection, err := h.useCase.ProtectScore(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return true
	}
	return needProtection
}
