package exercise

import (
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/pagination"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
)

type archivePreviewRequest struct {
	UploadID        uuid.UUID                      `json:"UploadID"`
	FileID          uuid.UUID                      `json:"FileID"`
	Password        string                         `json:"Password"`
	OwnerEventID    *uuid.UUID                     `json:"OwnerEventID"`
	FixedExerciseID uuid.UUID                      `json:"FixedExerciseID"`
	Targets         []exerciseUseCase.ImportTarget `json:"Targets"`
}

// previewArchive godoc
// @Summary Preview every archive item without writing tasks, files or images
// @Description Omitted Targets are nonbinding editable-ID suggestions. Explicit choices must name every item exactly once. FixedExerciseID forces one item into that editable target. Revisions bind the exact immutable source/item and current target content. Summary never returns flags or device secrets.
// @Tags exercises
// @Accept json
// @Produce json
// @Param body body archivePreviewRequest true "owned source and optional per-item choices"
// @Success 200 {object} response.Response{data=exerciseUseCase.ArchiveImportPreview}
// @Failure 400 {object} response.Response
// @Failure 403 {object} response.Response
// @Router /exercises/import/preview [post]
func (h *Handler) previewArchive(ctx *gin.Context) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var req archivePreviewRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	view, err := h.useCase.PreviewArchiveImport(ctx, actor, exerciseUseCase.PreviewArchiveInput{UploadID: req.UploadID, FileID: req.FileID, Password: req.Password, OwnerEventID: req.OwnerEventID, FixedExerciseID: req.FixedExerciseID, Targets: req.Targets})
	if err != nil {
		abortArchiveError(ctx, err)
		return
	}
	response.AbortWithData(ctx, view)
}

// importTargets godoc
// @Summary Search nonarchived editable import targets, with owner context
// @Description Write policy is applied before pagination. Same-name tasks are distinguished by Scope and OwnerEventID/Name/Tag. Search and Total use the same working snapshot as the returned names.
// @Tags exercises
// @Produce json
// @Param search query string false "working task name"
// @Param cursor query string false "last task UUID of the previous page, descending"
// @Param pageSize query int false "1..200, default 50"
// @Success 200 {object} response.Response{data=pagination.CursorPage[exerciseUseCase.ImportTargetExercise]}
// @Failure 400 {object} response.Response
// @Failure 403 {object} response.Response
// @Router /exercises/import/targets [get]
func (h *Handler) importTargets(ctx *gin.Context) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	req := struct {
		pagination.CursorParams
		Search string `form:"search"`
	}{CursorParams: pagination.CursorParams{PageSize: 50}}
	if err := ctx.ShouldBindQuery(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if req.PageSize == 0 {
		req.PageSize = 50
	}
	if err := req.CursorParams.Validate(); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if req.Sort != pagination.SortDesc {
		response.AbortWithBadRequest(ctx, pagination.ErrInvalidSort)
		return
	}
	view, err := h.useCase.ListImportTargets(ctx, actor, req.Search, req.Cursor, req.PageSize)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, view)
}
