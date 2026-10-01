package settings

import (
	"context"
	"encoding/json"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	settingsModel "github.com/cybericebox/daemon/internal/model/notification/settings"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	// IProtection is the subset of the protection middleware this handler needs.
	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		ListGlobalSettings(ctx context.Context) ([]settingsModel.GlobalSetting, error)
		UpsertGlobalSetting(
			ctx context.Context,
			in settingsModel.UpsertGlobalInput,
		) (settingsModel.GlobalSetting, error)
		ListUserSettings(ctx context.Context, userID uuid.UUID) ([]settingsModel.UserSetting, error)
		UpsertUserSetting(ctx context.Context, in settingsModel.UpsertUserInput) error
		ListSignalDefaults(ctx context.Context) ([]settingsModel.SignalDefault, error)
		UpsertSignalDefault(ctx context.Context, in settingsModel.SignalDefault) (settingsModel.SignalDefault, error)
	}
)

type globalSettingResponse struct {
	NotificationType string `json:"NotificationType"`
	Channel          string `json:"Channel"`
	Enabled          bool   `json:"Enabled"`
	UserCanChange    bool   `json:"UserCanChange"`
	UserDefault      bool   `json:"UserDefault"`
}

type userSettingResponse struct {
	NotificationType string `json:"NotificationType"`
	Channel          string `json:"Channel"`
	Enabled          bool   `json:"Enabled"`
}

type upsertGlobalRequest struct {
	NotificationType string `json:"NotificationType"`
	Channel          string `json:"Channel"`
	Enabled          bool   `json:"Enabled"`
	UserCanChange    bool   `json:"UserCanChange"`
	UserDefault      bool   `json:"UserDefault"`
}

type upsertUserRequest struct {
	NotificationType string `json:"NotificationType"`
	Channel          string `json:"Channel"`
	Enabled          bool   `json:"Enabled"`
}

// signalDefaultResponse is the platform default subscription every Event
// inherits for one (Event-scoped signal, channel) pair until it overrides it.
type signalDefaultResponse struct {
	SignalType string          `json:"SignalType"`
	Channel    string          `json:"Channel"`
	Enabled    bool            `json:"Enabled"`
	Audience   json.RawMessage `json:"Audience" swaggertype:"object"`
}

type upsertSignalDefaultRequest struct {
	SignalType string          `json:"SignalType"`
	Channel    string          `json:"Channel"`
	Enabled    bool            `json:"Enabled"`
	Audience   json.RawMessage `json:"Audience" swaggertype:"object"`
}

func signalDefaultToResponse(d settingsModel.SignalDefault) signalDefaultResponse {
	return signalDefaultResponse{SignalType: d.SignalType, Channel: d.Channel, Enabled: d.Enabled, Audience: d.Audience}
}

func globalToResponse(s settingsModel.GlobalSetting) globalSettingResponse {
	return globalSettingResponse{
		NotificationType: s.NotificationType, Channel: s.Channel, Enabled: s.Enabled,
		UserCanChange: s.UserCanChange, UserDefault: s.UserDefault,
	}
}

func userToResponse(s settingsModel.UserSetting) userSettingResponse {
	return userSettingResponse{
		NotificationType: s.NotificationType,
		Channel:          s.Channel,
		Enabled:          s.Enabled,
	}
}

func NewSettingsAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	g := router.Group("settings")
	{
		g.GET("global", h.prot.RequirePermission(rbac.PermNotificationsSettingsRead), h.listGlobal)
		g.PUT(
			"global",
			h.prot.RequirePermission(rbac.PermNotificationsSettingsWrite),
			h.upsertGlobal,
		)
		g.GET("user", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.listUser)
		g.PUT("user", h.prot.RequirePermission(rbac.PermNotificationsSelf), h.upsertUser)
	}
	router.GET("signal-defaults", h.prot.RequirePermission(rbac.PermNotificationsSettingsRead), h.listSignalDefaults)
	router.PUT("signal-defaults", h.prot.RequirePermission(rbac.PermNotificationsSettingsWrite), h.upsertSignalDefault)
}

