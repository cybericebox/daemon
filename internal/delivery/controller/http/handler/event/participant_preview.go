package event

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"io"
	"mime"
	"net/http"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/cybericebox/daemon/pkg/labaccess"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type previewFlagRequest struct {
	Flag string `json:"Flag"`
}
type previewProxyRequest struct {
	Device string `json:"Device"`
	Port   int32  `json:"Port"`
}

type previewUseCase interface {
	EndPreviewRuns(context.Context, uuid.UUID, uuid.UUID) error
	GetPreviewSettings(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.PreviewSettings, error)
	PutPreviewSettings(context.Context, uuid.UUID, uuid.UUID, eventUseCase.PreviewSettingsInput) (eventUseCase.PreviewSettings, error)
	GetPreviewDefaults(context.Context) (eventUseCase.PreviewSettings, error)
	PutPreviewDefaults(context.Context, eventUseCase.PreviewSettingsInput) (eventUseCase.PreviewSettings, error)
	ListPreviewCatalog(context.Context, uuid.UUID, uuid.UUID, string) ([]eventUseCase.PreviewCatalogItem, error)
	PreviewBoard(context.Context, uuid.UUID, uuid.UUID) (eventUseCase.PreviewBoard, error)
	StartPreviewRun(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, time.Duration) (eventUseCase.PreviewRunView, error)
	SubmitPreviewFlag(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, string) (eventUseCase.SubmitChallengeResult, error)
	UnlockPreviewHint(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (eventUseCase.OwnHintView, error)
	PreviewSolves(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (eventUseCase.ChallengeSolvesPage, error)
	StreamPreviewFile(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID, uuid.UUID) (io.ReadCloser, mediaModel.File, error)
	PreviewLabStatus(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (exerciseModel.LabDeployStatus, error)
	PreviewVPNConfig(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) (string, error)
	PreviewProxyLink(context.Context, uuid.UUID, uuid.UUID, uuid.UUID, string, int32) (labaccess.Link, error)
	EndPreviewRun(context.Context, uuid.UUID, uuid.UUID, uuid.UUID) error
}

func (h *Handler) initPreviewAPI(router *gin.RouterGroup) {
	defaults := router.Group("settings/participant-preview")
	defaults.GET("", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.getPreviewDefaults)
	defaults.PUT("", h.prot.RequirePermission(rbac.PermPlatformSettingsWrite), h.putPreviewDefaults)
	defaults.GET("catalog", h.prot.RequirePermission(rbac.PermPlatformSettingsRead), h.previewCatalog)
	preview := router.Group("events/:id/manage/participant-preview", h.prot.RequirePermission(rbac.PermSelf))
	// Preview reads also require manage access; observer/read roles cannot enter participant simulation.
	preview.GET("settings", h.requireManage, h.getPreviewSettings)
	preview.PUT("settings", h.requireManage, h.putPreviewSettings)
	preview.GET("catalog", h.requireManage, h.previewCatalog)
	preview.GET("board", h.requireManage, h.previewBoard)
	preview.DELETE("runs", h.requirePreviewCleanup, h.endPreviewRuns)
	preview.POST("selections/:selectionID/start", h.requireManage, h.startPreview)
	preview.DELETE("selections/:selectionID/run", h.requirePreviewCleanup, h.endPreview)
	preview.GET("selections/:selectionID/lab", h.requireManage, h.previewLab)
	preview.GET("selections/:selectionID/vpn", h.requireManage, h.previewVPN)
	preview.POST("selections/:selectionID/proxy", h.requireManage, h.previewProxy)
	preview.POST("selections/:selectionID/tasks/:taskID/submit", h.requireManage, h.submitPreview)
	preview.GET("selections/:selectionID/tasks/:taskID/solves", h.requireManage, h.previewSolves)
	preview.POST("selections/:selectionID/tasks/:taskID/hints/:hintID/unlock", h.requireManage, h.previewHint)
	preview.GET("selections/:selectionID/tasks/:taskID/files/:fileID", h.requireManage, h.previewFile)
}
func previewUser(ctx *gin.Context) uuid.UUID {
	claims, _ := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	return claims.UserID
}
func previewParam(ctx *gin.Context, key string) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param(key))
	if err != nil || id == uuid.Nil {
		response.AbortWithBadRequest(ctx, fmt.Errorf("invalid %s", key))
		return uuid.Nil, false
	}
	return id, true
}
func previewIDs(ctx *gin.Context) (event, user, selection uuid.UUID, ok bool) {
	event, ok = previewParam(ctx, "id")
	if !ok {
		return
	}
	user = previewUser(ctx)
	selection, ok = previewParam(ctx, "selectionID")
	return
}
func previewReply(ctx *gin.Context, data any, err error) {
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, data)
}
func (h *Handler) requirePreviewCleanup(ctx *gin.Context) {
	event, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.RequireManageEvent(ctx.Request.Context(), event, previewUser(ctx)); err != nil {
		response.AbortWithError(ctx, err)
	}
}

