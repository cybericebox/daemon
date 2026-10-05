package event

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/labview"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	"github.com/cybericebox/daemon/internal/model/rbac"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type standsResponse struct {
	InfrastructureAllowed bool  `json:"InfrastructureAllowed"`
	LaboratoriesAvailable bool  `json:"LaboratoriesAvailable"`
	TeardownDelayMinutes  int32 `json:"TeardownDelayMinutes"`
	// DeployAt is when the first labs start deploying: the event start minus the lead the platform computes.
	DeployAt         *time.Time           `json:"DeployAt"`
	TeardownAt       *time.Time           `json:"TeardownAt"`
	ChallengesOpened bool                 `json:"ChallengesOpened"`
	Summary          standSummaryResponse `json:"Summary"`
	// Prewarm is the state of the image cache for the event's images; null while nothing was prewarmed.
	Prewarm *standPrewarmResponse `json:"Prewarm"`
	Items   []standTeamResponse   `json:"Items"`
}

// standPrewarmResponse counts the event's images by cache state as the agent last reported them.
type standPrewarmResponse struct {
	Total     int       `json:"Total"`
	Done      int       `json:"Done"`
	Warming   int       `json:"Warming"`
	Queued    int       `json:"Queued"`
	Failed    int       `json:"Failed"`
	Skipped   int       `json:"Skipped"`
	UpdatedAt time.Time `json:"UpdatedAt"`
}

type standSummaryResponse struct {
	Total       int `json:"Total"`
	NotDeployed int `json:"NotDeployed"`
	Creating    int `json:"Creating"`
	Ready       int `json:"Ready"`
	Failed      int `json:"Failed"`
	Removed     int `json:"Removed"`
}

type standTeamResponse struct {
	TeamID     uuid.UUID          `json:"TeamID"`
	TeamName   string             `json:"TeamName"`
	Moderators bool               `json:"Moderators"`
	Status     string             `json:"Status"`
	Reason     string             `json:"Reason"`
	UpdatedAt  *time.Time         `json:"UpdatedAt"`
	Generation int32              `json:"Generation"`
	Labs       []standLabResponse `json:"Labs"`
	// Queue is set while some of the stand's labs wait in the launch queue (the best placed one);
	// from the current monitoring state.
	Queue *labview.StandQueueResponse `json:"Queue"`
	// ImageWarning: an image of the stand is pulled by tag, not pinned to a digest.
	ImageWarning bool `json:"ImageWarning"`
}

type standLabResponse struct {
	ChallengeID   uuid.UUID `json:"ChallengeID"`
	ChallengeName string    `json:"ChallengeName"`
	Status        string    `json:"Status"`
	Reason        string    `json:"Reason"`
}

type standSettingsRequest struct {
	TeardownDelayMinutes int32 `json:"TeardownDelayMinutes"`
}

type moderatorsChallengeResponse struct {
	ChallengeID uuid.UUID                 `json:"ChallengeID"`
	Name        string                    `json:"Name"`
	Readiness   string                    `json:"Readiness"`
	Lab         *moderatorsLabRefResponse `json:"Lab"`
}

type moderatorsLabRefResponse struct {
	Status string `json:"Status"`
}

type standLabStatusResponse struct {
	Phase        string                   `json:"Phase"`
	Ready        bool                     `json:"Ready"`
	VPNCIDR      string                   `json:"VPNCIDR"`
	InternetCIDR string                   `json:"InternetCIDR"`
	Access       []standLabAccessResponse `json:"Access"`
	// Queue is set while the Lab waits in the launch queue (phase Queued).
	Queue             *labview.QueueResponse   `json:"Queue"`
	ImageWarning      string                   `json:"ImageWarning"`
	GroupImageWarning string                   `json:"GroupImageWarning"`
	Devices           []labview.DeviceResponse `json:"Devices"`
}

type standLabAccessResponse struct {
	Device   string `json:"Device"`
	Port     int32  `json:"Port"`
	Protocol string `json:"Protocol"`
	URL      string `json:"URL"`
}

