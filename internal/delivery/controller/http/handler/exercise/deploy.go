package exercise

import (
	exerciseUseCase "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/labview"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

type deployListItem struct {
	DeployID  string    `json:"DeployID"`
	ExpiresAt time.Time `json:"ExpiresAt"`
	CreatedAt time.Time `json:"CreatedAt"`
	// Expired: the lease is over but the lab is not removed yet; it still runs and can be ended.
	Expired    bool   `json:"Expired"`
	ExerciseID string `json:"ExerciseID"`
	VersionID  string `json:"VersionID"`
	VariantID  string `json:"VariantID"`
	Lab        string `json:"Lab"`
	// Tasks are the flag-linked tasks to find in the lab; values are never returned.
	Tasks []deployTaskResponse `json:"Tasks"`
	// SolvedTaskIDs are the tasks the author has already checked correctly.
	SolvedTaskIDs []string `json:"SolvedTaskIDs"`
}

func deployListItemOf(d exerciseModel.TestDeploy) deployListItem {
	tasks := make([]deployTaskResponse, 0, len(d.Flags))
	for _, f := range d.Flags {
		tasks = append(tasks, deployTaskResponse{TaskID: f.TaskID.String(), Name: f.Name})
	}
	return deployListItem{SolvedTaskIDs: solvedIDs(d.Solved), DeployID: d.ID.String(), ExpiresAt: d.ExpiresAt, CreatedAt: d.CreatedAt, Expired: d.Expired, ExerciseID: d.ExerciseID.String(), VersionID: d.VersionID.String(), VariantID: d.VariantID.String(), Lab: d.LabName, Tasks: tasks}
}

func solvedIDs(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

type (
	deployResponse struct {
		DeployID  string `json:"DeployID"`
		Lab       string `json:"Lab"`
		VPNClient string `json:"VPNClient,omitempty"`
		// Tasks are the flag-linked tasks to find in the lab; values are never returned.
		Tasks []deployTaskResponse `json:"Tasks,omitempty"`
	}

	deployTaskResponse struct {
		TaskID string `json:"TaskID"`
		Name   string `json:"Name"`
	}

	deployStatusResponse struct {
		Phase        string                 `json:"Phase"`
		Ready        bool                   `json:"Ready"`
		VPNCIDR      string                 `json:"VPNCIDR,omitempty"`
		InternetCIDR string                 `json:"InternetCIDR,omitempty"`
		VPNConfig    string                 `json:"VPNConfig,omitempty"`
		Devices      []deployDeviceResponse `json:"Devices,omitempty"`
		Access       []deployAccessResponse `json:"Access,omitempty"`
		// VPNConnected: the author's VPN client shook hands within the last three minutes.
		VPNConnected bool `json:"VPNConnected"`
		// Queue is set while the lab waits in the launch queue (Phase Queued); otherwise null.
		Queue *labview.QueueResponse `json:"Queue"`
		// ImageWarning lists the lab images pulled by tag (not pinned to a digest); GroupImageWarning is the same for
		// the VPN or gateway image. Empty when fine.
		ImageWarning      string `json:"ImageWarning"`
		GroupImageWarning string `json:"GroupImageWarning"`
		// SolvedTaskIDs are the tasks the author has already checked correctly.
		SolvedTaskIDs []string `json:"SolvedTaskIDs"`
		// VPNLastHandshake is the client's last handshake; absent when it never connected.
		VPNLastHandshake *time.Time `json:"VPNLastHandshake,omitempty"`
		// VPNProbeURL is the tester page served inside the tunnel by the lab group's VPN pod; absent until known.
		VPNProbeURL string `json:"VPNProbeURL,omitempty"`
		// ExpiresAt is the end of the lease; Expired: it is over but the lab is not removed yet (it can only be ended).
		ExpiresAt time.Time `json:"ExpiresAt"`
		Expired   bool      `json:"Expired"`
	}

	deployDeviceResponse struct {
		Name  string `json:"Name"`
		Ready bool   `json:"Ready"`
		// Reason is the container waiting/termination reason (ImagePullBackOff, ...); empty while healthy.
		Reason string `json:"Reason"`
		// Scheduling is the pod's way through the scheduler queue; null when it does not track the device.
		Scheduling *labview.SchedulingResponse `json:"Scheduling"`
		// Snapshot is null for a device without state persistence.
		Snapshot *labview.SnapshotResponse `json:"Snapshot"`
	}

	rescueDeviceRequest struct {
		// Enable true starts the device in rescue mode (a shell from its latest snapshot), false returns it to normal.
		Enable bool `json:"Enable"`
	}

	deployAccessResponse struct {
		Device   string `json:"Device"`
		Port     int32  `json:"Port"`
		Protocol string `json:"Protocol"`
		URL      string `json:"URL"`
	}
)

func deployHandleToResponse(h exerciseModel.DeployHandle) deployResponse {
	out := deployResponse{DeployID: h.Group, Lab: h.Lab, VPNClient: h.VPNClient}
	for _, f := range h.Flags {
		out.Tasks = append(out.Tasks, deployTaskResponse{TaskID: f.TaskID.String(), Name: f.Name})
	}
	return out
}

func deployStatusToResponse(s exerciseModel.LabDeployStatus) deployStatusResponse {
	out := deployStatusResponse{
		Phase: s.Phase, Ready: s.Ready, VPNCIDR: s.VPNCIDR, InternetCIDR: s.InternetCIDR, VPNConfig: s.VPNConfig,
		Queue: labview.Queue(s.Queue), ImageWarning: s.ImageWarning, GroupImageWarning: s.GroupImageWarning,
	}
	out.SolvedTaskIDs = solvedIDs(s.SolvedTasks)
	out.VPNConnected = s.VPNConnected
	out.VPNProbeURL = s.VPNProbeURL
	out.ExpiresAt, out.Expired = s.ExpiresAt, s.Expired
	if !s.VPNLastHandshake.IsZero() {
		t := s.VPNLastHandshake
		out.VPNLastHandshake = &t
	}
	for _, d := range s.Devices {
		out.Devices = append(out.Devices, deployDeviceResponse{Name: d.Name, Ready: d.Ready, Reason: d.Reason, Scheduling: labview.Scheduling(d.Scheduling), Snapshot: labview.Snapshot(d.Snapshot)})
	}
	for _, a := range s.Access {
		out.Access = append(out.Access, deployAccessResponse{Device: a.Device, Port: a.Port, Protocol: a.Protocol, URL: a.URL})
	}
	return out
}

// deploy godoc
// @Summary  Deploy a variant's topology for testing
// @Tags     exercises
// @Produce  json
// @Param    id         path  string  true  "exercise id"
// @Param    versionID  path  string  true  "version id"
// @Param    variantID  path  string  true  "variant id"
// @Success  200  {object}  response.Response{data=deployResponse}
// @Failure  400  {object}  response.Response
// @Failure  409  {object}  response.Response
// @Router   /exercises/{id}/versions/{versionID}/variants/{variantID}/deploy [post]
func (h *Handler) deploy(ctx *gin.Context) {
	versionID, err := uuid.FromString(ctx.Param("versionID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	variantID, err := uuid.FromString(ctx.Param("variantID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	exerciseID, ok := parseExerciseID(ctx)
	if !ok {
		return
	}
	if err = h.useCase.AuthorizeTestDeploy(ctx, exerciseUseCase.Actor{UserID: claims.UserID, Role: claims.Role}, exerciseID, versionID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	handle, err := h.useCase.DeployVariantTest(ctx, claims.UserID, versionID, variantID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, deployHandleToResponse(handle))
}

// deployStatus godoc
// @Summary  Poll a test deploy's runtime status
// @Tags     exercises
// @Produce  json
// @Param    deployID  path  string  true  "test deployment id"
// @Success  200  {object}  response.Response{data=deployStatusResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/deploys/{group} [get]
func (h *Handler) deployStatus(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	deployID, err := uuid.FromString(ctx.Param("deployID"))
	if err != nil {
		response.AbortWithBadRequest(ctx)
		return
	}
	status, err := h.useCase.DeployTestStatus(ctx, claims.UserID, deployID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, deployStatusToResponse(status))
}

// destroyDeploy godoc
// @Summary  Tear a test deploy down
// @Tags     exercises
// @Param    deployID  path  string  true  "test deployment id"
// @Success  200  {object}  response.Response
// @Failure  400  {object}  response.Response
// @Router   /exercises/deploys/{group} [delete]
func (h *Handler) destroyDeploy(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	deployID, err := uuid.FromString(ctx.Param("deployID"))
	if err != nil {
		response.AbortWithBadRequest(ctx)
		return
	}
	if err := h.useCase.DestroyDeployTest(ctx, claims.UserID, deployID); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

type deployCheckRequest struct {
	TaskID string `json:"TaskID" binding:"required"`
	Flag   string `json:"Flag"`
}

type deployCheckResponse struct {
	Correct bool `json:"Correct"`
}

// checkDeploy godoc
// @Summary  Check a flag the author found in a test deploy
// @Description The expected values stay on the server; the answer is only correct or not. Case-sensitive exact match, author of the deploy only.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    deployID  path  string  true  "test deployment id"
// @Param    body  body  deployCheckRequest  true  "the task and the flag found"
// @Success  200  {object}  response.Response{data=deployCheckResponse}
// @Failure  400  {object}  response.Response
// @Router   /exercises/deploys/{deployID}/check [post]
func (h *Handler) checkDeploy(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	deployID, err := uuid.FromString(ctx.Param("deployID"))
	if err != nil {
		response.AbortWithBadRequest(ctx)
		return
	}
	var req deployCheckRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx)
		return
	}
	taskID, err := uuid.FromString(req.TaskID)
	if err != nil {
		response.AbortWithBadRequest(ctx)
		return
	}
	correct, err := h.useCase.CheckTestFlag(ctx, claims.UserID, deployID, taskID, req.Flag)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, deployCheckResponse{Correct: correct})
}

// listDeploys godoc
// @Summary  List the caller's test deploys
// @Description Only the caller's own, until they are removed: a deploy whose lease is over but is not removed yet has Expired=true (it still runs and can be ended). Optional filters narrow by exercise, version and variant. Each item names the flag-linked tasks; flag values are never returned.
// @Tags     exercises
// @Produce  json
// @Param    exerciseID  query  string  false  "exercise id"
// @Param    versionID   query  string  false  "version id"
// @Param    variantID   query  string  false  "variant id"
// @Success  200  {object}  response.Response{data=[]deployListItem}
// @Failure  400  {object}  response.Response
// @Router   /exercises/deploys [get]
func (h *Handler) listDeploys(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var exerciseID, versionID, variantID uuid.UUID
	for key, target := range map[string]*uuid.UUID{"exerciseID": &exerciseID, "versionID": &versionID, "variantID": &variantID} {
		if raw := ctx.Query(key); raw != "" {
			parsed, parseErr := uuid.FromString(raw)
			if parseErr != nil {
				response.AbortWithBadRequest(ctx, parseErr)
				return
			}
			*target = parsed
		}
	}
	items, err := h.useCase.ListTestDeploys(ctx, claims.UserID, exerciseID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]deployListItem, 0, len(items))
	for _, d := range items {
		if (versionID != uuid.Nil && d.VersionID != versionID) || (variantID != uuid.Nil && d.VariantID != variantID) {
			continue
		}
		out = append(out, deployListItemOf(d))
	}
	response.AbortWithData(ctx, out)
}
func (h *Handler) extendDeploy(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("deployID"))
	if err != nil {
		response.AbortWithBadRequest(ctx)
		return
	}
	d, err := h.useCase.ExtendTestDeploy(ctx, claims.UserID, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, deployListItemOf(d))
}

type deployLinkRequest struct {
	Device string `json:"Device" binding:"required"`
	Port   int32  `json:"Port"`
}

type deployLinkResponse struct {
	URL       string    `json:"URL"`
	ExpiresAt time.Time `json:"ExpiresAt"`
}

// deployLink godoc
// @Summary  Get the link that opens one web device of a ready test deploy
// @Description Returns https://<device>-<code>.<base>/_auth?t=... for the author of the deploy. The link lives about two minutes and is single use; the page fetches it on every click. The laboratory proxy sets its own session cookie, which lasts until the deploy's lease ends; the platform sets none. Test traffic is never counted for an event.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    deployID  path  string  true  "test deployment id"
// @Param    body  body  deployLinkRequest  true  "the device and port from the deploy status"
// @Success  200  {object}  response.Response{data=deployLinkResponse}
// @Failure  409  {object}  response.Response
// @Router   /exercises/deploys/{deployID}/link [post]
func (h *Handler) deployLink(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	deployID, err := uuid.FromString(ctx.Param("deployID"))
	if err != nil {
		response.AbortWithBadRequest(ctx)
		return
	}
	var req deployLinkRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx)
		return
	}
	link, err := h.useCase.OpenTestDeployLink(ctx, claims.UserID, deployID, req.Device, req.Port)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, deployLinkResponse{URL: link.URL, ExpiresAt: link.ExpiresAt})
}

