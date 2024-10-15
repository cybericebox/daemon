package user

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
		GetUsers(ctx context.Context, search string) ([]*model.UserInfo, error)
		InviteUsers(ctx context.Context, data model.InviteUsers) error
		UpdateUserRole(ctx context.Context, user model.User) error
		DeleteUser(ctx context.Context, userID uuid.UUID) error
	}
)

func NewUserAPIHandler(useCase IUseCase) *Handler {
	return &Handler{useCase: useCase}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	userAPI := router.Group("users", protection.RequireProtection())
	{
		userAPI.GET("", h.GetUsers) // all routes are protected
		userAPI.POST("invite", h.InviteUsers)
		userAPI.PATCH(":userID", h.UpdateUserRole)
		userAPI.DELETE(":userID", h.DeleteUser)
	}
}

func (h *Handler) GetUsers(ctx *gin.Context) {
	search := ctx.Query("search")

	users, err := h.useCase.GetUsers(ctx, search)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, users)
}

func (h *Handler) InviteUsers(ctx *gin.Context) {
	var inp model.InviteUsers

	if err := ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err := h.useCase.InviteUsers(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) UpdateUserRole(ctx *gin.Context) {
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	var inp model.User

	if err = ctx.BindJSON(&inp); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	inp.ID = userID

	if err = h.useCase.UpdateUserRole(ctx, inp); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}

func (h *Handler) DeleteUser(ctx *gin.Context) {
	userID, err := uuid.FromString(ctx.Param("userID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	if err = h.useCase.DeleteUser(ctx, userID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithSuccess(ctx)
}
