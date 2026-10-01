// Package exercise is the HTTP delivery layer of the exercise catalog:
// admin-only routes gated per-permission (exercises.*), plus the attachment
// upload/download routes (files.go).
package exercise

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/labaccess"
	"github.com/cybericebox/daemon/pkg/pagination"
)

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		InfrastructureAvailable() bool
		MaxActiveTestDeploys() int
		DevicePersistenceAllowed() bool
		FlagPolicy() exerciseUseCase.FlagPolicy
		// catalog
		CreateExercise(ctx context.Context, in exerciseUseCase.CreateExerciseInput) (exerciseUseCase.ExerciseView, error)
		GetExercise(ctx context.Context, id uuid.UUID) (exerciseUseCase.ExerciseView, error)
		ListExercises(ctx context.Context, f exerciseUseCase.ExercisesFilter) (exerciseUseCase.ExercisesListResult, error)
		ListExerciseTags(ctx context.Context, actor exerciseUseCase.Actor, prefix string, limit int) ([]exerciseUseCase.ExerciseTagSuggestion, error)
		UpdateExerciseIdentity(ctx context.Context, in exerciseUseCase.UpdateExerciseInput) (exerciseUseCase.ExerciseView, error)
		GetExerciseUsage(ctx context.Context, id uuid.UUID) (exerciseUseCase.ExerciseUsage, error)
		DeleteExercise(ctx context.Context, id uuid.UUID) error
		ArchiveExercise(ctx context.Context, id, by uuid.UUID) (exerciseUseCase.ExerciseView, error)
		UnarchiveExercise(ctx context.Context, id, by uuid.UUID) (exerciseUseCase.ExerciseView, error)
		// lifecycle
		SaveDraft(ctx context.Context, exerciseID uuid.UUID, in exerciseUseCase.SaveDraftInput) (exerciseUseCase.VersionView, error)
		PublishDraft(ctx context.Context, exerciseID uuid.UUID) (exerciseUseCase.VersionView, error)
		DiscardDraft(ctx context.Context, exerciseID uuid.UUID) error
		RollbackToVersion(ctx context.Context, exerciseID, versionID, by uuid.UUID) (exerciseUseCase.VersionView, error)
		CreateCheckpoint(ctx context.Context, exerciseID, by uuid.UUID, label string) (exerciseUseCase.VersionView, error)
		RestoreToVersion(ctx context.Context, exerciseID, versionID, by uuid.UUID) (exerciseUseCase.VersionView, error)
		GetVersion(ctx context.Context, exerciseID, versionID uuid.UUID) (exerciseUseCase.VersionView, error)
		GetWorkingCopy(ctx context.Context, exerciseID uuid.UUID) (exerciseUseCase.VersionView, error)
		ListVersions(ctx context.Context, exerciseID uuid.UUID) ([]exerciseUseCase.VersionListItem, error)
		// deploy (per-variant test)
		DeployVariantTest(ctx context.Context, userID, versionID, variantID uuid.UUID) (exerciseModel.DeployHandle, error)
		DeployTestStatus(ctx context.Context, userID, deployID uuid.UUID) (exerciseModel.LabDeployStatus, error)
		DestroyDeployTest(ctx context.Context, userID, deployID uuid.UUID) error
		ResetTestDeployDevice(ctx context.Context, userID, deployID uuid.UUID, device string) error
		RescueTestDeployDevice(ctx context.Context, userID, deployID uuid.UUID, device string, enable bool) error
		ListTestDeploys(ctx context.Context, userID, exerciseID uuid.UUID) ([]exerciseModel.TestDeploy, error)
		ExtendTestDeploy(ctx context.Context, userID, deployID uuid.UUID) (exerciseModel.TestDeploy, error)
		CheckTestFlag(ctx context.Context, userID, deployID, taskID uuid.UUID, flag string) (bool, error)
		OpenTestDeployLink(ctx context.Context, userID, deployID uuid.UUID, device string, port int32) (labaccess.Link, error)
		// archive portability
		ExportExerciseArchive(ctx context.Context, id uuid.UUID, opts exerciseUseCase.ExportOptions) ([]byte, error)
		ImportExerciseArchive(ctx context.Context, in exerciseUseCase.ImportExerciseInput) (exerciseUseCase.ExerciseView, error)
		// files
		UploadFile(ctx context.Context, name, contentType string, r io.Reader, createdBy uuid.UUID) (mediaModel.File, error)
		StreamFile(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error)
		// MaxUploadBytes is the same cap UploadFile enforces internally; the
		// handler uses it to cap the request body at the HTTP boundary too
		// (see files.go), so an oversized upload is rejected before the
		// multipart body is parsed, not after.
		MaxUploadBytes() int64
		// W4: data-dependent authorization and ownership
		AuthorizeExercise(ctx context.Context, actor exerciseUseCase.Actor, id uuid.UUID, action exerciseUseCase.Action) (exerciseUseCase.Access, error)
		AuthorizeFileUpload(ctx context.Context, actor exerciseUseCase.Actor) error
		AuthorizeFileDownload(ctx context.Context, actor exerciseUseCase.Actor, file mediaModel.File) error
		AuthorizeTestDeploy(ctx context.Context, actor exerciseUseCase.Actor, exerciseID, versionID uuid.UUID) error
		GetAccessSummary(ctx context.Context, actor exerciseUseCase.Actor) (exerciseUseCase.AccessSummary, error)
		CreateExerciseFor(ctx context.Context, actor exerciseUseCase.Actor, in exerciseUseCase.CreateExerciseInput) (exerciseUseCase.ExerciseView, error)
		GetExerciseFor(ctx context.Context, actor exerciseUseCase.Actor, id uuid.UUID) (exerciseUseCase.ExerciseView, error)
		ListExercisesFor(ctx context.Context, actor exerciseUseCase.Actor, f exerciseUseCase.ExercisesFilter) (exerciseUseCase.ExercisesListResult, error)
		SetExerciseAccess(ctx context.Context, actor exerciseUseCase.Actor, id uuid.UUID, in exerciseUseCase.SetAccessInput) (exerciseUseCase.ExerciseView, error)
		ProposeExercise(ctx context.Context, actor exerciseUseCase.Actor, id uuid.UUID, note string) (exerciseUseCase.ProposalView, error)
		ListProposals(ctx context.Context, status string) ([]exerciseUseCase.ProposalView, error)
		ApproveProposal(ctx context.Context, actor exerciseUseCase.Actor, proposalID uuid.UUID, in exerciseUseCase.ApproveProposalInput) (exerciseUseCase.ProposalView, error)
		RejectProposal(ctx context.Context, actor exerciseUseCase.Actor, proposalID uuid.UUID, note string) (exerciseUseCase.ProposalView, error)
	}
)

func NewExerciseAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	ex := router.Group("exercises")
	{
		// W4: every route below is PermSelf-gated plus the data-dependent
		// exercise policy (h.authorize / the use case): admins keep their RBAC
		// rights, event managers work on their events' exercises and read
		// the catalog exercises available to them. Export/import stay RBAC.
		self := h.prot.RequirePermission(rbac.PermSelf)
		ex.GET("", self, h.list)
		ex.GET("tags", self, h.listTags)
		ex.GET("access", self, h.access)
		ex.POST("", self, h.create)

		// files routes are static segments ("files", "files/:fileID") and must
		// be registered before the ":id" param routes below — gin's router
		// clashes registering a static child under a path where a differently
		// named wildcard (":id") was already claimed at that position.
		ex.POST("files", self, h.uploadFile)
		ex.GET("files/:fileID", self, h.downloadFile)
		ex.POST("export", h.prot.RequirePermission(rbac.PermExercisesExport), h.export)
		ex.POST("import", h.prot.RequirePermission(rbac.PermExercisesWrite), h.importArchive)
		ex.GET("capabilities", self, h.capabilities)
		ex.GET("flag-policy", self, h.flagPolicy)

		// catalog proposals (admin review)
		ex.GET("proposals", h.prot.RequirePermission(rbac.PermExercisesRead), h.listProposals)
		ex.POST("proposals/:proposalID/approve", h.prot.RequirePermission(rbac.PermExercisesPublish), h.approveProposal)
		ex.POST("proposals/:proposalID/reject", h.prot.RequirePermission(rbac.PermExercisesPublish), h.rejectProposal)

		// deploy routes: "deploys" is a static segment sibling to ":id" and, like
		// "files", must be registered before the ":id" param routes. Status,
		// extend and destroy are owner-scoped in the use case; creating one
		// is authorized per exercise (h.deploy).
		ex.GET("deploys", self, h.listDeploys)
		ex.GET("deploys/:deployID", self, h.deployStatus)
		ex.POST("deploys/:deployID/extend", self, h.extendDeploy)
		ex.POST("deploys/:deployID/link", self, h.deployLink)
		ex.POST("deploys/:deployID/check", self, h.checkDeploy)
		ex.POST("deploys/:deployID/devices/:device/reset", self, h.resetDeployDevice)
		ex.POST("deploys/:deployID/devices/:device/rescue", self, h.rescueDeployDevice)
		ex.DELETE("deploys/:deployID", self, h.destroyDeploy)

		ex.GET(":id", self, h.authorize(exerciseUseCase.ActionReadPublished), h.get)
		ex.GET(":id/usage", self, h.authorize(exerciseUseCase.ActionRead), h.usage)
		ex.PATCH(":id", self, h.authorize(exerciseUseCase.ActionWrite), h.update)
		ex.DELETE(":id", self, h.authorize(exerciseUseCase.ActionDelete), h.delete)
		ex.POST(":id/archive", self, h.authorize(exerciseUseCase.ActionWrite), h.archive)
		ex.POST(":id/unarchive", self, h.authorize(exerciseUseCase.ActionWrite), h.unarchive)
		ex.PUT(":id/access", h.prot.RequirePermission(rbac.PermExercisesWrite), h.setAccess)
		ex.POST(":id/proposals", self, h.authorize(exerciseUseCase.ActionPublish), h.propose)

		ex.GET(":id/versions", self, h.authorize(exerciseUseCase.ActionRead), h.listVersions)
		ex.GET(":id/versions/:versionID", self, h.authorize(exerciseUseCase.ActionReadPublished), h.getVersion)
		ex.POST(":id/versions/:versionID/rollback", self, h.authorize(exerciseUseCase.ActionPublish), h.rollback)
		ex.POST(":id/versions/:versionID/restore", self, h.authorize(exerciseUseCase.ActionWrite), h.restore)
		ex.POST(":id/checkpoints", self, h.authorize(exerciseUseCase.ActionWrite), h.checkpoint)
		ex.POST(":id/versions/:versionID/variants/:variantID/deploy", self, h.deploy)

		ex.GET(":id/draft", self, h.authorize(exerciseUseCase.ActionRead), h.getDraft)
		ex.PUT(":id/draft", self, h.authorize(exerciseUseCase.ActionWrite), h.saveDraft)
		ex.DELETE(":id/draft", self, h.authorize(exerciseUseCase.ActionPublish), h.discardDraft)
		ex.POST(":id/publish", self, h.authorize(exerciseUseCase.ActionPublish), h.publish)
	}
}