// resetDeployDevice godoc
// @Summary  Reset one device of the author's own test lab to its base image
// @Description Discards the device's snapshots and restarts it from the image. 409 (71405) when the device keeps no state, 404 (31406) when the lab or device is not there, 503 (71407) while it is still being restarted.
// @Tags     exercises
// @Produce  json
// @Param    deployID  path  string  true  "deploy id"
// @Param    device    path  string  true  "device name"
// @Success  200  {object}  response.Response
// @Router   /exercises/deploys/{deployID}/devices/{device}/reset [post]
func (h *Handler) resetDeployDevice(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("deployID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.ResetTestDeployDevice(ctx, claims.UserID, id, ctx.Param("device")); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}

// rescueDeployDevice godoc
// @Summary  Start one device of the author's own test lab in rescue mode, or back to normal
// @Description Enable=true starts the device from its latest snapshot with a shell instead of the image entrypoint; Enable=false returns it to the normal start. Errors as for reset.
// @Tags     exercises
// @Accept   json
// @Produce  json
// @Param    deployID  path  string  true  "deploy id"
// @Param    device    path  string  true  "device name"
// @Param    body      body  rescueDeviceRequest  true  "rescue switch"
// @Success  200  {object}  response.Response
// @Router   /exercises/deploys/{deployID}/devices/{device}/rescue [post]
func (h *Handler) rescueDeployDevice(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	id, err := uuid.FromString(ctx.Param("deployID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req rescueDeviceRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err = h.useCase.RescueTestDeployDevice(ctx, claims.UserID, id, ctx.Param("device"), req.Enable); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}
