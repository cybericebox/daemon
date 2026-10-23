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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/rbac"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
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
		// Policy is the platform's device resources settings (presets, frame, ceiling).
		Policy() resourcesModel.Policy
		FlagPolicy() exerciseUseCase.FlagPolicy
		// catalog
		CreateExercise(ctx context.Context, in exerciseUseCase.CreateExerciseInput) (exerciseUseCase.ExerciseView, error)
		GetExercise(ctx context.Context, id uuid.UUID) (exerciseUseCase.ExerciseView, error)
		ListExercises(ctx context.Context, f exerciseUseCase.ExercisesFilter) (exerciseUseCase.ExercisesListResult, error)
		ListExerciseTags(ctx context.Context, actor exerciseUseCase.Actor, prefix string, limit int) ([]exerciseUseCase.ExerciseTagSuggestion, error)
		ListExerciseAuthors(ctx context.Context, actor exerciseUseCase.Actor, search string, ids []uuid.UUID, limit int) ([]exerciseUseCase.ExerciseAuthorSuggestion, error)
		UpdateExerciseIdentity(ctx context.Context, in exerciseUseCase.UpdateExerciseInput) (exerciseUseCase.ExerciseView, error)
		GetExerciseUsage(ctx context.Context, id uuid.UUID) (exerciseUseCase.ExerciseUsage, error)
		GetVariantStats(ctx context.Context, exerciseID uuid.UUID) ([]exerciseUseCase.VersionVariantStats, error)
		DeleteExercise(ctx context.Context, id uuid.UUID) error
		ArchiveExercise(ctx context.Context, id, by uuid.UUID) (exerciseUseCase.ExerciseView, error)
		UnarchiveExercise(ctx context.Context, id, by uuid.UUID) (exerciseUseCase.ExerciseView, error)
		// bulk actions of the catalog table: per task by the rule of the single-task route
		BulkSetArchived(ctx context.Context, actor exerciseUseCase.Actor, ids []uuid.UUID, archived bool) ([]exerciseUseCase.BulkResult, error)
		BulkSetAccess(ctx context.Context, actor exerciseUseCase.Actor, ids []uuid.UUID, in exerciseUseCase.SetAccessInput) ([]exerciseUseCase.BulkResult, error)
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
		DeployVariantTest(ctx context.Context, userID, versionID, variantID uuid.UUID, length time.Duration) (exerciseModel.DeployHandle, error)
		TestRunLengths(ctx context.Context) (allowed []int, defaultMinutes int)
		DeployTestStatus(ctx context.Context, userID, deployID uuid.UUID) (exerciseModel.LabDeployStatus, error)
		DestroyDeployTest(ctx context.Context, userID, deployID uuid.UUID) error
		ResetTestDeployDevice(ctx context.Context, userID, deployID uuid.UUID, device string) error
		RescueTestDeployDevice(ctx context.Context, userID, deployID uuid.UUID, device string, enable bool) error
		ListTestDeploys(ctx context.Context, userID, exerciseID uuid.UUID) ([]exerciseModel.TestDeploy, error)
		ExtendTestDeploy(ctx context.Context, userID, deployID uuid.UUID) (exerciseModel.TestDeploy, error)
		CheckTestFlag(ctx context.Context, userID, deployID, taskID uuid.UUID, flag string) (bool, error)
		OpenTestDeployLink(ctx context.Context, userID, deployID uuid.UUID, device string, port int32) (labaccess.Link, error)
		PreviewArchiveImport(context.Context, exerciseUseCase.Actor, exerciseUseCase.PreviewArchiveInput) (exerciseUseCase.ArchiveImportPreview, error)
		CommitArchiveImport(context.Context, exerciseUseCase.Actor, exerciseUseCase.CommitArchiveInput) (exerciseUseCase.ArchiveImportStart, error)
		ImportRawArchive(context.Context, exerciseUseCase.Actor, exerciseUseCase.ImportExerciseInput) (exerciseUseCase.ArchiveImportStart, error)
		ListImportTargets(context.Context, exerciseUseCase.Actor, string, uuid.UUID, int) (pagination.CursorPage[exerciseUseCase.ImportTargetExercise], error)
		// archive portability
		// ArchiveLimits are the archive limits in force: read once per import or export and passed down.
		ArchiveLimits(ctx context.Context) exerciseModel.ArchiveLimits
		ExportExerciseArchive(ctx context.Context, id uuid.UUID, opts exerciseUseCase.ExportOptions, lim exerciseModel.ArchiveLimits) ([]byte, error)
		// background import of archives naming device image files
		QueueArchiveImport(ctx context.Context, in exerciseUseCase.QueueImportInput) (uuid.UUID, error)
		ArchiveImportJob(ctx context.Context, id, user uuid.UUID) (exerciseUseCase.ImportJobView, error)
		RunningArchiveImports(ctx context.Context, user uuid.UUID) ([]exerciseUseCase.ImportJobView, error)
		// background export of tasks «з образами»
		StartArchiveExport(ctx context.Context, in exerciseUseCase.StartExportInput) (uuid.UUID, error)
		ArchiveExportJob(ctx context.Context, id, user uuid.UUID) (exerciseUseCase.ExportJobView, error)
		// files
		UploadFile(ctx context.Context, name, contentType string, r io.Reader, createdBy uuid.UUID) (mediaModel.File, error)
		GetFile(ctx context.Context, id uuid.UUID) (mediaModel.File, error)
		StreamFile(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error)
		// ReadFileChunk opens chunk index of a stored file (chunks are UploadChunkBytes long) and reports its length.
		ReadFileChunk(ctx context.Context, id uuid.UUID, index int) (io.ReadCloser, mediaModel.File, int64, error)
		// LookupExerciseFile answers whether the task already stores a content (asked before an attachment upload).
		LookupExerciseFile(ctx context.Context, exerciseID uuid.UUID, sha256 string) (exerciseUseCase.FileLookup, error)
		// MaxUploadBytes is the same cap UploadFile enforces internally; the
		// handler uses it to cap the request body at the HTTP boundary too
		// (see files.go), so an oversized upload is rejected before the
		// multipart body is parsed, not after.
		MaxUploadBytes() int64
		// MaxSingleUploadBytes is the most one single-request upload may carry (one chunk at most).
		MaxSingleUploadBytes() int64
		// resumable chunked upload (a request body is limited at the edge)
		UploadChunkBytes() int64
		StartUpload(ctx context.Context, name, contentType string, size int64, createdBy uuid.UUID, purpose mediaModel.UploadPurpose) (mediaModel.Upload, error)
		UploadStatus(ctx context.Context, id, owner uuid.UUID) (mediaModel.Upload, error)
		PutChunk(ctx context.Context, id, owner uuid.UUID, index int, r io.Reader) (mediaModel.Upload, error)
		CompleteUpload(ctx context.Context, id, owner uuid.UUID, wantSHA256 string) (mediaModel.File, error)
		AbortUpload(ctx context.Context, id, owner uuid.UUID) error
		// W4: data-dependent authorization and ownership
		AuthorizeExercise(ctx context.Context, actor exerciseUseCase.Actor, id uuid.UUID, action exerciseUseCase.Action) (exerciseUseCase.Access, error)
		AuthorizeFileUpload(ctx context.Context, actor exerciseUseCase.Actor) error
		// RequireAuthor: exercises.write or an owner/manager of some event (import routes not about one target).
		RequireAuthor(ctx context.Context, actor exerciseUseCase.Actor) error
		// AuthorizeImport: the catalog (nil) needs exercises.write; an event needs that or managing it.
		AuthorizeImport(ctx context.Context, actor exerciseUseCase.Actor, ownerEventID *uuid.UUID) error
		// AuthorizeExport: exercises.export, or every task belongs to an event the caller manages.
		AuthorizeExport(ctx context.Context, actor exerciseUseCase.Actor, ids []uuid.UUID) error
		AuthorizeFileDownload(ctx context.Context, actor exerciseUseCase.Actor, file mediaModel.File) error
		AuthorizeTestDeploy(ctx context.Context, actor exerciseUseCase.Actor, exerciseID, versionID, variantID uuid.UUID) error
		RedactFlags(ctx context.Context, actor exerciseUseCase.Actor, v exerciseUseCase.VersionView) (exerciseUseCase.VersionView, error)
		GetAccessSummary(ctx context.Context, actor exerciseUseCase.Actor) (exerciseUseCase.AccessSummary, error)
		CreateExerciseFor(ctx context.Context, actor exerciseUseCase.Actor, in exerciseUseCase.CreateExerciseInput) (exerciseUseCase.ExerciseView, error)
		GetExerciseFor(ctx context.Context, actor exerciseUseCase.Actor, id uuid.UUID) (exerciseUseCase.ExerciseView, error)
		ListExercisesFor(ctx context.Context, actor exerciseUseCase.Actor, f exerciseUseCase.ExercisesFilter) (exerciseUseCase.ExercisesListResult, error)
		SetExerciseAccess(ctx context.Context, actor exerciseUseCase.Actor, id uuid.UUID, in exerciseUseCase.SetAccessInput) (exerciseUseCase.ExerciseView, error)
		RequestElevation(ctx context.Context, actor exerciseUseCase.Actor, exerciseID uuid.UUID, reason string) (exerciseUseCase.ElevationView, error)
		LatestElevation(ctx context.Context, exerciseID uuid.UUID) (*exerciseUseCase.ElevationView, error)
		GetElevation(ctx context.Context, id uuid.UUID) (exerciseUseCase.ElevationView, error)
		ListElevations(ctx context.Context, status string) ([]exerciseUseCase.ElevationView, error)
		DecideElevation(ctx context.Context, actor exerciseUseCase.Actor, id uuid.UUID, in exerciseUseCase.DecideElevationInput) (exerciseUseCase.ElevationView, error)
		ProposeExercise(ctx context.Context, actor exerciseUseCase.Actor, id uuid.UUID, note string) (exerciseUseCase.ProposalView, error)
		ListProposals(ctx context.Context, status string) ([]exerciseUseCase.ProposalView, error)
		ApproveProposal(ctx context.Context, actor exerciseUseCase.Actor, proposalID uuid.UUID, in exerciseUseCase.ApproveProposalInput) (exerciseUseCase.ProposalView, error)
		RejectProposal(ctx context.Context, actor exerciseUseCase.Actor, proposalID uuid.UUID, note string) (exerciseUseCase.ProposalView, error)
		// image availability (the editor's check); scope is the owner event of an event-owned exercise
		ExerciseImageScope(ctx context.Context, exerciseID uuid.UUID) (imageRegistryUseCase.Scope, error)
		CheckImageAvailability(ctx context.Context, scope imageRegistryUseCase.Scope, images []string) ([]imageRegistryModel.Availability, error)
		// image upload through the site (the platform registry)
		RequireImageUploads(ctx context.Context) error
		MissingBlobs(ctx context.Context, digests []string) ([]string, error)
		PushStagedBlob(ctx context.Context, in imageRegistryUseCase.PushBlobInput) (imageRegistryUseCase.BlobStatus, error)
		ImageBlobStatus(ctx context.Context, digest string) (imageRegistryUseCase.BlobStatus, error)
		CreateImage(ctx context.Context, exerciseID uuid.UUID, in imageRegistryUseCase.CreateImageInput) (imageRegistryUseCase.CreateImageResult, error)
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
		// the catalog exercises available to them. Import and export follow
		// the same split: the catalog and any task for admins (exercises.write /
		// exercises.export), an event's own tasks for its owners/managers.
		self := h.prot.RequirePermission(rbac.PermSelf)
		// long: whole-file server-side work (assemble, promote, import) outlives the server WriteTimeout
		long := download.LongOperation()
		ex.GET("", self, h.list)
		ex.GET("tags", self, h.listTags)
		ex.GET("authors", self, h.listAuthors)
		ex.GET("access", self, h.access)
		ex.POST("", self, h.create)

		// files routes are static segments ("files", "files/:fileID") and must
		// be registered before the ":id" param routes below — gin's router
		// clashes registering a static child under a path where a differently
		// named wildcard (":id") was already claimed at that position.
		ex.POST("files", self, h.uploadFile)
		ex.GET("files/:fileID", self, h.downloadFile)
		// chunked resumable download of a stored file (the export): the meta says how many chunks there are
		ex.GET("files/:fileID/meta", self, h.fileMeta)
		ex.GET("files/:fileID/chunks/:index", self, h.fileChunk)
		// resumable chunked upload: chunks of at most 50 MiB through the API, in order
		ex.POST("uploads", self, h.startUpload)
		ex.GET("uploads/:uploadID", self, h.uploadStatus)
		ex.PUT("uploads/:uploadID/chunks/:index", self, middleware.RawBody, h.putUploadChunk)
		ex.POST("uploads/:uploadID/complete", long, self, h.completeUpload)
		ex.DELETE("uploads/:uploadID", self, h.abortUpload)
		// a ZIP archive that went up in chunks; the target (catalog or event) is authorized in the handler
		ex.POST("import/preview", long, self, h.previewArchive)
		ex.GET("import/targets", self, h.importTargets)
		ex.POST("import/uploaded", long, self, h.importUploadedArchive)
		// a task archive upload (purpose exercise-archive), read where its chunks lie; with images a background job
		ex.POST("import/upload", long, self, h.importArchiveUpload)
		// the background import's state, for the user who started it only (the use case scopes it to them)
		ex.GET("import/jobs", self, h.runningImportJobs)
		ex.GET("import/jobs/:jobID", self, h.importJob)
		// the built-in example archive: for whoever may import somewhere
		ex.GET("import/example", self, h.importExample)
		ex.GET("import/example/document", self, h.importExampleDocument)
		// every task of the selection is authorized in the handler (AuthorizeExport)
		ex.POST("export", self, h.export)
		// the background export's state, for the user who started it only (the use case scopes it to them)
		ex.GET("export/jobs/:jobID", self, h.exportJob)
		ex.POST("import", long, self, h.importArchive)
		ex.GET("capabilities", self, h.capabilities)
		ex.GET("flag-policy", self, h.flagPolicy)

		// catalog proposals (admin review)
		ex.GET("proposals", h.prot.RequirePermission(rbac.PermExercisesRead), h.listProposals)
		ex.POST("proposals/:proposalID/approve", h.prot.RequirePermission(rbac.PermExercisesPublish), h.approveProposal)
		ex.POST("proposals/:proposalID/reject", h.prot.RequirePermission(rbac.PermExercisesPublish), h.rejectProposal)

		// resource elevations: a platform admin lists and decides the requests to take devices above the frame
		ex.GET("elevations", h.prot.RequirePermission(rbac.PermExercisesElevationsRead), h.listElevations)
		ex.GET("elevations/:elevationID", h.prot.RequirePermission(rbac.PermExercisesElevationsRead), h.getElevation)
		ex.POST("elevations/:elevationID/approve", h.prot.RequirePermission(rbac.PermExercisesElevationsWrite), h.approveElevation)
		ex.POST("elevations/:elevationID/reject", h.prot.RequirePermission(rbac.PermExercisesElevationsWrite), h.rejectElevation)

		// deploy routes: "deploys" is a static segment sibling to ":id" and, like
		// "files", must be registered before the ":id" param routes. Status,
		// extend and destroy are owner-scoped in the use case; creating one
		// is authorized per exercise (h.deploy).
		ex.GET("deploys", self, h.listDeploys)
		ex.GET("test-runs/lengths", self, h.testRunLengths)
		ex.GET("deploys/:deployID", self, h.deployStatus)
		ex.POST("deploys/:deployID/extend", self, h.extendDeploy)
		ex.POST("deploys/:deployID/link", self, h.deployLink)
		ex.POST("deploys/:deployID/check", self, h.checkDeploy)
		ex.POST("deploys/:deployID/devices/:device/reset", self, h.resetDeployDevice)
		ex.POST("deploys/:deployID/devices/:device/rescue", self, h.rescueDeployDevice)
		ex.DELETE("deploys/:deployID", self, h.destroyDeploy)

		// bulk actions of the catalog table ("bulk" is a static segment, registered before ":id"): every task is
		// authorized as by its single-task route (archive: the exercise policy per task; access: exercises.write).
		ex.POST("bulk/archive", self, h.bulkArchive)
		ex.POST("bulk/unarchive", self, h.bulkUnarchive)
		ex.PUT("bulk/access", h.prot.RequirePermission(rbac.PermExercisesWrite), h.bulkSetAccess)

		ex.GET(":id", self, h.authorize(exerciseUseCase.ActionReadPublished), h.get)
		ex.GET(":id/usage", self, h.authorize(exerciseUseCase.ActionRead), h.usage)
		ex.GET(":id/variant-stats", self, h.authorize(exerciseUseCase.ActionRead), h.variantStats)
		ex.PATCH(":id", self, h.authorize(exerciseUseCase.ActionWrite), h.update)
		ex.DELETE(":id", self, h.authorize(exerciseUseCase.ActionDelete), h.delete)
		ex.POST(":id/archive", self, h.authorize(exerciseUseCase.ActionWrite), h.archive)
		ex.POST(":id/unarchive", self, h.authorize(exerciseUseCase.ActionWrite), h.unarchive)
		ex.PUT(":id/access", h.prot.RequirePermission(rbac.PermExercisesWrite), h.setAccess)
		ex.POST(":id/proposals", self, h.authorize(exerciseUseCase.ActionPublish), h.propose)
		ex.GET(":id/elevation", self, h.authorize(exerciseUseCase.ActionRead), h.exerciseElevation)
		ex.POST(":id/elevation", self, h.authorize(exerciseUseCase.ActionWrite), h.requestElevation)
		ex.POST(":id/files/lookup", self, h.authorize(exerciseUseCase.ActionWrite), h.lookupFile)

		ex.GET(":id/versions", self, h.authorize(exerciseUseCase.ActionRead), h.listVersions)
		ex.GET(":id/versions/:versionID", self, h.authorize(exerciseUseCase.ActionReadPublished), h.getVersion)
		ex.POST(":id/versions/:versionID/rollback", self, h.authorize(exerciseUseCase.ActionPublish), h.rollback)
		ex.POST(":id/versions/:versionID/restore", self, h.authorize(exerciseUseCase.ActionWrite), h.restore)
		ex.POST(":id/checkpoints", self, h.authorize(exerciseUseCase.ActionWrite), h.checkpoint)
		ex.POST(":id/versions/:versionID/variants/:variantID/deploy", self, h.deploy)

		ex.GET(":id/draft", self, h.authorize(exerciseUseCase.ActionRead), h.getDraft)
		ex.PUT(":id/draft", self, h.authorize(exerciseUseCase.ActionWrite), h.saveDraft)
		// image availability: the same access as saving the draft
		ex.POST(":id/images/check", self, h.authorize(exerciseUseCase.ActionWrite), h.checkImages)
		// image upload through the site: the same access as saving the draft
		ex.POST(":id/images/blobs/missing", self, h.authorize(exerciseUseCase.ActionWrite), h.missingImageBlobs)
		ex.POST(":id/images/blobs", self, h.authorize(exerciseUseCase.ActionWrite), h.pushImageBlob)
		ex.GET(":id/images/blobs/:digest", self, h.authorize(exerciseUseCase.ActionWrite), h.imageBlobStatus)
		ex.POST(":id/images", self, h.authorize(exerciseUseCase.ActionWrite), h.createImage)
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