type standVPNConfigResponse struct {
	Config string `json:"Config"`
}

func toStandsResponse(v eventUseCase.StandsView) standsResponse {
	out := standsResponse{
		InfrastructureAllowed: v.InfrastructureAllowed, LaboratoriesAvailable: v.LaboratoriesAvailable,
		TeardownDelayMinutes: v.Timing.TeardownDelayMinutes,
		DeployAt:             v.DeployAt, TeardownAt: v.TeardownAt, ChallengesOpened: v.ChallengesOpened,
		Summary: standSummaryResponse{Total: v.Summary.Total, NotDeployed: v.Summary.NotDeployed, Creating: v.Summary.Creating,
			Ready: v.Summary.Ready, Failed: v.Summary.Failed, Removed: v.Summary.Removed},
		Items: make([]standTeamResponse, 0, len(v.Items)),
	}
	if p := v.Prewarm; p != nil {
		out.Prewarm = &standPrewarmResponse{Total: p.Total, Done: p.Done, Warming: p.Warming, Queued: p.Queued, Failed: p.Failed, Skipped: p.Skipped, UpdatedAt: p.UpdatedAt}
	}
	for _, item := range v.Items {
		out.Items = append(out.Items, toStandTeamResponse(item))
	}
	return out
}

func toStandTeamResponse(v eventUseCase.StandTeamView) standTeamResponse {
	out := standTeamResponse{TeamID: v.TeamID, TeamName: v.TeamName, Moderators: v.Moderators, Status: v.Status.String(),
		Reason: v.Reason, UpdatedAt: v.UpdatedAt, Generation: v.Generation, Labs: make([]standLabResponse, 0, len(v.Labs)),
		Queue: labview.StandQueue(v.Launch), ImageWarning: v.Launch.ImageWarning}
	for _, lab := range v.Labs {
		out.Labs = append(out.Labs, standLabResponse{ChallengeID: lab.ChallengeID, ChallengeName: lab.ChallengeName, Status: labReadinessString(lab.Readiness), Reason: lab.Reason})
	}
	return out
}

func labReadinessString(r labBindingModel.Readiness) string {
	switch r {
	case labBindingModel.ReadinessReady:
		return "ready"
	case labBindingModel.ReadinessFailed:
		return "failed"
	case labBindingModel.ReadinessDestroyed:
		return "removed"
	default:
		return "pending"
	}
}

func teamChallengeReadinessString(r teamChallengeModel.Readiness) string {
	switch r {
	case teamChallengeModel.ReadinessReady:
		return "ready"
	case teamChallengeModel.ReadinessPublished:
		return "available"
	case teamChallengeModel.ReadinessFailed:
		return "failed"
	default:
		return "preparing"
	}
}

func toStandLabStatusResponse(v exerciseModel.LabDeployStatus) standLabStatusResponse {
	live := labview.Status(v)
	out := standLabStatusResponse{Phase: v.Phase, Ready: v.Ready, VPNCIDR: v.VPNCIDR, InternetCIDR: v.InternetCIDR, Access: make([]standLabAccessResponse, 0, len(v.Access)),
		Queue: live.Queue, ImageWarning: live.ImageWarning, GroupImageWarning: live.GroupImageWarning, Devices: live.Devices}
	for _, a := range v.Access {
		out.Access = append(out.Access, standLabAccessResponse{Device: a.Device, Port: a.Port, Protocol: a.Protocol, URL: a.URL})
	}
	return out
}

// getStands godoc
// @Summary List team stands (moderator «Стенди»)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=standsResponse}
// @Router /events/{id}/manage/labs [get]
func (h *Handler) getStands(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.GetEventStands(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toStandsResponse(v))
}

// updateStandSettings godoc
// @Summary Change the stand deploy lead and teardown delay
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param body body standSettingsRequest true "stand schedule"
// @Success 200 {object} response.Response{data=standsResponse}
// @Router /events/{id}/manage/labs/settings [put]
func (h *Handler) updateStandSettings(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req standSettingsRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.UpdateEventStandSettings(ctx, eventID, eventStandModel.Timing{TeardownDelayMinutes: req.TeardownDelayMinutes}, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toStandsResponse(v))
}