// getPreviewDefaults godoc
// @Summary Get platform default preview exercises
// @Tags participant-preview
// @Produce json
// @Success 200 {object} response.Response{data=eventUseCase.PreviewSettings}
// @Router /settings/participant-preview [get]
func (h *Handler) getPreviewDefaults(ctx *gin.Context) {
	v, e := h.useCase.GetPreviewDefaults(ctx.Request.Context())
	previewReply(ctx, v, e)
}

// putPreviewDefaults godoc
// @Summary Replace platform default preview exercises
// @Tags participant-preview
// @Accept json
// @Produce json
// @Param body body eventUseCase.PreviewSettingsInput true "Published catalog selections"
// @Success 200 {object} response.Response{data=eventUseCase.PreviewSettings}
// @Router /settings/participant-preview [put]
func (h *Handler) putPreviewDefaults(ctx *gin.Context) {
	var in eventUseCase.PreviewSettingsInput
	if err := ctx.ShouldBindJSON(&in); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, e := h.useCase.PutPreviewDefaults(ctx.Request.Context(), in)
	previewReply(ctx, v, e)
}

// getPreviewSettings godoc
// @Summary Get inherited or replaced event preview exercises
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Success 200 {object} response.Response{data=eventUseCase.PreviewSettings}
// @Router /events/{id}/manage/participant-preview/settings [get]
func (h *Handler) getPreviewSettings(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, e := h.useCase.GetPreviewSettings(ctx.Request.Context(), id, previewUser(ctx))
	previewReply(ctx, v, e)
}

// putPreviewSettings godoc
// @Summary Replace or inherit event preview exercises
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param body body eventUseCase.PreviewSettingsInput true "Preview selection"
// @Success 200 {object} response.Response{data=eventUseCase.PreviewSettings}
// @Router /events/{id}/manage/participant-preview/settings [put]
func (h *Handler) putPreviewSettings(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	var in eventUseCase.PreviewSettingsInput
	if err := ctx.ShouldBindJSON(&in); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, e := h.useCase.PutPreviewSettings(ctx.Request.Context(), id, previewUser(ctx), in)
	previewReply(ctx, v, e)
}

// previewCatalog godoc
// @Summary List published preview exercise variants
// @Tags participant-preview
// @Param search query string false "Search"
// @Success 200 {object} response.Response{data=[]eventUseCase.PreviewCatalogItem}
// @Router /settings/participant-preview/catalog [get]
// @Router /events/{id}/manage/participant-preview/catalog [get]
func (h *Handler) previewCatalog(ctx *gin.Context) {
	id := uuid.Nil
	if ctx.Param("id") != "" {
		var ok bool
		id, ok = parseEventID(ctx)
		if !ok {
			return
		}
	}
	v, e := h.useCase.ListPreviewCatalog(ctx.Request.Context(), id, previewUser(ctx), ctx.Query("search"))
	previewReply(ctx, v, e)
}