// listAuthors godoc
// @Summary  Authors of the exercises the caller may read, by use (the author filter); names only, never emails
// @Tags     exercises
// @Produce  json
// @Param    search  query  string  false  "name substring (case-insensitive); empty returns the most frequent authors"
// @Param    id      query  []string  false  "only these authors (repeated or comma-separated, max 100): the names of the chosen ones"  collectionFormat(multi)
// @Param    limit   query  int     false  "max authors (default 50, max 200)"
// @Success  200  {object}  response.Response{data=[]exerciseUseCase.ExerciseAuthorSuggestion}
// @Failure  400  {object}  response.Response
// @Router   /exercises/authors [get]
func (h *Handler) listAuthors(ctx *gin.Context) {
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
	rawIDs := queryList(ctx, "id")
	if len(rawIDs) > maxAuthorFilter {
		response.AbortWithBadRequest(ctx, fmt.Errorf("at most %d authors", maxAuthorFilter))
		return
	}
	ids := make([]uuid.UUID, 0, len(rawIDs))
	for _, raw := range rawIDs {
		id, err := uuid.FromString(raw)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		ids = append(ids, id)
	}
	items, err := h.useCase.ListExerciseAuthors(ctx, actor, ctx.Query("search"), ids, limit)
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
		// Resources is the platform's device resources settings: the presets, the default, the frame a device may
		// use without approval and the ceiling an approved elevation may reach.
		Resources resourceSettingsResponse `json:"Resources"`
	}{
		Laboratories: h.useCase.InfrastructureAvailable(), MaxActiveTestDeploys: h.useCase.MaxActiveTestDeploys(),
		DevicePersistence: h.useCase.DevicePersistenceAllowed(), Resources: toResourceSettings(h.useCase.Policy()),
	})
}

