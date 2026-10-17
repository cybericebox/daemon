package exercise

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
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
