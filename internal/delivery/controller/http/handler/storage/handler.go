package storage

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/protection"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"net/http"
)

type (
	Handler struct {
		useCase IUseCase
	}

	IUseCase interface {
		GetUploadFileLink(ctx context.Context, storageType string, fileID uuid.UUID) (string, error)
		GetDownloadFileLink(ctx context.Context, storageType string, fileID uuid.UUID) (string, error)
	}
)

func NewStorageAPIHandler(useCase IUseCase) *Handler {
	return &Handler{useCase: useCase}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	storageAPI := router.Group("storage/:storageType", protection.RequireProtection)
	{
		storageAPI.GET(":fileID", h.getFileLink)
		storageAPI.GET("download/:fileID", h.redirectToDownloadFile)
		storageAPI.PUT("upload/:fileID", h.redirectToUploadURL)
	}
}

func (h *Handler) getFileLink(ctx *gin.Context) {
	fileID := uuid.FromStringOrNil(ctx.Param("fileID"))
	storageType := ctx.Param("storageType")

	url, err := h.useCase.GetDownloadFileLink(ctx, storageType, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithContent(ctx, url)

}

func (h *Handler) redirectToUploadURL(ctx *gin.Context) {
	fileID := uuid.FromStringOrNil(ctx.Param("fileID"))
	storageType := ctx.Param("storageType")

	url, err := h.useCase.GetUploadFileLink(ctx, storageType, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.Redirect(ctx, http.StatusTemporaryRedirect, url)
}

func (h *Handler) redirectToDownloadFile(ctx *gin.Context) {
	fileID := uuid.FromStringOrNil(ctx.Param("fileID"))
	storageType := ctx.Param("storageType")

	url, err := h.useCase.GetDownloadFileLink(ctx, storageType, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.Redirect(ctx, http.StatusTemporaryRedirect, url)
}
