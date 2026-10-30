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
	Custom bool `json:"Custom"`
	// Ignored: a value is saved but does not apply (it no longer validates or does not parse), so the defaults are
	// in force; reset or save again.
	Ignored   bool                 `json:"Ignored"`
	UpdatedAt *time.Time           `json:"UpdatedAt"`
	Fields    []typedFieldResponse `json:"Fields"`
}

func toTypedResponse(v platformSettingsUseCase.TypedView) typedSettingResponse {
	out := typedSettingResponse{Custom: v.Custom, Ignored: v.Ignored, UpdatedAt: v.UpdatedAt, Fields: make([]typedFieldResponse, 0, len(v.Fields))}
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
	settingsAPI.GET("node-profiles", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.getNodeProfileSettings)
	settingsAPI.PUT("node-profiles", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.saveNodeProfileSettings)
	settingsAPI.DELETE("node-profiles", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.resetNodeProfileSettings)
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

// nodeProfilesResponse is the node profile setting: the typed fields Container, MicroVM and VM (1 is ticked) and, per
// field, whether a connected laboratory agent can run that profile now.
type nodeProfilesResponse struct {
	typedSettingResponse
	// RunnableNow is keyed by field (Container, MicroVM, VM). A ticked profile with false here is the one to warn
	// about; it changes nothing else.
	RunnableNow map[string]bool `json:"RunnableNow"`
}

func respondNodeProfiles(ctx *gin.Context, v platformSettingsUseCase.NodeProfilesView, err error) {
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, nodeProfilesResponse{typedSettingResponse: toTypedResponse(v.TypedView), RunnableNow: v.RunnableNow})
}

// GetNodeProfileSettings godoc
// @Summary  Get the node profiles the platform supports (default: the container only), with, per profile, whether a connected laboratory agent can run it now
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=nodeProfilesResponse}
// @Router   /settings/node-profiles [get]
func (h *Handler) getNodeProfileSettings(ctx *gin.Context) {
	v, err := h.useCase.GetNodeProfileSettings(ctx)
	respondNodeProfiles(ctx, v, err)
}

// SaveNodeProfileSettings godoc
// @Summary  Save the supported node profiles (Container, MicroVM, VM: 1 or 0 each); a missing or unknown field, a value other than 0 or 1 or no profile at all is a 400 (Context.field)
// @Tags     settings
// @Accept   json
// @Produce  json
// @Param    body  body      saveTypedRequest  true  "Container, MicroVM and VM, 1 or 0"
// @Success  200   {object}  response.Response{data=nodeProfilesResponse}
// @Failure  400   {object}  response.Response
// @Router   /settings/node-profiles [put]
func (h *Handler) saveNodeProfileSettings(ctx *gin.Context) {
	in, ok := bindTypedValues(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.SaveNodeProfileSettings(ctx, in)
	respondNodeProfiles(ctx, v, err)
}

// ResetNodeProfileSettings godoc
// @Summary  Remove the saved node profiles: the container only applies again
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=nodeProfilesResponse}  "the defaults"
// @Router   /settings/node-profiles [delete]
func (h *Handler) resetNodeProfileSettings(ctx *gin.Context) {
	v, err := h.useCase.ResetNodeProfileSettings(ctx)
	respondNodeProfiles(ctx, v, err)
}