// listTags godoc
// @Summary  Existing exercise tags by use (autocomplete and tag filter)
// @Tags     exercises
// @Produce  json
// @Param    prefix  query  string  false  "tag prefix (case-insensitive, literal); empty returns the most used tags"
// @Param    limit   query  int     false  "max tags (default 50, max 200)"
// @Success  200  {object}  response.Response{data=[]exerciseUseCase.ExerciseTagSuggestion}
// @Failure  400  {object}  response.Response
// @Router   /exercises/tags [get]
func (h *Handler) listTags(ctx *gin.Context) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	limit := 0
	if raw := ctx.Query("limit"); raw != "" {
		value, err := strconv.Atoi(raw)
		if err != nil || value < 1 {
			response.AbortWithBadRequest(ctx, fmt.Errorf("limit must be a positive integer"))
			return
		}
		limit = value
	}
	items, err := h.useCase.ListExerciseTags(ctx, actor, ctx.Query("prefix"), limit)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, items)
}

// capabilities exposes only the editor action's availability, not platform-wide
// agent inventory or monitoring. The actual deploy remains guarded server-side.
func (h *Handler) capabilities(ctx *gin.Context) {
	response.AbortWithData(ctx, struct {
		Laboratories bool `json:"Laboratories"`
		// MaxActiveTestDeploys is how many test labs one user may run at once.
		MaxActiveTestDeploys int `json:"MaxActiveTestDeploys"`
		// DevicePersistence is false when the cluster does not let devices keep their state: the editor
		// hides the option then.
		DevicePersistence bool `json:"DevicePersistence"`
	}{Laboratories: h.useCase.InfrastructureAvailable(), MaxActiveTestDeploys: h.useCase.MaxActiveTestDeploys(), DevicePersistence: h.useCase.DevicePersistenceAllowed()})
}