// list godoc
// @Summary  List catalog exercises (cursor or offset page)
// @Tags     exercises
// @Produce  json
// @Param    search    query  string    false  "name/description substring"
// @Param    tags      query  []string  false  "filter by tags (any match)"  collectionFormat(multi)
// @Param    withTag   query  []string  false  "tasks having every one of these tags (offset pages only)"  collectionFormat(multi)
// @Param    withoutTag  query  []string  false  "tasks having none of these tags (offset pages only)"  collectionFormat(multi)
// @Param    author    query  []string  false  "author (creator) ids, repeated or comma-separated, max 100: any of them (offset pages only)"  collectionFormat(multi)
// @Param    difficultyMin  query  int  false  "lowest rounded average difficulty level, 1 elementary … 6 insane; tasks without one are excluded (offset pages only)"
// @Param    difficultyMax  query  int  false  "highest rounded average difficulty level, 1..6 (offset pages only)"
// @Param    cursor    query  string    false  "id of the last row of the previous page"
// @Param    page      query  int       false  "1-based page; selects offset pagination"
// @Param    pageSize  query  int       false  "page size"
// @Param    status    query  []string  false  "published, changed, draft_only, archived or none, repeated or comma-separated: any of them; archived with others lists both (offset pages only)"  collectionFormat(multi)
// @Param    sortBy    query  string    false  "name, tags, status, author, difficulty, subtasks, variants or updated (offset pages only)"
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
			Search: ctx.Query("search"), Tags: ctx.QueryArray("tags"), Archived: ctx.Query("archived"),
			Page: page.Page, PageSize: page.PageSize, SortBy: ctx.Query("sortBy"), SortDir: ctx.Query("sortDir"),
		}
		if !applyVisibilityQuery(ctx, &filter) || !applyTableQuery(ctx, &filter) {
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
	audit.SetTarget(ctx, "exercise:"+v.ID.String())
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

// variantStats godoc
// @Summary  Grow-only issue and solve counters of the exercise's variants, per version (newest first)
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response{data=[]versionVariantStatsResponse}
// @Failure  400  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Router   /exercises/{id}/variant-stats [get]
func (h *Handler) variantStats(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	stats, err := h.useCase.GetVariantStats(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, variantStatsToResponse(stats))
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
	// The internal notes are for full access only (admins, the owner event's owners/managers).
	notes := accessFrom(ctx).Flags
	out := make([]versionListItemResponse, 0, len(items))
	for _, v := range items {
		adminNote := v.AdminNote
		if !notes {
			adminNote = ""
		}
		out = append(out, versionListItemResponse{
			ID:           v.ID,
			Status:       v.Status,
			AdminNote:    adminNote,
			Label:        v.Label,
			VariantCount: v.VariantCount,
			CreatedAt:    v.CreatedAt,
			CreatedBy:    v.CreatedBy,
			AuthorName:   v.AuthorName,
			PublishedAt:  v.PublishedAt,
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
	// An event viewer reads their event's tasks without the flags.
	if access := accessFrom(ctx); access.Full && !access.Flags {
		v = exerciseUseCase.WithoutFlags(v)
	}
	// The internal notes reach only full access: not an event viewer, not a published-only reader.
	if !accessFrom(ctx).Flags {
		v = exerciseUseCase.WithoutNotes(v)
	}
	// The fixed flags of a catalog exercise reach only the managers of an event it is attached to.
	if !accessFrom(ctx).Full {
		actor, ok := actorFrom(ctx)
		if !ok {
			return
		}
		if v, err = h.useCase.RedactFlags(ctx, actor, v); err != nil {
			response.AbortWithError(ctx, err)
			return
		}
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
	// An event viewer reads the working copy without the flags and the internal notes.
	if !accessFrom(ctx).Flags {
		v = exerciseUseCase.WithoutNotes(exerciseUseCase.WithoutFlags(v))
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
	copyOf := make([]uuid.NullUUID, 0, len(req.Variants))
	for _, v := range req.Variants {
		variants = append(variants, v.toDomain())
		copyOf = append(copyOf, v.copyOf())
	}
	view, err := h.useCase.SaveDraft(ctx, id, exerciseUseCase.SaveDraftInput{
		AdminNote: req.AdminNote,
		Variants:  variants,
		CopyOf:    copyOf,
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
