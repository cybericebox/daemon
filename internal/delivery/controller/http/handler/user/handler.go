package user

import (
	"context"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	Handler struct {
		useCase IUseCase
	}

	IUseCase interface {
		GetUsers(ctx context.Context, search string, page, pageSize int) ([]*userModel.UserInfo, error)
		InviteUsers(ctx context.Context, data userModel.InviteUsers) error
		UpdateUserRole(ctx context.Context, user userModel.User) error
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
	page, pageSize, err := tools.GetPaginationParams(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	users, err := h.useCase.GetUsers(ctx, search, page, pageSize)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}

	response.AbortWithData(ctx, users)
}

func (h *Handler) InviteUsers(ctx *gin.Context) {
	var inp userModel.InviteUsers

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

	var inp userModel.User

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
