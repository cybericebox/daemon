package event

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type brandChangeRequest struct {
	Action string    `json:"Action"`
	FileID uuid.UUID `json:"FileID"`
}

func (r brandChangeRequest) toInput() eventUseCase.BrandAssetChange {
	return eventUseCase.BrandAssetChange{Action: r.Action, FileID: r.FileID}
}

type saveGeneralRequest struct {
	Name        string             `json:"Name"`
	Description string             `json:"Description"`
	Preview     brandChangeRequest `json:"Preview"`
}

type saveAppearanceRequest struct {
	Brand   string             `json:"Brand"`
	Accent  string             `json:"Accent"`
	Logo    brandChangeRequest `json:"Logo"`
	Favicon brandChangeRequest `json:"Favicon"`
}

func (h *Handler) uploadEventBrandDraft(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	middleware.LimitBody(ctx, (5<<20)+(32<<10))
	header, err := ctx.FormFile("file")
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	file, err := header.Open()
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	defer func() { _ = file.Close() }()
	fileID, err := h.useCase.UploadEventBrandDraft(ctx.Request.Context(), id, claims.UserID, ctx.Param("kind"), file, header.Size)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, struct {
		FileID uuid.UUID `json:"FileID"`
	}{FileID: fileID})
}

func (h *Handler) saveEventGeneral(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req saveGeneralRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.SaveEventGeneral(ctx.Request.Context(), id, claims.UserID, eventUseCase.EventGeneralInput{Name: req.Name, Description: req.Description, Preview: req.Preview.toInput()})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	config, ok := h.configResponse(ctx, id, view.Config)
	if !ok {
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, struct {
		Name   string         `json:"Name"`
		Config configResponse `json:"Config"`
	}{Name: view.Name, Config: config})
}

func (h *Handler) saveEventAppearance(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req saveAppearanceRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.SaveEventAppearance(ctx.Request.Context(), id, claims.UserID, eventUseCase.EventAppearanceInput{Brand: req.Brand, Accent: req.Accent, Logo: req.Logo.toInput(), Favicon: req.Favicon.toInput()})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	config, ok := h.configResponse(ctx, id, view.Config)
	if !ok {
		return
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, struct {
		Config     configResponse `json:"Config"`
		LogoURL    string         `json:"LogoURL"`
		FaviconURL string         `json:"FaviconURL"`
	}{Config: config, LogoURL: view.LogoURL, FaviconURL: view.FaviconURL})
}

func (h *Handler) streamEventFavicon(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithNotFound(ctx)
		return
	}
	reader, contentType, err := h.useCase.StreamEventFavicon(ctx.Request.Context(), id, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = reader.Close() }()
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Disposition", "inline; filename=event-favicon.png")
	ctx.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
	ctx.DataFromReader(http.StatusOK, -1, contentType, reader, nil)
}
