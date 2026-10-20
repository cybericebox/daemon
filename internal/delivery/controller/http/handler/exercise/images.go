package exercise

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

type (
	checkImagesRequest struct {
		// Images are the device image references of the editor (at most 200, each at most 512 characters).
		Images []string `json:"Images" binding:"required,max=200,dive,required,max=512"`
	}

	// imageAvailabilityResponse is the check result for one image: Status "available" or "unavailable";
	// Reason (unavailable only): not_configured, not_found, unauthorized, unreachable,
	// needs_event_secret, invalid_reference or credential_conflict.
	imageAvailabilityResponse struct {
		Image  string `json:"Image"`
		Status string `json:"Status"`
		Reason string `json:"Reason,omitempty"`
	}
)

func toImageAvailability(av []imageRegistryModel.Availability) []imageAvailabilityResponse {
	out := make([]imageAvailabilityResponse, 0, len(av))
	for _, a := range av {
		out = append(out, imageAvailabilityResponse{Image: a.Image, Status: string(a.Status), Reason: string(a.Reason)})
	}
	return out
}

// checkImages godoc
// @Summary  Check that the device images of the exercise can be pulled
// @Description  Same access as saving the draft. Platform images (platform:<name>@sha256:…) are checked in the platform registry, other images anonymously; an event-owned exercise also uses its event's registry credentials. One result per distinct image, in the order sent. Positive results are cached for 60 s.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string              true  "exercise ID"
// @Param    body  body  checkImagesRequest  true  "images"
// @Success  200  {object}  response.Response{data=[]imageAvailabilityResponse}
// @Failure  400  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Router   /exercises/{id}/images/check [post]
func (h *Handler) checkImages(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	var req checkImagesRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	scope, err := h.useCase.ExerciseImageScope(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	av, err := h.useCase.CheckImageAvailability(ctx, scope, req.Images)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toImageAvailability(av))
}

// Image upload through the site (a `docker save` archive read in the browser): which blobs the platform registry
// lacks → each missing blob through the chunked upload (Purpose "image-blob", never completed) → a push of the
// upload queued (202 pushing; the push runs in the background) → its state polled until present or failed → the
// image created from its blobs. Same access as saving the draft. The registry not configured or failing: 409 with
// the upload refusal (ErrRegistryNotConfigured / ErrRegistryUnavailable; the latter names the reason in
// Status.Context for admins).

type (
	missingBlobsRequest struct {
		// Digests are the image's blob digests (config and layers), sha256:<64 lowercase hex>.
		Digests []string `json:"Digests" binding:"required,min=1,max=501,dive,required,max=80"`
	}

	missingBlobsResponse struct {
		// Missing are the digests the registry does not have, in the order sent; never null.
		Missing []string `json:"Missing"`
	}

	pushBlobRequest struct {
		// UploadID is a chunked upload of the caller with Purpose "image-blob" and every chunk received.
		UploadID uuid.UUID `json:"UploadID" binding:"required"`
		Digest   string    `json:"Digest" binding:"required,max=80"`
		Size     int64     `json:"Size" binding:"required,min=1"`
	}

	// blobStatusResponse: State "present" (the registry has it), "pushing" (queued or running), "failed" (Reason:
	// unauthorized, unreachable, rate_limited, not_found, not_configured, digest_mismatch, upload_missing) or
	// "missing" (not in the registry, no push known).
	blobStatusResponse struct {
		State  string `json:"State"`
		Reason string `json:"Reason,omitempty"`
	}

	descriptorRequest struct {
		MediaType string `json:"MediaType" binding:"required,max=255"`
		Digest    string `json:"Digest" binding:"required,max=80"`
		Size      int64  `json:"Size" binding:"required,min=1"`
	}

	createImageRequest struct {
		// Name becomes the platform image name (lowercased, other characters replaced).
		Name   string              `json:"Name" binding:"required,max=255"`
		Config descriptorRequest   `json:"Config" binding:"required"`
		Layers []descriptorRequest `json:"Layers" binding:"required,min=1,max=500,dive"`
	}

	createImageResponse struct {
		// Reference is platform:<name>@sha256:<manifest digest>, the device image to save.
		Reference string `json:"Reference"`
	}
)

