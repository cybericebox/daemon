package exercise

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type IExerciseCategoryUseCase interface {
	GetExerciseCategories(ctx context.Context) ([]*model.ExerciseCategory, error)
	CreateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error
	UpdateExerciseCategory(ctx context.Context, category model.ExerciseCategory) error
	DeleteExerciseCategory(ctx context.Context, categoryID uuid.UUID) error
}

func (h *Handler) initCategoryExerciseAPIHandler(router *gin.RouterGroup) {
	categoryAPI := router.Group("categories")
	{
		categoryAPI.GET("", h.getCategories)
		categoryAPI.POST("", h.createCategory)
		categoryAPI.PUT(":categoryID", h.updateCategory)
		categoryAPI.DELETE(":categoryID", h.deleteCategory)
	}
}

func (h *Handler) getCategories(ctx *gin.Context) {
	categories, err := h.useCase.GetExerciseCategories(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithContent(ctx, categories)
}

func (h *Handler) createCategory(ctx *gin.Context) {
	var inp model.ExerciseCategory

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.CreateExerciseCategory(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) updateCategory(ctx *gin.Context) {
	categoryID, err := uuid.FromString(ctx.Param("categoryID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	var inp model.ExerciseCategory

	if err = ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	inp.ID = categoryID

	if err = h.useCase.UpdateExerciseCategory(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) deleteCategory(ctx *gin.Context) {
	categoryID, err := uuid.FromString(ctx.Param("categoryID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err = h.useCase.DeleteExerciseCategory(ctx, categoryID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}
