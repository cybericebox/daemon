package exercise

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type (
	Handler struct {
		useCase IUseCase
	}

	IUseCase interface {
		IExerciseCategoryUseCase

		GetExercises(ctx context.Context) ([]*model.Exercise, error)
		GetExercise(ctx context.Context, id uuid.UUID) (*model.Exercise, error)

		CreateExercise(ctx context.Context, exercise model.Exercise) error
		UpdateExercise(ctx context.Context, exercise model.Exercise) error

		DeleteExercise(ctx context.Context, id uuid.UUID) error
	}
)

func NewExerciseAPIHandler(useCase IUseCase) *Handler {
	return &Handler{useCase: useCase}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	exerciseAPI := router.Group("exercises", protection.RequireProtection())
	{
		exerciseAPI.GET("", h.getExercises)
		exerciseAPI.GET(":exerciseID", h.getExercise)
		exerciseAPI.POST("", h.createExercise)
		exerciseAPI.PUT(":exerciseID", h.updateExercise)
		exerciseAPI.DELETE(":exerciseID", h.deleteExercise)

		h.initCategoryExerciseAPIHandler(exerciseAPI)
	}
}

func (h *Handler) getExercises(ctx *gin.Context) {
	exercises, err := h.useCase.GetExercises(ctx)

	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, exercises)
}

func (h *Handler) getExercise(ctx *gin.Context) {
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	exercise, err := h.useCase.GetExercise(ctx, exerciseID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, exercise)
}

func (h *Handler) createExercise(ctx *gin.Context) {
	var inp model.Exercise

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.CreateExercise(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) updateExercise(ctx *gin.Context) {
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	var inp model.Exercise

	if err = ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	// Set the ID of the exercise to the ID from the URL
	inp.ID = exerciseID

	if err = h.useCase.UpdateExercise(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) deleteExercise(ctx *gin.Context) {
	exerciseID, err := uuid.FromString(ctx.Param("exerciseID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err = h.useCase.DeleteExercise(ctx, exerciseID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}