// listGlobal godoc
// @Summary  List global notification settings
// @Tags     notification-settings
// @Produce  json
// @Success  200  {object}  response.Response{data=[]globalSettingResponse}
// @Router   /notifications/settings/global [get]
func (h *Handler) listGlobal(ctx *gin.Context) {
	items, err := h.useCase.ListGlobalSettings(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]globalSettingResponse, 0, len(items))
	for _, s := range items {
		out = append(out, globalToResponse(s))
	}
	response.AbortWithData(ctx, out)
}

// upsertGlobal godoc
// @Summary  Create or update a global notification setting
// @Tags     notification-settings
// @Accept   json
// @Produce  json
// @Param    body  body      upsertGlobalRequest  true  "setting body"
// @Success  200   {object}  response.Response{data=globalSettingResponse}
// @Failure  400   {object}  response.Response
// @Router   /notifications/settings/global [put]
func (h *Handler) upsertGlobal(ctx *gin.Context) {
	var req upsertGlobalRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	s, err := h.useCase.UpsertGlobalSetting(ctx, settingsModel.UpsertGlobalInput{
		NotificationType: req.NotificationType, Channel: req.Channel, Enabled: req.Enabled,
		UserCanChange: req.UserCanChange, UserDefault: req.UserDefault,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, globalToResponse(s))
}

// listUser godoc
// @Summary  List notification settings for the current user
// @Tags     notification-settings
// @Produce  json
// @Success  200  {object}  response.Response{data=[]userSettingResponse}
// @Router   /notifications/settings/user [get]
func (h *Handler) listUser(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	items, err := h.useCase.ListUserSettings(ctx, userID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]userSettingResponse, 0, len(items))
	for _, s := range items {
		out = append(out, userToResponse(s))
	}
	response.AbortWithData(ctx, out)
}

// upsertUser godoc
// @Summary  Create or update a notification setting for the current user
// @Tags     notification-settings
// @Accept   json
// @Produce  json
// @Param    body  body      upsertUserRequest  true  "setting body"
// @Success  200   {object}  response.Response
// @Failure  400   {object}  response.Response
// @Router   /notifications/settings/user [put]
func (h *Handler) upsertUser(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	userID := claims.UserID
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req upsertUserRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.UpsertUserSetting(ctx, settingsModel.UpsertUserInput{
		UserID:           userID,
		NotificationType: req.NotificationType,
		Channel:          req.Channel,
		Enabled:          req.Enabled,
	}); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// listSignalDefaults godoc
// @Summary  List platform signal notification defaults
// @Description The platform default subscription of every Event-scoped (signal, channel) pair; Events inherit it until they override the pair.
// @Tags     notification-settings
// @Produce  json
// @Success  200  {object}  response.Response{data=[]signalDefaultResponse}
// @Router   /notifications/signal-defaults [get]
func (h *Handler) listSignalDefaults(ctx *gin.Context) {
	items, err := h.useCase.ListSignalDefaults(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]signalDefaultResponse, 0, len(items))
	for _, d := range items {
		out = append(out, signalDefaultToResponse(d))
	}
	response.AbortWithData(ctx, out)
}

// upsertSignalDefault godoc
// @Summary  Create or update a platform signal notification default
// @Tags     notification-settings
// @Accept   json
// @Produce  json
// @Param    body  body      upsertSignalDefaultRequest  true  "signal default body"
// @Success  200   {object}  response.Response{data=signalDefaultResponse}
// @Failure  400   {object}  response.Response
// @Router   /notifications/signal-defaults [put]
func (h *Handler) upsertSignalDefault(ctx *gin.Context) {
	var req upsertSignalDefaultRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	saved, err := h.useCase.UpsertSignalDefault(ctx, settingsModel.SignalDefault{
		SignalType: req.SignalType, Channel: req.Channel, Enabled: req.Enabled, Audience: req.Audience,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, signalDefaultToResponse(saved))
}