// list godoc
// @Summary  List catalog exercises (cursor or offset page)
// @Tags     exercises
// @Produce  json
// @Param    search    query  string    false  "name/description substring"
// @Param    tags      query  []string  false  "filter by tags (any match)"  collectionFormat(multi)
// @Param    cursor    query  string    false  "id of the last row of the previous page"
// @Param    page      query  int       false  "1-based page; selects offset pagination"
// @Param    pageSize  query  int       false  "page size"
// @Param    status    query  string    false  "published, changed, draft_only, archived, or none (offset pages only)"
// @Param    sortBy    query  string    false  "name, tags, status, or updated (offset pages only)"
// @Param    sortDir   query  string    false  "asc or desc (offset pages only)"
// @Param    archived  query  string    false  "exclude (default) or only; status=archived implies only"
// @Param    scope     query  string    false  "catalog, event, or empty for both"
// @Param    event     query  []string  false  "event ids, repeated or comma-separated (max 100): relevant to ANY of them"  collectionFormat(multi)
// @Param    infrastructure  query  string  false  "yes or no"
// @Success  200  {object}  response.Response{data=pagination.CursorPage[exerciseListItemResponse]}
// @Failure  400  {object}  response.Response
// @Router   /exercises [get]
func (h *Handler) list(ctx *gin.Context) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	if ctx.Query("page") != "" {
		page, err := utils.GetOffsetPaginationParams(ctx)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		filter := exerciseUseCase.ExercisesFilter{
			Search: ctx.Query("search"), Tags: ctx.QueryArray("tags"), Status: ctx.Query("status"),
			Archived: ctx.Query("archived"),
			Page:     page.Page, PageSize: page.PageSize, SortBy: ctx.Query("sortBy"), SortDir: ctx.Query("sortDir"),
		}
		if !applyVisibilityQuery(ctx, &filter) {
			return
		}
		res, err := h.useCase.ListExercisesFor(ctx, actor, filter)
		if err != nil {
			response.AbortWithError(ctx, err)
			return
		}
		items := make([]exerciseListItemResponse, 0, len(res.Exercises))
		for _, e := range res.Exercises {
			items = append(items, exerciseListItemToResponse(e))
		}
		response.AbortWithData(ctx, pagination.NewOffsetPage(items, res.Total, page.Page, page.PageSize))
		return
	}
	page, err := utils.GetCursorPaginationParams(ctx)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	filter := exerciseUseCase.ExercisesFilter{
		Search:   ctx.Query("search"),
		Tags:     ctx.QueryArray("tags"),
		Archived: ctx.Query("archived"),
		Cursor:   page.Cursor,
		PageSize: page.PageSize,
	}
	if !applyVisibilityQuery(ctx, &filter) {
		return
	}
	res, err := h.useCase.ListExercisesFor(ctx, actor, filter)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]exerciseListItemResponse, 0, len(res.Exercises))
	for _, e := range res.Exercises {
		items = append(items, exerciseListItemToResponse(e))
	}
	response.AbortWithData(ctx, pagination.NewCursorPage(items, res.HasMore, res.NextCursor, res.Total))
}

