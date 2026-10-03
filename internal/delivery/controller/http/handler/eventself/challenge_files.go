package eventself

import (
	"mime"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/download"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// downloadChallengeAttachment godoc
// @Summary Download an attachment from the participant's published challenge
// @Tags events-self
// @Produce application/octet-stream
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param fileID path string true "attachment file ID"
// @Success 200 {file} file
// @Router /events/{id}/teams/challenges/{challengeID}/files/{fileID} [get]
func (h *Handler) downloadChallengeAttachment(ctx *gin.Context) {
	eventID, ok := eventIDFromPath(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	fileID, err := uuid.FromString(ctx.Param("fileID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := claimsFromContext(ctx)
	if !ok {
		return
	}
	reader, file, err := h.useCase.StreamOwnChallengeAttachment(ctx, eventID, claims.UserID, challengeID, fileID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	defer func() { _ = reader.Close() }()
	download.ExtendDeadline(ctx)
	ctx.Header("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
	ctx.Header("X-Content-Type-Options", "nosniff")
	ctx.Header("Content-Security-Policy", "default-src 'none'; sandbox")
	ctx.Header("Cache-Control", "private, no-store")
	contentType := file.ContentType
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	download.File(ctx, reader, file.SizeBytes, contentType)
}