func (d descriptorRequest) toDescriptor() imageRegistryUseCase.Descriptor {
	return imageRegistryUseCase.Descriptor{MediaType: d.MediaType, Digest: d.Digest, Size: d.Size}
}

// missingImageBlobs godoc
// @Summary  Which image blobs the platform registry does not have yet
// @Description  Same access as saving the draft. Upload only the missing blobs (chunked upload, Purpose "image-blob"), then push each one.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string               true  "exercise ID"
// @Param    body  body  missingBlobsRequest  true  "blob digests"
// @Success  200  {object}  response.Response{data=missingBlobsResponse}
// @Failure  400  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /exercises/{id}/images/blobs/missing [post]
func (h *Handler) missingImageBlobs(ctx *gin.Context) {
	var req missingBlobsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	missing, err := h.useCase.MissingBlobs(ctx, req.Digests)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if missing == nil {
		missing = []string{}
	}
	response.AbortWithData(ctx, missingBlobsResponse{Missing: missing})
}

func toBlobStatus(s imageRegistryUseCase.BlobStatus) blobStatusResponse {
	return blobStatusResponse{State: string(s.State), Reason: string(s.Reason)}
}

// pushImageBlob godoc
// @Summary  Queue the push of an uploaded image blob into the platform registry
// @Description  Same access as saving the draft. The upload must be the caller's chunked upload with Purpose "image-blob", every chunk received and Size bytes. 200 {State:"present"} when the registry already has the blob; 202 {State:"pushing"} when the push is queued (poll GET …/images/blobs/{digest}).
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string           true  "exercise ID"
// @Param    body  body  pushBlobRequest  true  "upload and blob"
// @Success  200  {object}  response.Response{data=blobStatusResponse}
// @Success  202  {object}  response.Response{data=blobStatusResponse}
// @Failure  400  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Failure  404  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /exercises/{id}/images/blobs [post]
func (h *Handler) pushImageBlob(ctx *gin.Context) {
	var req pushBlobRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	st, err := h.useCase.PushStagedBlob(ctx, imageRegistryUseCase.PushBlobInput{UploadID: req.UploadID, Digest: req.Digest, Size: req.Size})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	if st.State == imageRegistryModel.BlobPushing {
		response.AbortWithDataStatus(ctx, http.StatusAccepted, toBlobStatus(st))
		return
	}
	response.AbortWithData(ctx, toBlobStatus(st))
}

// imageBlobStatus godoc
// @Summary  Where one image blob stands: present, pushing, failed or missing
// @Description  Same access as saving the draft. present: the platform registry has it; pushing: a push is queued or running; failed: the last push failed (Reason), ask for the push again or upload again; missing: upload it.
// @Tags     exercises
// @Produce  json
// @Param    id      path  string  true  "exercise ID"
// @Param    digest  path  string  true  "blob digest, sha256:<64 hex>"
// @Success  200  {object}  response.Response{data=blobStatusResponse}
// @Failure  400  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /exercises/{id}/images/blobs/{digest} [get]
func (h *Handler) imageBlobStatus(ctx *gin.Context) {
	st, err := h.useCase.ImageBlobStatus(ctx, ctx.Param("digest"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toBlobStatus(st))
}

// createImage godoc
// @Summary  Create a platform image from blobs already in the registry
// @Description  Same access as saving the draft. Builds the manifest (Docker schema 2 when every type is Docker's, otherwise OCI), pushes it under the tag sha256-<the whole digest hex> (the digest alone: the same content is the same tag whatever its name) and returns the platform reference for the device.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string              true  "exercise ID"
// @Param    body  body  createImageRequest  true  "image name, config and layers"
// @Success  200  {object}  response.Response{data=createImageResponse}
// @Failure  400  {object}  response.Response
// @Failure  403  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /exercises/{id}/images [post]
func (h *Handler) createImage(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	var req createImageRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	in := imageRegistryUseCase.CreateImageInput{Name: req.Name, Config: req.Config.toDescriptor(), Layers: make([]imageRegistryUseCase.Descriptor, 0, len(req.Layers))}
	for _, l := range req.Layers {
		in.Layers = append(in.Layers, l.toDescriptor())
	}
	res, err := h.useCase.CreateImage(ctx, id, in)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, createImageResponse{Reference: res.Reference})
}