// create godoc
// @Summary  Create a catalog exercise (identity only)
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  createExerciseRequest  true  "identity"
// @Success  200  {object}  response.Response{data=exerciseResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises [post]
func (h *Handler) create(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req createExerciseRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.CreateExerciseFor(ctx, exerciseUseCase.Actor{UserID: claims.UserID, Role: claims.Role}, exerciseUseCase.CreateExerciseInput{
		Name: req.Name, Description: req.Description, Tags: req.Tags, CreatedBy: claims.UserID, OwnerEventID: req.OwnerEventID,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, exerciseToResponse(v))
}

// get godoc
// @Summary  Get one exercise card
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response{data=exerciseResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id} [get]
func (h *Handler) get(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetExerciseFor(ctx, actor, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if !accessFrom(ctx).Full {
		// A published-only reader never learns about the working copy.
		v.DraftVersionID, v.HasChanges = nil, false
	}
	response.AbortWithData(ctx, exerciseToResponse(v))
}

// update godoc
// @Summary  Update exercise identity (name/description/tags)
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string                 true  "exercise ID"
// @Param    body  body  updateExerciseRequest  true  "identity fields"
// @Success  200  {object}  response.Response{data=exerciseResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id} [patch]
func (h *Handler) update(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	var req updateExerciseRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.UpdateExerciseIdentity(ctx, exerciseUseCase.UpdateExerciseInput{
		ID: id, Name: req.Name, Description: req.Description, Tags: req.Tags, UpdatedBy: claims.UserID,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, exerciseToResponse(v))
}

// usage godoc
// @Summary  Events (active or archived) where any version of the exercise is attached
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response{data=exerciseUsageResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/usage [get]
func (h *Handler) usage(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	u, err := h.useCase.GetExerciseUsage(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, usageToResponse(u))
}

// delete godoc
// @Summary  Delete an exercise (versions cascade)
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Failure  409  {object}  response.Response  "ErrExerciseInUse: used by events"
// @Router   /exercises/{id} [delete]
func (h *Handler) delete(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DeleteExercise(ctx, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// archive godoc
// @Summary  Archive an exercise (hidden from catalog, frozen; idempotent)
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response{data=exerciseResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/archive [post]
func (h *Handler) archive(ctx *gin.Context) {
	h.setArchived(ctx, h.useCase.ArchiveExercise)
}

// unarchive godoc
// @Summary  Return an archived exercise to the catalog (idempotent)
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response{data=exerciseResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/unarchive [post]
func (h *Handler) unarchive(ctx *gin.Context) {
	h.setArchived(ctx, h.useCase.UnarchiveExercise)
}

func (h *Handler) setArchived(ctx *gin.Context, apply func(context.Context, uuid.UUID, uuid.UUID) (exerciseUseCase.ExerciseView, error)) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	v, err := apply(ctx, id, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, exerciseToResponse(v))
}

// listVersions godoc
// @Summary  Version history of an exercise
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response{data=[]versionListItemResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/versions [get]
func (h *Handler) listVersions(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListVersions(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]versionListItemResponse, 0, len(items))
	for _, v := range items {
		out = append(out, versionListItemResponse{
			ID: v.ID, Status: v.Status, AdminNote: v.AdminNote, Label: v.Label, VariantCount: v.VariantCount,
			CreatedAt: v.CreatedAt, CreatedBy: v.CreatedBy, PublishedAt: v.PublishedAt,
		})
	}
	response.AbortWithData(ctx, out)
}

// getVersion godoc
// @Summary  One version's content (secrets masked)
// @Tags     exercises
// @Produce  json
// @Param    id         path  string  true  "exercise ID"
// @Param    versionID  path  string  true  "version ID"
// @Success  200  {object}  response.Response{data=versionResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/versions/{versionID} [get]
func (h *Handler) getVersion(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	versionID, err := uuid.FromString(ctx.Param("versionID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.GetVersion(ctx, id, versionID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	// A reader of a catalog exercise (not its admin) sees only the
	// published version; other versions are indistinguishable from absent.
	if !accessFrom(ctx).Full && v.Status != string(exerciseModel.VersionStatusPublished) {
		response.AbortWithError(ctx, exerciseModel.ErrExerciseVersionNotFound.Err())
		return
	}
	response.AbortWithData(ctx, versionToResponse(v))
}

// getDraft godoc
// @Summary  The working copy: draft row, else published content, else an empty copy
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response{data=versionResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/draft [get]
func (h *Handler) getDraft(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetWorkingCopy(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, versionToResponse(v))
}

// saveDraft godoc
// @Summary  Save the draft version (full snapshot PUT)
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string            true  "exercise ID"
// @Param    body  body  saveDraftRequest  true  "full variant snapshot"
// @Success  200  {object}  response.Response{data=versionResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/draft [put]
func (h *Handler) saveDraft(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	var req saveDraftRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	// Map the DTO into a fresh domain slice for this call only: SaveDraft
	// mutates the Variants slice it is given in place (merges kept secrets,
	// assigns fresh content ids), so the request DTO must never be reused
	// (logged/echoed) after this call.
	variants := make([]exerciseModel.Variant, 0, len(req.Variants))
	for _, v := range req.Variants {
		variants = append(variants, v.toDomain())
	}
	view, err := h.useCase.SaveDraft(ctx, id, exerciseUseCase.SaveDraftInput{
		AdminNote: req.AdminNote,
		Variants:  variants,
		SavedBy:   claims.UserID,
	})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, versionToResponse(view))
}

// discardDraft godoc
// @Summary  Discard the draft version
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/draft [delete]
func (h *Handler) discardDraft(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	if err := h.useCase.DiscardDraft(ctx, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

// publish godoc
// @Summary  Publish the draft version
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response{data=versionResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/publish [post]
func (h *Handler) publish(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.PublishDraft(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, versionToResponse(v))
}

// rollback godoc
// @Summary  Create a new draft from a historical version
// @Tags     exercises
// @Produce  json
// @Param    id         path  string  true  "exercise ID"
// @Param    versionID  path  string  true  "source version ID"
// @Success  200  {object}  response.Response{data=versionResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/versions/{versionID}/rollback [post]
func (h *Handler) rollback(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	versionID, err := uuid.FromString(ctx.Param("versionID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.RollbackToVersion(ctx, id, versionID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, versionToResponse(v))
}

// checkpoint godoc
// @Summary  Save a snapshot of the working copy with an optional label
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string             true   "exercise ID"
// @Param    body  body  checkpointRequest  false  "optional Note (≤ 500 characters), stored as the snapshot Label"
// @Success  200  {object}  response.Response{data=versionResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/checkpoints [post]
func (h *Handler) checkpoint(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	var req checkpointRequest
	// An absent body decodes as io.EOF: the note is optional.
	if err := ctx.ShouldBindJSON(&req); err != nil && !errors.Is(err, io.EOF) {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.CreateCheckpoint(ctx, id, claims.UserID, req.Note)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, versionToResponse(v))
}

// restore godoc
// @Summary  Restore a version, preserving the current draft as a checkpoint
// @Tags     exercises
// @Produce  json
// @Param    id         path  string  true  "exercise ID"
// @Param    versionID  path  string  true  "source version ID"
// @Success  200  {object}  response.Response{data=versionResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/{id}/versions/{versionID}/restore [post]
func (h *Handler) restore(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	versionID, err := uuid.FromString(ctx.Param("versionID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.RestoreToVersion(ctx, id, versionID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, versionToResponse(v))
}

// parseExerciseID extracts and validates the :id path param.
func parseExerciseID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("id"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}
