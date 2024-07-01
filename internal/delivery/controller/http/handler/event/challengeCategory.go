package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type IChallengeCategoryUseCase interface {
	GetEventCategories(ctx context.Context, eventID uuid.UUID) ([]*model.ChallengeCategory, error)
	CreateEventCategory(ctx context.Context, category *model.ChallengeCategory) error
	UpdateEventCategory(ctx context.Context, category *model.ChallengeCategory) error
	DeleteEventCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error

	UpdateEventCategoriesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error
}

func (h *Handler) initChallengeCategoryAPIHandler(router *gin.RouterGroup) {
	categoryAPI := router.Group("categories")
	{
		categoryAPI.GET("", h.getCategories)
		categoryAPI.POST("", h.createCategory)
		categoryAPI.PUT(":categoryID", h.updateCategory)
		categoryAPI.DELETE(":categoryID", h.deleteCategory)

		categoryAPI.PATCH("order", h.updateCategoriesOrder)
	}
}

func (h *Handler) getCategories(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))

	categories, err := h.useCase.GetEventCategories(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithContent(ctx, categories)
}

func (h *Handler) createCategory(ctx *gin.Context) {
	var inp model.ChallengeCategory
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	inp.EventID = uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))

	if err := h.useCase.CreateEventCategory(ctx, &inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithOK(ctx, "Category created successfully")
}

func (h *Handler) updateCategory(ctx *gin.Context) {
	var inp model.ChallengeCategory
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	inp.ID = uuid.FromStringOrNil(ctx.Param("categoryID"))
	inp.EventID = uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))

	if err := h.useCase.UpdateEventCategory(ctx, &inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithOK(ctx, "Category updated successfully")
}

func (h *Handler) deleteCategory(ctx *gin.Context) {
	eventID := uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))
	categoryID := uuid.FromStringOrNil(ctx.Param("categoryID"))

	if err := h.useCase.DeleteEventCategory(ctx, eventID, categoryID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithOK(ctx, "Category deleted successfully")
}

func (h *Handler) updateCategoriesOrder(ctx *gin.Context) {
	var inp []model.Order
	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	eventID := uuid.FromStringOrNil(ctx.GetString(tools.EventIDCtxKey))

	if err := h.useCase.UpdateEventCategoriesOrder(ctx, eventID, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithOK(ctx, "Categories order updated successfully")
}