// recreateStand godoc
// @Summary Recreate one team stand (its Labs; VPN configs stay valid)
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param teamID path string true "team ID"
// @Success 200 {object} response.Response{data=standTeamResponse}
// @Router /events/{id}/manage/labs/{teamID}/recreate [post]
func (h *Handler) recreateStand(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, teamID, ok := parseManagedTeamID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.RecreateTeamStand(ctx, eventID, teamID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toStandTeamResponse(v))
}

// listModeratorsChallenges godoc
// @Summary List the moderators team challenges with their Lab state
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=[]moderatorsChallengeResponse}
// @Router /events/{id}/manage/labs/moderators/challenges [get]
func (h *Handler) listModeratorsChallenges(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	items, err := h.useCase.ListModeratorsChallenges(ctx, eventID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]moderatorsChallengeResponse, 0, len(items))
	for _, item := range items {
		value := moderatorsChallengeResponse{ChallengeID: item.ChallengeID, Name: item.Name, Readiness: teamChallengeReadinessString(item.Readiness)}
		if item.LabReadiness != nil {
			value.Lab = &moderatorsLabRefResponse{Status: labReadinessString(*item.LabReadiness)}
		}
		out = append(out, value)
	}
	response.AbortWithData(ctx, out)
}

// moderatorsChallengeLab godoc
// @Summary Read the live Lab status and access links of a moderators team challenge
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Success 200 {object} response.Response{data=standLabStatusResponse}
// @Router /events/{id}/manage/labs/moderators/challenges/{challengeID}/lab [get]
func (h *Handler) moderatorsChallengeLab(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.GetModeratorsChallengeLabStatus(ctx, eventID, challengeID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toStandLabStatusResponse(v))
}

// moderatorsVPNConfig godoc
// @Summary Issue the caller's VPN config for the moderators team stand
// @Tags events
// @Produce json
// @Param id path string true "event ID"
// @Success 200 {object} response.Response{data=standVPNConfigResponse}
// @Router /events/{id}/manage/labs/moderators/vpn [get]
func (h *Handler) moderatorsVPNConfig(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	config, err := h.useCase.GetModeratorsVPNConfig(ctx, eventID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, standVPNConfigResponse{Config: config})
}

type moderatorsLabLinkRequest struct {
	Device string `json:"Device" binding:"required"`
	Port   int32  `json:"Port"`
}

type moderatorsLabLinkResponse struct {
	URL       string    `json:"url"`
	ExpiresAt time.Time `json:"expires_at"`
}

// moderatorsLabLink godoc
// @Summary Get the link that opens one web device of a moderators team task's lab
// @Description Returns https://<device>-<code>.<base>/_auth?t=... for the event owner and write moderators testing tasks as the hidden moderators team. The link lives about two minutes and is single use; the page fetches it on every click. The laboratory proxy sets its own session cookie; the platform sets none.
// @Tags events
// @Accept json
// @Produce json
// @Param id path string true "event ID"
// @Param challengeID path string true "event challenge ID"
// @Param body body moderatorsLabLinkRequest true "the device and port from the lab status"
// @Success 200 {object} response.Response{data=moderatorsLabLinkResponse}
// @Router /events/{id}/manage/labs/moderators/challenges/{challengeID}/lab/link [post]
func (h *Handler) moderatorsLabLink(ctx *gin.Context) {
	eventID, ok := parseEventID(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	var req moderatorsLabLinkRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	link, err := h.useCase.OpenModeratorsLabLink(ctx, eventID, claims.UserID, challengeID, req.Device, req.Port)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	ctx.Header("Cache-Control", "private, no-store")
	response.AbortWithData(ctx, moderatorsLabLinkResponse{URL: link.URL, ExpiresAt: link.ExpiresAt})
}
