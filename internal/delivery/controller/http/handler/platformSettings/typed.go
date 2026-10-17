package platformSettings

import (
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	platformSettingsModel "github.com/cybericebox/daemon/internal/model/platformSettings"
	"github.com/cybericebox/daemon/internal/model/rbac"
	platformSettingsUseCase "github.com/cybericebox/daemon/internal/useCase/platformSettings"
)

// Typed settings: retention periods, archive limits and test run leases, each edited whole on its own admin tab.
// Durations are seconds, sizes bytes, counts plain integers. The static paths coexist with the generic settings/:key.

// saveTypedRequest is the PUT body: every field of the setting. int64 values make a fraction, an exponent, a
// string or an overflow a 400 before the use case.
type saveTypedRequest struct {
	Values map[string]int64 `json:"Values"`
}

type typedFieldResponse struct {
	Key string `json:"Key"`
	// Kind is duration (seconds), size (bytes) or count.
	Kind    string `json:"Kind"     enums:"duration,size,count"`
	Value   int64  `json:"Value"`
	Default int64  `json:"Default"`
	Min     int64  `json:"Min"`
	Max     int64  `json:"Max"`
	// InPrivacyPolicy marks a period published in the Privacy Policy.
	InPrivacyPolicy bool `json:"InPrivacyPolicy"`
}

type typedSettingResponse struct {
	// Custom: a value is saved; false: the defaults apply.
	Custom    bool                 `json:"Custom"`
	UpdatedAt *time.Time           `json:"UpdatedAt"`
	Fields    []typedFieldResponse `json:"Fields"`
}

func toTypedResponse(v platformSettingsUseCase.TypedView) typedSettingResponse {
	out := typedSettingResponse{Custom: v.Custom, UpdatedAt: v.UpdatedAt, Fields: make([]typedFieldResponse, 0, len(v.Fields))}
	for _, f := range v.Fields {
		out.Fields = append(out.Fields, typedFieldResponse{
			Key: f.Key, Kind: f.Kind, Value: f.Value, Default: f.Default, Min: f.Min, Max: f.Max, InPrivacyPolicy: f.InPrivacyPolicy,
		})
	}
	return out
}

func (h *Handler) initTyped(settingsAPI *gin.RouterGroup) {
	settingsAPI.GET("retention", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.getRetentionSettings)
	settingsAPI.PUT("retention", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.saveRetentionSettings)
	settingsAPI.DELETE("retention", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.resetRetentionSettings)
	settingsAPI.GET("archive-limits", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.getArchiveLimitSettings)
	settingsAPI.PUT("archive-limits", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.saveArchiveLimitSettings)
	settingsAPI.DELETE("archive-limits", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.resetArchiveLimitSettings)
	settingsAPI.GET("test-runs", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.getTestRunSettings)
	settingsAPI.PUT("test-runs", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.saveTestRunSettings)
	settingsAPI.DELETE("test-runs", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.resetTestRunSettings)
}

func respondTyped(ctx *gin.Context, v platformSettingsUseCase.TypedView, err error) {
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toTypedResponse(v))
}

func bindTypedValues(ctx *gin.Context) (platformSettingsModel.Values, bool) {
	var req saveTypedRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return nil, false
	}
	return req.Values, true
}

// GetRetentionSettings godoc
// @Summary  Get the data retention periods (seconds) in force, with defaults and bounds
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=typedSettingResponse}
// @Router   /settings/retention [get]
func (h *Handler) getRetentionSettings(ctx *gin.Context) {
	v, err := h.useCase.GetRetentionSettings(ctx)
	respondTyped(ctx, v, err)
}

// SaveRetentionSettings godoc
// @Summary  Save every data retention period (seconds); a missing or unknown field, a value out of bounds or a grace not shorter than the inactivity period is a 400 (Context.field)
// @Tags     settings
// @Accept   json
// @Produce  json
// @Param    body  body      saveTypedRequest  true  "every period, integer seconds"
// @Success  200   {object}  response.Response{data=typedSettingResponse}
// @Failure  400   {object}  response.Response
// @Router   /settings/retention [put]
func (h *Handler) saveRetentionSettings(ctx *gin.Context) {
	in, ok := bindTypedValues(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.SaveRetentionSettings(ctx, in)
	respondTyped(ctx, v, err)
}

// ResetRetentionSettings godoc
// @Summary  Remove the saved retention periods: the defaults apply again
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=typedSettingResponse}  "the defaults"
// @Router   /settings/retention [delete]
func (h *Handler) resetRetentionSettings(ctx *gin.Context) {
	v, err := h.useCase.ResetRetentionSettings(ctx)
	respondTyped(ctx, v, err)
}

// GetArchiveLimitSettings godoc
// @Summary  Get the task archive limits (bytes, entries) in force, with defaults and bounds
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=typedSettingResponse}
// @Router   /settings/archive-limits [get]
func (h *Handler) getArchiveLimitSettings(ctx *gin.Context) {
	v, err := h.useCase.GetArchiveLimitSettings(ctx)
	respondTyped(ctx, v, err)
}

// SaveArchiveLimitSettings godoc
// @Summary  Save every task archive limit; a missing or unknown field, a value out of bounds or one file over all files together is a 400 (Context.field)
// @Tags     settings
// @Accept   json
// @Produce  json
// @Param    body  body      saveTypedRequest  true  "every limit, integer bytes or entries"
// @Success  200   {object}  response.Response{data=typedSettingResponse}
// @Failure  400   {object}  response.Response
// @Router   /settings/archive-limits [put]
func (h *Handler) saveArchiveLimitSettings(ctx *gin.Context) {
	in, ok := bindTypedValues(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.SaveArchiveLimitSettings(ctx, in)
	respondTyped(ctx, v, err)
}

// ResetArchiveLimitSettings godoc
// @Summary  Remove the saved archive limits: the defaults apply again
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=typedSettingResponse}  "the defaults"
// @Router   /settings/archive-limits [delete]
func (h *Handler) resetArchiveLimitSettings(ctx *gin.Context) {
	v, err := h.useCase.ResetArchiveLimitSettings(ctx)
	respondTyped(ctx, v, err)
}

// GetTestRunSettings godoc
// @Summary  Get the test run leases (seconds) in force, with defaults and bounds
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=typedSettingResponse}
// @Router   /settings/test-runs [get]
func (h *Handler) getTestRunSettings(ctx *gin.Context) {
	v, err := h.useCase.GetTestRunSettings(ctx)
	respondTyped(ctx, v, err)
}

// SaveTestRunSettings godoc
// @Summary  Save both test run leases; a missing or unknown field, a value out of bounds or a longest run under one lease is a 400 (Context.field)
// @Tags     settings
// @Accept   json
// @Produce  json
// @Param    body  body      saveTypedRequest  true  "Lease and LeaseMax, integer seconds"
// @Success  200   {object}  response.Response{data=typedSettingResponse}
// @Failure  400   {object}  response.Response
// @Router   /settings/test-runs [put]
func (h *Handler) saveTestRunSettings(ctx *gin.Context) {
	in, ok := bindTypedValues(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.SaveTestRunSettings(ctx, in)
	respondTyped(ctx, v, err)
}

// ResetTestRunSettings godoc
// @Summary  Remove the saved test run leases: the defaults apply again
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=typedSettingResponse}  "the defaults"
// @Router   /settings/test-runs [delete]
func (h *Handler) resetTestRunSettings(ctx *gin.Context) {
	v, err := h.useCase.ResetTestRunSettings(ctx)
	respondTyped(ctx, v, err)
}