// previewBoard godoc
// @Summary Get owned participant preview board
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Success 200 {object} response.Response{data=previewBoardResponse}
// @Router /events/{id}/manage/participant-preview/board [get]
func (h *Handler) previewBoard(ctx *gin.Context) {
	id, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, e := h.useCase.PreviewBoard(ctx.Request.Context(), id, previewUser(ctx))
	previewReply(ctx, toPreviewBoardResponse(v), e)
}

// startPreview godoc
// @Summary Start or reuse an owned preview run
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param selectionID path string true "selectionID"
// @Param request body previewStartRequest false "the chosen run length"
// @Success 200 {object} response.Response{data=eventUseCase.PreviewRunView}
// @Router /events/{id}/manage/participant-preview/selections/{selectionID}/start [post]
func (h *Handler) startPreview(ctx *gin.Context) {
	event, user, selection, ok := previewIDs(ctx)
	if !ok {
		return
	}
	var in previewStartRequest
	if ctx.Request.ContentLength != 0 {
		if err := ctx.ShouldBindJSON(&in); err != nil || in.LeaseMinutes < 0 {
			response.AbortWithBadRequest(ctx)
			return
		}
	}
	v, e := h.useCase.StartPreviewRun(ctx.Request.Context(), event, user, selection, time.Duration(in.LeaseMinutes)*time.Minute)
	previewReply(ctx, v, e)
}

