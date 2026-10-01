package platformSettings

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformSettingsUseCase "github.com/cybericebox/daemon/internal/useCase/platformSettings"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	// IUseCase is this handler's narrow port into the useCase layer.
	IUseCase interface {
		GetPlatformSetting(ctx context.Context, key string) (*platformSettingsModel.PlatformSetting, error)
		GetPlatformSettingValue(ctx context.Context, key string) (json.RawMessage, error)
		ListPlatformSettings(ctx context.Context) ([]platformSettingsModel.PlatformSetting, error)
		UpsertPlatformSetting(
			ctx context.Context,
			in platformSettingsUseCase.UpsertInput,
		) (*platformSettingsModel.PlatformSetting, error)
	}

	// IProtection is the subset of the protection middleware this handler needs.
	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}
)

// upsertSettingRequest is the PUT /settings/:key body (key comes from the path).
type upsertSettingRequest struct {
	Value              json.RawMessage `json:"Value"              swaggertype:"object"`
	RequiredPermission string          `json:"RequiredPermission"`
}

// settingResponse is the API representation of a setting. It mirrors the domain
// model's JSON but lives in the HTTP layer, decoupling the wire contract and
// giving swagger a concrete type for the response envelope's `data` field.
type settingResponse struct {
	ID                 uuid.UUID       `json:"ID"`
	Key                string          `json:"Key"`
	Value              json.RawMessage `json:"Value"      swaggertype:"object"`
	RequiredPermission string          `json:"RequiredPermission"`
	CreatedAt          time.Time       `json:"CreatedAt"`
	UpdatedAt          time.Time       `json:"UpdatedAt"`
}

func toResponse(s *platformSettingsModel.PlatformSetting) settingResponse {
	return settingResponse{
		ID:                 s.ID,
		Key:                s.Key,
		Value:              s.Value,
		RequiredPermission: s.RequiredPermission,
		CreatedAt:          s.CreatedAt,
		UpdatedAt:          s.UpdatedAt,
	}
}

func toResponseList(items []platformSettingsModel.PlatformSetting) []settingResponse {
	out := make([]settingResponse, 0, len(items))
	for i := range items {
		out = append(out, toResponse(&items[i]))
	}
	return out
}

func NewSettingAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

// Init hangs this package's routes on the shared API router group. Each handler
// package owns its own route wiring in this file (no central route table).
func (h *Handler) Init(router *gin.RouterGroup) {
	settingsAPI := router.Group("settings")
	{
		settingsAPI.GET("", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.listSettings)
		settingsAPI.GET(":key", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.getSetting)
		settingsAPI.PUT(":key", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.upsertSetting)
		settingsAPI.GET(":key/value", h.prot.RequirePermission(rbac.PermPlatformSettingsReadValue), h.getSetting)
	}
}

// ListSettings godoc
// @Summary  List settings readable by the caller
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=[]settingResponse}
// @Router   /settings [get]
func (h *Handler) listSettings(ctx *gin.Context) {
	items, err := h.useCase.ListPlatformSettings(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponseList(items))
}

// GetSetting godoc
// @Summary  Get a setting by key
// @Tags     settings
// @Produce  json
// @Param    key  path      string  true  "setting key"
// @Success  200  {object}  response.Response{data=settingResponse}
// @Failure  404  {object}  response.Response
// @Router   /settings/{key} [get]
func (h *Handler) getSetting(ctx *gin.Context) {
	s, err := h.useCase.GetPlatformSetting(ctx, ctx.Param("key"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(s))
}

// GetSetting godoc
// @Summary  Get a setting value by key
// @Tags     settings
// @Produce  json
// @Param    key  path      string  true  "setting key"
// @Success  200  {object}  response.Response{data=object}
// @Failure  404  {object}  response.Response
// @Router   /settings/{key}/value [get]
func (h *Handler) getSettingValue(ctx *gin.Context) {
	s, err := h.useCase.GetPlatformSettingValue(ctx, ctx.Param("key"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, s)
}

// UpsertPlatformSetting godoc
// @Summary  Create or update a setting by key (transactional)
// @Tags     settings
// @Accept   json
// @Produce  json
// @Param    key   path      string                true  "setting key"
// @Param    body  body      upsertSettingRequest  true  "setting value"
// @Success  200   {object}  response.Response{data=settingResponse}
// @Failure  400   {object}  response.Response
// @Router   /settings/{key} [put]
func (h *Handler) upsertSetting(ctx *gin.Context) {
	var req upsertSettingRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}

	s, err := h.useCase.UpsertPlatformSetting(
		ctx, platformSettingsUseCase.UpsertInput{
			Key:                ctx.Param("key"),
			Value:              req.Value,
			RequiredPermission: req.RequiredPermission,
		},
	)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(s))
}
