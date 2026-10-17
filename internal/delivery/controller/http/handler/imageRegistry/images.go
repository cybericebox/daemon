package imageRegistry

import (
	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	"github.com/cybericebox/daemon/internal/model/rbac"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
)

type (
	// unavailableImageResponse is one image that cannot be pulled. Reason: not_configured, not_found,
	// unauthorized, unreachable, needs_event_secret, invalid_reference or credential_conflict.
	unavailableImageResponse struct {
		Image  string `json:"Image"`
		Status string `json:"Status"`
		Reason string `json:"Reason"`
	}

	// eventExerciseImagesResponse is an attachment of the event with its unavailable images.
	eventExerciseImagesResponse struct {
		EventExerciseID string                     `json:"EventExerciseID"`
		ExerciseID      string                     `json:"ExerciseID"`
		ExerciseName    string                     `json:"ExerciseName"`
		Unavailable     []unavailableImageResponse `json:"Unavailable"`
	}
)

func (h *Handler) initEventImages(router *gin.RouterGroup) {
	images := router.Group("events/:id/manage/images", h.prot.RequirePermission(rbac.PermSelf))
	images.GET("availability", h.requireRead, h.eventImagesAvailability)
}

// eventImagesAvailability godoc
// @Summary  The event's tasks whose images cannot be pulled now
// @Description  Checks the device images of every active task of the event (its pinned version, all variants) with the event's own registry credentials. Lists only the tasks with unavailable images; an empty list means every image can be pulled. Positive results are cached for 60 s.
// @Tags     event-registry
// @Produce  json
// @Param    id  path  string  true  "Event ID"
// @Success  200  {object}  response.Response{data=[]eventExerciseImagesResponse}
// @Router   /events/{id}/manage/images/availability [get]
func (h *Handler) eventImagesAvailability(ctx *gin.Context) {
	eventID, ok := parseUUID(ctx, "id")
	if !ok {
		return
	}
	rows, err := h.useCase.EventImagesAvailability(ctx.Request.Context(), eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toEventImages(rows))
}

func toEventImages(rows []imageRegistryUseCase.EventExerciseImages) []eventExerciseImagesResponse {
	out := make([]eventExerciseImagesResponse, 0, len(rows))
	for _, r := range rows {
		item := eventExerciseImagesResponse{
			EventExerciseID: r.EventExerciseID.String(), ExerciseID: r.ExerciseID.String(), ExerciseName: r.ExerciseName,
			Unavailable: make([]unavailableImageResponse, 0, len(r.Unavailable)),
		}
		for _, a := range r.Unavailable {
			item.Unavailable = append(item.Unavailable, unavailableImageResponse{Image: a.Image, Status: string(a.Status), Reason: string(a.Reason)})
		}
		out = append(out, item)
	}
	return out
}
