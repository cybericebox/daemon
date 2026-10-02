package exercise

import (
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
)

// requestElevation godoc
// @Summary  Ask a platform admin to let devices of the task go above the platform resource frame
// @Description  Asks for the devices of the working copy (the draft, else the published version) that pass the frame and are not covered by an earlier approval, with a reason. One request is open per exercise. 409 (70969) when no device needs it, 409 (70970) when a request is already pending, 400 (20967) when a device is above the elevation ceiling, 400 (20973) without a reason. The platform admins are notified.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    id    path  string                   true  "exercise ID"
// @Param    body  body  requestElevationRequest  true  "reason"
// @Success  200  {object}  response.Response{data=elevationResponse}
// @Router   /exercises/{id}/resource-elevations [post]
func (h *Handler) requestElevation(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var req requestElevationRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	e, err := h.useCase.RequestElevation(ctx, actor, id, req.Reason)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	audit.SetTarget(ctx, "exercise:"+id.String())
	response.AbortWithData(ctx, elevationToResponse(e))
}

// exerciseElevations godoc
// @Summary  Resource elevation requests of one exercise, newest first
// @Tags     exercises
// @Produce  json
// @Param    id  path  string  true  "exercise ID"
// @Success  200  {object}  response.Response{data=[]elevationResponse}
// @Router   /exercises/{id}/resource-elevations [get]
func (h *Handler) exerciseElevations(ctx *gin.Context) {
	id, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListExerciseElevations(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, elevationsToResponse(items))
}

// listElevations godoc
// @Summary  Resource elevation requests (platform admin)
// @Description  Requires exercises.elevations.read (super_admin only).
// @Tags     exercises
// @Produce  json
// @Param    status  query  string  false  "pending | approved | rejected; empty lists all"
// @Success  200  {object}  response.Response{data=[]elevationResponse}
// @Router   /exercises/resource-elevations [get]
func (h *Handler) listElevations(ctx *gin.Context) {
	items, err := h.useCase.ListElevations(ctx, ctx.Query("status"))
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, elevationsToResponse(items))
}

// decideElevation godoc
// @Summary  Approve or reject a resource elevation request
// @Description  The approval stores the approved values per device (default: exactly what was requested; lower values are allowed, never above the ceiling): a later version of the exercise keeps it while every value stays at or below them. 404 (30968), 409 (70971) when already decided, 400 (20972) for invalid approved values. Notifies the author and closes the admins' request. Requires exercises.elevations.write (super_admin only).
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    elevationID  path  string                  true  "request ID"
// @Param    body         body  decideElevationRequest  true  "decision"
// @Success  200  {object}  response.Response{data=elevationResponse}
// @Router   /exercises/resource-elevations/{elevationID}/decide [post]
func (h *Handler) decideElevation(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("elevationID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	actor, ok := actorFrom(ctx)
	if !ok {
		return
	}
	var req decideElevationRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	in := exerciseUseCase.DecideElevationInput{Approve: req.Approve, Note: req.Note}
	for _, d := range req.Devices {
		in.Devices = append(in.Devices, exerciseUseCase.ElevationDevice{DeviceID: d.DeviceID, Name: d.Name, CPUMillicores: d.CPUMillicores, MemoryBytes: d.MemoryBytes})
	}
	e, err := h.useCase.DecideElevation(ctx, actor, id, in)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	audit.SetTarget(ctx, "exercise:"+e.ExerciseID.String())
	response.AbortWithData(ctx, elevationToResponse(e))
}