// endPreview godoc
// @Summary End an owned preview run
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param selectionID path string true "selectionID"
// @Success 204
// @Router /events/{id}/manage/participant-preview/selections/{selectionID}/run [delete]
func (h *Handler) endPreview(ctx *gin.Context) {
	event, user, selection, ok := previewIDs(ctx)
	if !ok {
		return
	}
	if err := h.useCase.EndPreviewRun(ctx.Request.Context(), event, user, selection); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// previewLab godoc
// @Summary Read owned preview laboratory status
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param selectionID path string true "selectionID"
// @Success 200 {object} response.Response{data=exerciseModel.LabDeployStatus}
// @Router /events/{id}/manage/participant-preview/selections/{selectionID}/lab [get]
func (h *Handler) previewLab(ctx *gin.Context) {
	event, user, selection, ok := previewIDs(ctx)
	if !ok {
		return
	}
	v, e := h.useCase.PreviewLabStatus(ctx.Request.Context(), event, user, selection)
	v.VPNConfig = ""
	v.SolvedTasks = nil
	previewReply(ctx, v, e)
}

// previewVPN godoc
// @Summary Download owned preview VPN configuration
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param selectionID path string true "selectionID"
// @Success 200 {object} response.Response{data=object}
// @Router /events/{id}/manage/participant-preview/selections/{selectionID}/vpn [get]
func (h *Handler) previewVPN(ctx *gin.Context) {
	event, user, selection, ok := previewIDs(ctx)
	if !ok {
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	config, e := h.useCase.PreviewVPNConfig(ctx.Request.Context(), event, user, selection)
	previewReply(ctx, struct{ Config string }{config}, e)
}

// previewProxy godoc
// @Summary Open owned preview web endpoint
// @Param body body previewProxyRequest true "Device endpoint"
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param selectionID path string true "selectionID"
// @Success 200 {object} response.Response{data=labaccess.Link}
// @Router /events/{id}/manage/participant-preview/selections/{selectionID}/proxy [post]
func (h *Handler) previewProxy(ctx *gin.Context) {
	event, user, selection, ok := previewIDs(ctx)
	if !ok {
		return
	}
	var in previewProxyRequest
	if err := ctx.ShouldBindJSON(&in); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, e := h.useCase.PreviewProxyLink(ctx.Request.Context(), event, user, selection, in.Device, in.Port)
	previewReply(ctx, v, e)
}

// submitPreview godoc
// @Summary Check a preview flag without competition attempts
// @Param body body previewFlagRequest true "Flag"
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param selectionID path string true "selectionID"
// @Param taskID path string true "taskID"
// @Success 200 {object} response.Response{data=eventUseCase.SubmitChallengeResult}
// @Router /events/{id}/manage/participant-preview/selections/{selectionID}/tasks/{taskID}/submit [post]
func (h *Handler) submitPreview(ctx *gin.Context) {
	event, user, selection, ok := previewIDs(ctx)
	if !ok {
		return
	}
	task, ok := previewParam(ctx, "taskID")
	if !ok {
		return
	}
	var in previewFlagRequest
	if err := ctx.ShouldBindJSON(&in); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, e := h.useCase.SubmitPreviewFlag(ctx.Request.Context(), event, user, selection, task, in.Flag)
	previewReply(ctx, v, e)
}

// previewHint godoc
// @Summary Unlock an owned preview hint
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param selectionID path string true "selectionID"
// @Param taskID path string true "taskID"
// @Param hintID path string true "hintID"
// @Success 200 {object} response.Response{data=eventUseCase.OwnHintView}
// @Router /events/{id}/manage/participant-preview/selections/{selectionID}/tasks/{taskID}/hints/{hintID}/unlock [post]
func (h *Handler) previewHint(ctx *gin.Context) {
	event, user, selection, ok := previewIDs(ctx)
	if !ok {
		return
	}
	task, ok := previewParam(ctx, "taskID")
	if !ok {
		return
	}
	hint, ok := previewParam(ctx, "hintID")
	if !ok {
		return
	}
	v, e := h.useCase.UnlockPreviewHint(ctx.Request.Context(), event, user, selection, task, hint)
	previewReply(ctx, v, e)
}

// previewSolves godoc
// @Summary Read own preview solve
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param selectionID path string true "selectionID"
// @Param taskID path string true "taskID"
// @Success 200 {object} response.Response{data=eventUseCase.ChallengeSolvesPage}
// @Router /events/{id}/manage/participant-preview/selections/{selectionID}/tasks/{taskID}/solves [get]
func (h *Handler) previewSolves(ctx *gin.Context) {
	event, user, selection, ok := previewIDs(ctx)
	if !ok {
		return
	}
	task, ok := previewParam(ctx, "taskID")
	if !ok {
		return
	}
	v, e := h.useCase.PreviewSolves(ctx.Request.Context(), event, user, selection, task)
	previewReply(ctx, v, e)
}

// previewFile godoc
// @Summary Download selected preview task attachment
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Param selectionID path string true "selectionID"
// @Param taskID path string true "taskID"
// @Param fileID path string true "fileID"
// @Success 200 {file} file
// @Router /events/{id}/manage/participant-preview/selections/{selectionID}/tasks/{taskID}/files/{fileID} [get]
func (h *Handler) previewFile(ctx *gin.Context) {
	event, user, selection, ok := previewIDs(ctx)
	if !ok {
		return
	}
	task, ok := previewParam(ctx, "taskID")
	if !ok {
		return
	}
	fileID, ok := previewParam(ctx, "fileID")
	if !ok {
		return
	}
	stream, file, err := h.useCase.StreamPreviewFile(ctx.Request.Context(), event, user, selection, task, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer stream.Close()
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	ctx.Header("X-Content-Type-Options", "nosniff")
	download.ExtendDeadline(ctx)
	ctx.Header("Cache-Control", "private, no-store")
	ctx.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	download.File(ctx, stream, file.SizeBytes, file.ContentType)
}

// endPreviewRuns godoc
// @Summary Reset all owned preview progress and laboratories
// @Tags participant-preview
// @Param id path string true "Event ID"
// @Success 204
// @Router /events/{id}/manage/participant-preview/runs [delete]
func (h *Handler) endPreviewRuns(ctx *gin.Context) {
	event, ok := parseEventID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.EndPreviewRuns(ctx.Request.Context(), event, previewUser(ctx)); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}

// previewStartRequest is the optional body of a preview start.
type previewStartRequest struct {
	// LeaseMinutes is the run length of the preview's test laboratory: one of the offered lengths; 0 or absent takes
	// the lease in force.
	LeaseMinutes int `json:"LeaseMinutes"`
}
