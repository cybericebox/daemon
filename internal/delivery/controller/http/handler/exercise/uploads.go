package exercise

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/middleware"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// Resumable chunked upload. A request body is limited to 100 MB at the edge, so a file goes up in chunks of at most
// 50 MiB, in order, through the API: start, send the chunks, complete. An interrupted upload reads its status and
// continues from the first chunk the server does not have. The backend assembles the chunks and checks the size and
// the sha256 the client computed.

type startUploadRequest struct {
	Name        string `json:"Name" binding:"required,max=255"`
	ContentType string `json:"ContentType" binding:"max=255"`
	Size        int64  `json:"Size" binding:"required,min=1"`
}

type uploadResponse struct {
	UploadID uuid.UUID `json:"UploadID"`
	Name     string    `json:"Name"`
	Size     int64     `json:"Size"`
	// ChunkSize is the size of every chunk but the last.
	ChunkSize int64 `json:"ChunkSize"`
	// Chunks is how many chunks the file takes; ChunksReceived is where a resumed upload continues (the index of
	// the next chunk to send).
	Chunks         int       `json:"Chunks"`
	ChunksReceived int       `json:"ChunksReceived"`
	ReceivedBytes  int64     `json:"ReceivedBytes"`
	ExpiresAt      time.Time `json:"ExpiresAt"`
}

func uploadToResponse(u mediaModel.Upload) uploadResponse {
	return uploadResponse{
		UploadID: u.ID, Name: u.Name, Size: u.SizeBytes, ChunkSize: u.ChunkBytes, Chunks: u.ChunkCount(),
		ChunksReceived: u.ChunksReceived, ReceivedBytes: u.ReceivedBytes(), ExpiresAt: u.ExpiresAt.UTC(),
	}
}

type completeUploadRequest struct {
	// SHA256 is the hex digest of the whole file, computed by the client.
	SHA256 string `json:"SHA256" binding:"required,len=64,hexadecimal"`
}

// uploadActor authorizes the caller as for the single-request upload.
func (h *Handler) uploadActor(ctx *gin.Context) (exerciseUseCase.Actor, bool) {
	actor, ok := actorFrom(ctx)
	if !ok {
		return actor, false
	}
	if err := h.useCase.AuthorizeFileUpload(ctx, actor); err != nil {
		response.AbortWithError(ctx, err)
		return actor, false
	}
	return actor, true
}

func parseUploadID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("uploadID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}

// startUpload godoc
// @Summary  Start a resumable chunked file upload
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    body  body  startUploadRequest  true  "file name, type and size"
// @Success  200  {object}  response.Response{data=uploadResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/uploads [post]
func (h *Handler) startUpload(ctx *gin.Context) {
	actor, ok := h.uploadActor(ctx)
	if !ok {
		return
	}
	var req startUploadRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	upload, err := h.useCase.StartUpload(ctx, req.Name, req.ContentType, req.Size, actor.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, uploadToResponse(upload))
}

// uploadStatus godoc
// @Summary  Status of a chunked upload: where to continue
// @Tags     exercises
// @Produce  json
// @Param    uploadID  path  string  true  "upload ID"
// @Success  200  {object}  response.Response{data=uploadResponse}
// @Router   /exercises/uploads/{uploadID} [get]
func (h *Handler) uploadStatus(ctx *gin.Context) {
	id, ok := parseUploadID(ctx)
	if !ok {
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	upload, err := h.useCase.UploadStatus(ctx, id, actor.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, uploadToResponse(upload))
}

// putUploadChunk godoc
// @Summary  Send chunk N (0-based) of a chunked upload; the body is the raw chunk
// @Tags     exercises
// @Accept   application/octet-stream
// @Produce  json
// @Param    uploadID  path  string  true  "upload ID"
// @Param    index     path  int     true  "chunk index, the next one in order"
// @Success  200  {object}  response.Response{data=uploadResponse}
// @Failure  409  {object}  response.Response
// @Router   /exercises/uploads/{uploadID}/chunks/{index} [put]
func (h *Handler) putUploadChunk(ctx *gin.Context) {
	id, ok := parseUploadID(ctx)
	if !ok {
		return
	}
	index, err := strconv.Atoi(ctx.Param("index"))
	if err != nil || index < 0 {
		response.AbortWithBadRequest(ctx, errors.New("invalid chunk index"))
		return
	}
	actor, ok := h.uploadActor(ctx)
	if !ok {
		return
	}
	// A chunk is the one body that may be as large as the chunk limit; one byte over it is refused at the boundary.
	middleware.LimitBody(ctx, h.useCase.UploadChunkBytes()+1)
	upload, err := h.useCase.PutChunk(ctx, id, actor.UserID, index, ctx.Request.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			err = mediaModel.ErrUploadChunkSize.Err()
		}
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, uploadToResponse(upload))
}

// completeUpload godoc
// @Summary  Assemble a chunked upload and check its size and hash
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    uploadID  path  string                 true  "upload ID"
// @Param    body      body  completeUploadRequest  true  "sha256 of the whole file"
// @Success  200  {object}  response.Response{data=fileUploadResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/uploads/{uploadID}/complete [post]
func (h *Handler) completeUpload(ctx *gin.Context) {
	id, ok := parseUploadID(ctx)
	if !ok {
		return
	}
	actor, ok := h.uploadActor(ctx)
	if !ok {
		return
	}
	var req completeUploadRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	file, err := h.useCase.CompleteUpload(ctx, id, actor.UserID, req.SHA256)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, fileUploadResponse{FileID: file.ID, Name: file.Name, Size: file.SizeBytes})
}

// abortUpload godoc
// @Summary  Drop a chunked upload and the chunks received so far
// @Tags     exercises
// @Param    uploadID  path  string  true  "upload ID"
// @Success  200  {object}  response.Response
// @Router   /exercises/uploads/{uploadID} [delete]
func (h *Handler) abortUpload(ctx *gin.Context) {
	id, ok := parseUploadID(ctx)
	if !ok {
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	if err := h.useCase.AbortUpload(ctx, id, actor.UserID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}
