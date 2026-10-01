package event

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/labview"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

type (
	rescueDeviceRequest struct {
		// Enable true starts the device in rescue mode (a shell from its latest snapshot), false returns it to normal.
		Enable bool `json:"Enable"`
	}
)

// getStandDetail godoc
// @Summary Live state of one team stand: launch queue, devices with snapshots, image warnings
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID (the moderators team ID from the stand list works too)"
// @Success 200 {object} response.Response{data=labview.StandDetailResponse}
// @Router /events/{id}/manage/labs/{teamID}/detail [get]
func (h *Handler) getStandDetail(ctx *gin.Context) {
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetTeamStandDetail(ctx, eventID, teamID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, labview.StandDetail(v))
}

// resetStandDevice godoc
// @Summary Reset one device of a team Lab to its base image (organizers only)
// @Description Discards the device's snapshots and restarts it from the image. 409 when the device keeps no state (DetailCode 71405), 404 when the Lab or device is not there, 503 (71407) while the device is still being restarted.
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param challengeID path string true "event challenge ID"
// @Param device path string true "device name"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/labs/{teamID}/challenges/{challengeID}/devices/{device}/reset [post]
func (h *Handler) resetStandDevice(ctx *gin.Context) {
	eventID, teamID, challengeID, device, ok := parseStandDevice(ctx)
	if !ok {
		return
	}
	if err := h.useCase.ResetStandDevice(ctx, eventID, teamID, challengeID, device); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}

// rescueStandDevice godoc
// @Summary Start one device of a team Lab in rescue mode, or back to normal (organizers only)
// @Description Enable=true starts the device from its latest snapshot with a shell instead of the image entrypoint, to repair a configuration that makes the service crash; Enable=false returns it to the normal start. Errors as for reset.
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Param challengeID path string true "event challenge ID"
// @Param device path string true "device name"
// @Param body body rescueDeviceRequest true "rescue switch"
// @Success 200 {object} response.Response
// @Router /events/{id}/manage/labs/{teamID}/challenges/{challengeID}/devices/{device}/rescue [post]
func (h *Handler) rescueStandDevice(ctx *gin.Context) {
	eventID, teamID, challengeID, device, ok := parseStandDevice(ctx)
	if !ok {
		return
	}
	var req rescueDeviceRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := h.useCase.RescueStandDevice(ctx, eventID, teamID, challengeID, device, req.Enable); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}

func parseStandDevice(ctx *gin.Context) (eventID, teamID, challengeID uuid.UUID, device string, ok bool) {
	eventID, teamID, ok = parseManagedTeamID(ctx)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, "", false
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, uuid.Nil, "", false
	}
	return eventID, teamID, challengeID, ctx.Param("device"), true
}
