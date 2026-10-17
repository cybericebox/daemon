// Package imageRegistry serves the platform image registry settings of
// «Панель платформи». Tokens are write-only: requests may carry them, no
// response ever does.
package imageRegistry

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	// IUseCase is this handler's narrow port into the useCase layer.
	IUseCase interface {
		GetSettings(ctx context.Context) (imageRegistryUseCase.SettingsView, error)
		SaveSettings(ctx context.Context, in imageRegistryUseCase.SaveSettingsInput) (imageRegistryUseCase.SettingsView, error)
		ListRepositories(ctx context.Context, in imageRegistryUseCase.ListRepositoriesInput) ([]string, error)
		ClearSettings(ctx context.Context) error
	}

	// IProtection is the subset of the protection middleware this handler needs.
	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}
)

// settingsResponse is the registry settings as the panel sees them: whether a
// token is stored, never the token.
type settingsResponse struct {
	Configured   bool       `json:"Configured"`
	Provider     string     `json:"Provider"`
	Account      string     `json:"Account"`
	Repository   string     `json:"Repository"`
	PushTokenSet bool       `json:"PushTokenSet"`
	PullTokenSet bool       `json:"PullTokenSet"`
	UpdatedAt    *time.Time `json:"UpdatedAt"`
}

// saveSettingsRequest is the settings form. Tokens are write-only: an empty
// token keeps the stored one; ClearPullToken removes the pull token.
type saveSettingsRequest struct {
	Provider       string `json:"Provider"`
	Account        string `json:"Account"`
	Repository     string `json:"Repository"`
	PushToken      string `json:"PushToken"`
	PullToken      string `json:"PullToken"`
	ClearPullToken bool   `json:"ClearPullToken"`
}

// listRepositoriesRequest asks which repositories a token can see. An empty
// Token uses the stored push token of the same provider account.
type listRepositoriesRequest struct {
	Provider string `json:"Provider"`
	Account  string `json:"Account"`
	Token    string `json:"Token"`
}

type repositoriesResponse struct {
	Repositories []string `json:"Repositories"`
}

func toResponse(v imageRegistryUseCase.SettingsView) settingsResponse {
	return settingsResponse{
		Configured: v.Configured, Provider: v.Provider, Account: v.Account, Repository: v.Repository,
		PushTokenSet: v.PushTokenSet, PullTokenSet: v.PullTokenSet, UpdatedAt: v.UpdatedAt,
	}
}

func New(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

// Init hangs this package's routes on the shared API router group.
func (h *Handler) Init(router *gin.RouterGroup) {
	api := router.Group("settings/image-registry")
	{
		api.GET("", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.getSettings)
		api.PUT("", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.saveSettings)
		api.DELETE("", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.clearSettings)
		api.POST("repositories", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.listRepositories)
	}
}

// GetImageRegistrySettings godoc
// @Summary  Get the platform image registry settings (tokens are never returned)
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=settingsResponse}
// @Router   /settings/image-registry [get]
func (h *Handler) getSettings(ctx *gin.Context) {
	v, err := h.useCase.GetSettings(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(v))
}

// SaveImageRegistrySettings godoc
// @Summary  Save the platform image registry settings after checking the push token can push
// @Tags     settings
// @Accept   json
// @Produce  json
// @Param    body  body      saveSettingsRequest  true  "settings; empty tokens keep the stored ones"
// @Success  200   {object}  response.Response{data=settingsResponse}
// @Failure  400   {object}  response.Response
// @Failure  409   {object}  response.Response  "registry unavailable (Context.reason) or secret storage not configured"
// @Router   /settings/image-registry [put]
func (h *Handler) saveSettings(ctx *gin.Context) {
	var req saveSettingsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.SaveSettings(ctx, imageRegistryUseCase.SaveSettingsInput{
		Provider: req.Provider, Account: req.Account, Repository: req.Repository,
		PushToken: req.PushToken, PullToken: req.PullToken, ClearPullToken: req.ClearPullToken,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(v))
}

// ClearImageRegistrySettings godoc
// @Summary  Remove the platform image registry settings and tokens
// @Tags     settings
// @Produce  json
// @Success  200  {object}  response.Response{data=settingsResponse}  "the cleared (not configured) settings"
// @Router   /settings/image-registry [delete]
func (h *Handler) clearSettings(ctx *gin.Context) {
	if err := h.useCase.ClearSettings(ctx); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toResponse(imageRegistryUseCase.SettingsView{}))
}

// ListImageRegistryRepositories godoc
// @Summary  List the repositories a registry token can see
// @Tags     settings
// @Accept   json
// @Produce  json
// @Param    body  body      listRepositoriesRequest  true  "provider, account and token (empty: the stored one)"
// @Success  200   {object}  response.Response{data=repositoriesResponse}
// @Failure  400   {object}  response.Response
// @Failure  409   {object}  response.Response  "registry unavailable (Context.reason)"
// @Router   /settings/image-registry/repositories [post]
func (h *Handler) listRepositories(ctx *gin.Context) {
	var req listRepositoriesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	repos, err := h.useCase.ListRepositories(ctx, imageRegistryUseCase.ListRepositoriesInput{
		Provider: req.Provider, Account: req.Account, Token: req.Token,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, repositoriesResponse{Repositories: repos})
}
