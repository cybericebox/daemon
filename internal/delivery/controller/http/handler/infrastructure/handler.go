package infrastructure

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/handler/labview"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
	infrastructureUseCase "github.com/cybericebox/daemon/internal/useCase/infrastructure"
	"github.com/cybericebox/daemon/pkg/pagination"
)

const observationsDefaultPageSize = 100

type (
	Handler struct {
		useCase IUseCase
		prot    IProtection
	}

	IProtection interface {
		RequirePermission(required rbac.Permission) gin.HandlerFunc
	}

	IUseCase interface {
		InfrastructureStatus(ctx context.Context) (infrastructureUseCase.Status, error)
		CurrentLabMonitoring(ctx context.Context, includeRecent bool) ([]infrastructureUseCase.CurrentLabView, error)
		ListLabMonitoring(ctx context.Context, eventID, teamID uuid.NullUUID, from, to, cursorAt time.Time, cursorID uuid.UUID, pageSize int32) (infrastructureUseCase.Page[infrastructureUseCase.LabMonitoringView], error)
		CurrentCapacityMonitoring(ctx context.Context) ([]infrastructureUseCase.CapacityMonitoringView, error)
		ListCapacityMonitoring(ctx context.Context, from, to, cursorAt time.Time, cursorID uuid.UUID, pageSize int32) (infrastructureUseCase.Page[infrastructureUseCase.CapacityMonitoringView], error)
		ListStands(ctx context.Context, filter infrastructureUseCase.StandsFilter) (infrastructureUseCase.StandsPage, error)
		ListStandEvents(ctx context.Context) ([]infrastructureUseCase.StandEventView, error)
		InfrastructureSummary(ctx context.Context) (infrastructureUseCase.SummaryView, error)
		RecreateTeamStand(ctx context.Context, eventID, teamID, by uuid.UUID) (eventUseCase.StandTeamView, error)
		ListTestLabs(ctx context.Context, search string, page, pageSize int) (infrastructureUseCase.TestLabsPage, error)
		TerminateTestLab(ctx context.Context, id uuid.UUID) error
		GetTeamStandDetail(ctx context.Context, eventID, teamID uuid.UUID) (eventUseCase.StandDetailView, error)
		ResetStandDevice(ctx context.Context, eventID, teamID, challengeID uuid.UUID, device string) error
		RescueStandDevice(ctx context.Context, eventID, teamID, challengeID uuid.UUID, device string, enable bool) error
		GetTestLabDetail(ctx context.Context, id uuid.UUID) (infrastructureUseCase.TestLabDetail, error)
		ResetTestLabDevice(ctx context.Context, id uuid.UUID, device string) error
		RescueTestLabDevice(ctx context.Context, id uuid.UUID, device string, enable bool) error
		ListAgents(ctx context.Context, includeArchived bool) (infrastructureUseCase.AgentsView, error)
		PreviewAgentDelete(ctx context.Context, id uuid.UUID) (infrastructureUseCase.DeletePreview, error)
		ReconnectAgent(ctx context.Context, id uuid.UUID, token, caPEM string) (infrastructureUseCase.AgentAdminView, error)
		EnrollAgent(ctx context.Context, in infraModel.AgentEnrollment) (infrastructureUseCase.AgentAdminView, error)
		UpdateAgent(ctx context.Context, id uuid.UUID, in infraModel.AgentUpdate) (infrastructureUseCase.AgentAdminView, error)
		RenewAgentCertificate(ctx context.Context, id uuid.UUID) (infrastructureUseCase.AgentAdminView, error)
		RotateAgentAccessKey(ctx context.Context, id uuid.UUID) (infrastructureUseCase.AgentAdminView, error)
		DeleteAgent(ctx context.Context, id uuid.UUID, confirm bool) error
		CheckAgent(ctx context.Context, id uuid.UUID) (infrastructureUseCase.AgentAdminView, error)
	}

	statusResponse struct {
		// Available is true when an infrastructure agent is connected — the
		// up-front signal the UI uses to enable/disable lab-deploying actions.
		Available bool `json:"Available"`
		// Healthy is true when the connected agent answers a live health check.
		Healthy      bool                 `json:"Healthy"`
		Mode         string               `json:"Mode"`
		Agents       []agentResponse      `json:"Agents"`
		Capabilities capabilitiesResponse `json:"Capabilities"`
		Warning      *warningResponse     `json:"Warning,omitempty"`
	}

	agentResponse struct {
		ID         uuid.UUID `json:"ID"`
		Key        string    `json:"Key"`
		Name       string    `json:"Name"`
		Configured bool      `json:"Configured"`
		Healthy    bool      `json:"Healthy"`
	}
	capabilitiesResponse struct {
		Laboratories bool `json:"Laboratories"`
	}
	warningResponse struct {
		Code    string `json:"Code"`
		Message string `json:"Message"`
	}

	// currentLabResponse is the merged current state of one lab group. Payload
	// keeps the agent's protojson camelCase keys.
	currentLabResponse struct {
		EventID      uuid.UUID `json:"EventID"`
		EventName    string    `json:"EventName"`
		EventTeamID  uuid.UUID `json:"EventTeamID"`
		TeamName     string    `json:"TeamName"`
		Moderators   bool      `json:"Moderators"`
		LabGroupName string    `json:"LabGroupName"`
		AgentID      string    `json:"AgentID"`
		Sequence     int64     `json:"Sequence"`
		ObservedAt   time.Time `json:"ObservedAt"`
		UpdatedAt    time.Time `json:"UpdatedAt"`
		Payload      any       `json:"Payload"`
	}

	observationResponse struct {
		ID            uuid.UUID `json:"ID"`
		EventID       uuid.UUID `json:"EventID"`
		EventName     string    `json:"EventName"`
		EventTeamID   uuid.UUID `json:"EventTeamID"`
		TeamName      string    `json:"TeamName"`
		Moderators    bool      `json:"Moderators"`
		LabGroupName  string    `json:"LabGroupName"`
		AgentID       string    `json:"AgentID"`
		Sequence      int64     `json:"Sequence"`
		ObservedAt    time.Time `json:"ObservedAt"`
		ReceivedAt    time.Time `json:"ReceivedAt"`
		SchemaVersion int32     `json:"SchemaVersion"`
		Snapshot      bool      `json:"Snapshot"`
		Payload       any       `json:"Payload"`
	}

	capacityResponse struct {
		ID            uuid.UUID `json:"ID"`
		AgentID       string    `json:"AgentID"`
		Sequence      int64     `json:"Sequence"`
		ObservedAt    time.Time `json:"ObservedAt"`
		ReceivedAt    time.Time `json:"ReceivedAt"`
		SchemaVersion int32     `json:"SchemaVersion"`
		Snapshot      bool      `json:"Snapshot"`
		Payload       any       `json:"Payload"`
	}

	// keysetResponse is a cursor page: NextCursor is present only when HasMore.
	keysetResponse[T any] struct {
		Items      []T        `json:"Items"`
		HasMore    bool       `json:"HasMore"`
		NextCursor *uuid.UUID `json:"NextCursor,omitempty"`
	}

	standResponse struct {
		EventID         uuid.UUID         `json:"EventID"`
		EventName       string            `json:"EventName"`
		EventTag        string            `json:"EventTag"`
		TeamID          uuid.UUID         `json:"TeamID"`
		TeamName        string            `json:"TeamName"`
		Moderators      bool              `json:"Moderators"`
		Status          string            `json:"Status"`
		Reason          string            `json:"Reason"`
		UpdatedAt       time.Time         `json:"UpdatedAt"`
		StatusChangedAt time.Time         `json:"StatusChangedAt"`
		Generation      int32             `json:"Generation"`
		Resources       resourcesResponse `json:"Resources"`
		// Queue is set while some of the stand's labs wait in the launch queue (the best placed one).
		Queue *labview.StandQueueResponse `json:"Queue"`
		// ImageWarning: an image of the stand is pulled by tag, not pinned to a digest.
		ImageWarning bool `json:"ImageWarning"`
	}

	// resourcesResponse is what a lab uses now, summed over its devices. Usage is
	// the live figure (UsageAvailable); Requested is the fallback. Known is false
	// while no device of the lab has been observed.
	resourcesResponse struct {
		Known                  bool  `json:"Known"`
		UsageAvailable         bool  `json:"UsageAvailable"`
		CPUMillicores          int64 `json:"CPUMillicores"`
		MemoryBytes            int64 `json:"MemoryBytes"`
		RequestedCPUMillicores int64 `json:"RequestedCPUMillicores"`
		RequestedMemoryBytes   int64 `json:"RequestedMemoryBytes"`
	}

	rescueDeviceRequest struct {
		// Enable true starts the device in rescue mode (a shell from its latest snapshot), false returns it to normal.
		Enable bool `json:"Enable"`
	}

	// testLabDetailResponse is one test lab with its live state down to the devices.
	testLabDetailResponse struct {
		ID        uuid.UUID `json:"ID"`
		GroupName string    `json:"GroupName"`
		// Status is queued, creating, ready or failed.
		Status string                 `json:"Status"`
		Live   labview.StatusResponse `json:"Live"`
	}

	// testLabResponse is one catalog test lab (an exercise test deploy).
	testLabResponse struct {
		ID            uuid.UUID `json:"ID"`
		GroupName     string    `json:"GroupName"`
		ExerciseID    uuid.UUID `json:"ExerciseID"`
		ExerciseName  string    `json:"ExerciseName"`
		VariantNumber int32     `json:"VariantNumber"`
		AuthorID      uuid.UUID `json:"AuthorID"`
		AuthorName    string    `json:"AuthorName"`
		AuthorEmail   string    `json:"AuthorEmail"`
		CreatedAt     time.Time `json:"CreatedAt"`
		ExpiresAt     time.Time `json:"ExpiresAt"`
		Expired       bool      `json:"Expired"`
		// Status is creating, ready, failed or unknown (the agent did not answer).
		// A queued lab waits in the launch queue (Queue).
		Status    string                 `json:"Status"`
		Resources resourcesResponse      `json:"Resources"`
		Queue     *labview.QueueResponse `json:"Queue"`
		// ImageWarning: an image of the lab or its group is pulled by tag, not pinned to a digest.
		ImageWarning bool `json:"ImageWarning"`
	}

	standEventResponse struct {
		ID   uuid.UUID `json:"ID"`
		Name string    `json:"Name"`
		Tag  string    `json:"Tag"`
	}

	recreatedStandResponse struct {
		EventID    uuid.UUID  `json:"EventID"`
		TeamID     uuid.UUID  `json:"TeamID"`
		Status     string     `json:"Status"`
		Reason     string     `json:"Reason"`
		Generation int32      `json:"Generation"`
		UpdatedAt  *time.Time `json:"UpdatedAt"`
	}

	summaryResponse struct {
		// Stands counts every team stand, the moderators team included.
		Stands standCountsResponse `json:"Stands"`
		// Moderators is the moderators-team part of Stands.
		Moderators standCountsResponse   `json:"Moderators"`
		TestLabs   testLabCountsResponse `json:"TestLabs"`
		Capacity   capacityUsageResponse `json:"Capacity"`
	}
	testLabCountsResponse struct {
		Total   int64 `json:"Total"`
		Active  int64 `json:"Active"`
		Expired int64 `json:"Expired"`
	}
	standCountsResponse struct {
		Total    int64 `json:"Total"`
		Creating int64 `json:"Creating"`
		Ready    int64 `json:"Ready"`
		Failed   int64 `json:"Failed"`
		Removed  int64 `json:"Removed"`
		Active   int64 `json:"Active"`
	}
	capacityUsageResponse struct {
		Available     bool     `json:"Available"`
		CPUPercent    *float64 `json:"CPUPercent"`
		MemoryPercent *float64 `json:"MemoryPercent"`
	}

	standsQuery struct {
		pagination.OffsetParams
		EventID string `form:"eventId"`
		Status  string `form:"status"`
		// Kind is event (event teams) or moderators (the moderators team); empty for both.
		Kind   string `form:"kind"`
		Search string `form:"search"`
	}

	testLabsQuery struct {
		pagination.OffsetParams
		Search string `form:"search"`
	}
)

func NewInfrastructureAPIHandler(useCase IUseCase, prot IProtection) *Handler {
	return &Handler{useCase: useCase, prot: prot}
}

func (h *Handler) Init(router *gin.RouterGroup) {
	read := h.prot.RequirePermission(rbac.PermInfrastructureRead)
	router.GET("infrastructure/status", read, h.getStatus)
	router.GET("infrastructure/summary", read, h.getSummary)
	router.GET("infrastructure/monitoring/current", read, h.currentLabMonitoring)
	router.GET("infrastructure/monitoring/observations", read, h.listLabMonitoring)
	router.GET("infrastructure/monitoring/capacity/current", read, h.currentCapacityMonitoring)
	router.GET("infrastructure/monitoring/capacity/observations", read, h.listCapacityMonitoring)
	router.GET("infrastructure/agents", read, h.listAgents)
	router.POST("infrastructure/agents", h.prot.RequirePermission(rbac.PermInfrastructureWrite), h.createAgent)
	router.PUT("infrastructure/agents/:agentID", h.prot.RequirePermission(rbac.PermInfrastructureWrite), h.updateAgent)
	router.DELETE("infrastructure/agents/:agentID", h.prot.RequirePermission(rbac.PermInfrastructureWrite), h.deleteAgent)
	router.POST("infrastructure/agents/:agentID/check", read, h.checkAgent)
	router.GET("infrastructure/agents/:agentID/delete-preview", read, h.previewAgentDelete)
	router.POST("infrastructure/agents/:agentID/reconnect", h.prot.RequirePermission(rbac.PermInfrastructureWrite), h.reconnectAgent)
	router.POST("infrastructure/agents/:agentID/renew-certificate", h.prot.RequirePermission(rbac.PermInfrastructureWrite), h.renewAgentCertificate)
	router.POST("infrastructure/agents/:agentID/rotate-access-key", h.prot.RequirePermission(rbac.PermInfrastructureWrite), h.rotateAgentAccessKey)
	router.GET("infrastructure/stands", read, h.listStands)
	router.GET("infrastructure/stands/events", read, h.listStandEvents)
	// The target ids live in the route so the audit entry names the stand.
	router.POST(
		"infrastructure/stands/:eventID/:teamID/recreate",
		h.prot.RequirePermission(rbac.PermInfrastructureWrite),
		h.recreateStand,
	)
	router.GET("infrastructure/stands/:eventID/:teamID/detail", read, h.getStandDetail)
	// The stand, lab and device live in the route so the audit entry names what was touched.
	router.POST(
		"infrastructure/stands/:eventID/:teamID/challenges/:challengeID/devices/:device/reset",
		h.prot.RequirePermission(rbac.PermInfrastructureWrite),
		h.resetStandDevice,
	)
	router.POST(
		"infrastructure/stands/:eventID/:teamID/challenges/:challengeID/devices/:device/rescue",
		h.prot.RequirePermission(rbac.PermInfrastructureWrite),
		h.rescueStandDevice,
	)
	router.GET("infrastructure/test-labs", read, h.listTestLabs)
	router.GET("infrastructure/test-labs/:labID/detail", read, h.getTestLabDetail)
	router.POST(
		"infrastructure/test-labs/:labID/devices/:device/reset",
		h.prot.RequirePermission(rbac.PermInfrastructureWrite),
		h.resetTestLabDevice,
	)
	router.POST(
		"infrastructure/test-labs/:labID/devices/:device/rescue",
		h.prot.RequirePermission(rbac.PermInfrastructureWrite),
		h.rescueTestLabDevice,
	)
	// The lab id lives in the route so the audit entry names the lab.
	router.POST(
		"infrastructure/test-labs/:labID/terminate",
		h.prot.RequirePermission(rbac.PermInfrastructureWrite),
		h.terminateTestLab,
	)
}

// getStatus godoc
//
//	@Summary		Infrastructure availability
//	@Description	Whether the cyber-range infrastructure agent is connected and healthy. Requires infrastructure.read (super_admin only); event-moderator readiness uses the event lifecycle projection.
//	@Tags			infrastructure
//	@Produce		json
//	@Success		200	{object}	response.Response{data=statusResponse}
//	@Router			/infrastructure/status [get]
func (h *Handler) getStatus(ctx *gin.Context) {
	s, err := h.useCase.InfrastructureStatus(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	agents := make([]agentResponse, 0, len(s.Agents))
	for _, agent := range s.Agents {
		agents = append(agents, agentResponse{ID: agent.ID, Key: agent.Key, Name: agent.Name, Configured: agent.Configured, Healthy: agent.Healthy})
	}
	var warning *warningResponse
	if s.Warning != nil {
		warning = &warningResponse{Code: s.Warning.Code, Message: s.Warning.Message}
	}
	response.AbortWithData(ctx, statusResponse{Available: s.Connected, Healthy: s.Healthy, Mode: string(s.Mode), Agents: agents, Capabilities: capabilitiesResponse{Laboratories: s.Capabilities.Laboratories}, Warning: warning})
}

// currentLabMonitoring godoc
//
//	@Summary	Get current Laboratory state
//	@Description	Returns the merged current state (last snapshot plus every later delta) of every team lab group, with event and team names. Active events only unless includeRecent is set. Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		includeRecent	query	bool	false	"also list groups of inactive events updated in the last 7 days"
//	@Success	200	{object}	response.Response{data=[]currentLabResponse}
//	@Router		/infrastructure/monitoring/current [get]
func (h *Handler) currentLabMonitoring(ctx *gin.Context) {
	includeRecent := false
	if value := ctx.Query("includeRecent"); value != "" {
		parsed, err := strconv.ParseBool(value)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		includeRecent = parsed
	}
	items, err := h.useCase.CurrentLabMonitoring(ctx, includeRecent)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]currentLabResponse, 0, len(items))
	for _, item := range items {
		out = append(out, currentLabResponse{
			EventID: item.EventID, EventName: item.EventName, EventTeamID: item.EventTeamID, TeamName: teamName(item.TeamName, item.Moderators), Moderators: item.Moderators,
			LabGroupName: item.LabGroupName, AgentID: item.AgentID, Sequence: item.Sequence, ObservedAt: item.ObservedAt, UpdatedAt: item.UpdatedAt,
			Payload: item.Payload,
		})
	}
	response.AbortWithData(ctx, out)
}

// listLabMonitoring godoc
//
//	@Summary	List Laboratory resource observations
//	@Description	Returns immutable secret-free resource and access observations (snapshots and deltas), newest first, as a cursor page. Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		eventId		query	string	false	"event ID"
//	@Param		teamId		query	string	false	"event team ID"
//	@Param		from		query	string	false	"RFC3339 lower time bound"
//	@Param		to			query	string	false	"RFC3339 upper time bound"
//	@Param		cursor		query	string	false	"NextCursor of the previous page"
//	@Param		pageSize	query	int		false	"page size (default 100, max 200)"
//	@Success	200	{object}	response.Response{data=keysetResponse[observationResponse]}
//	@Router		/infrastructure/monitoring/observations [get]
func (h *Handler) listLabMonitoring(ctx *gin.Context) {
	window, ok := parseObservationWindow(ctx)
	if !ok {
		return
	}
	eventID, teamID, ok := monitoringFilterIDs(ctx)
	if !ok {
		return
	}
	page, err := h.useCase.ListLabMonitoring(ctx, eventID, teamID, window.from, window.to, endOfTime, window.cursor, window.pageSize)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]observationResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, observationResponse{
			ID: item.ID, EventID: item.EventID, EventName: item.EventName, EventTeamID: item.EventTeamID, TeamName: teamName(item.TeamName, item.Moderators), Moderators: item.Moderators,
			LabGroupName: item.LabGroupName, AgentID: item.AgentID, Sequence: item.Sequence, ObservedAt: item.ObservedAt, ReceivedAt: item.ReceivedAt,
			SchemaVersion: item.SchemaVersion, Snapshot: item.Snapshot, Payload: item.Payload,
		})
	}
	response.AbortWithData(ctx, keysetResponse[observationResponse]{Items: items, HasMore: page.HasMore, NextCursor: page.NextCursor})
}

// currentCapacityMonitoring godoc
//
//	@Summary	Get current Laboratory cluster capacity
//	@Description	Returns the latest cluster-wide allocatable and requested CPU/RAM capacity for every connected Laboratory agent. Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Success	200	{object}	response.Response{data=[]capacityResponse}
//	@Router		/infrastructure/monitoring/capacity/current [get]
func (h *Handler) currentCapacityMonitoring(ctx *gin.Context) {
	items, err := h.useCase.CurrentCapacityMonitoring(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, capacityItems(items))
}

// listCapacityMonitoring godoc
//
//	@Summary	List Laboratory cluster-capacity observations
//	@Description	Returns immutable cluster-wide CPU/RAM capacity observations, newest first, as a cursor page. Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		from		query	string	false	"RFC3339 lower time bound"
//	@Param		to			query	string	false	"RFC3339 upper time bound"
//	@Param		cursor		query	string	false	"NextCursor of the previous page"
//	@Param		pageSize	query	int		false	"page size (default 100, max 200)"
//	@Success	200	{object}	response.Response{data=keysetResponse[capacityResponse]}
//	@Router		/infrastructure/monitoring/capacity/observations [get]
func (h *Handler) listCapacityMonitoring(ctx *gin.Context) {
	window, ok := parseObservationWindow(ctx)
	if !ok {
		return
	}
	page, err := h.useCase.ListCapacityMonitoring(ctx, window.from, window.to, endOfTime, window.cursor, window.pageSize)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, keysetResponse[capacityResponse]{Items: capacityItems(page.Items), HasMore: page.HasMore, NextCursor: page.NextCursor})
}

// listStands godoc
//
//	@Summary	List team stands across events
//	@Description	Platform-wide stand table with event and team names. status is creating, ready, failed, removed or active (creating + ready). Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		eventId		query	string	false	"event ID"
//	@Param		status		query	string	false	"creating | ready | failed | removed | active"
//	@Param		kind		query	string	false	"event | moderators"
//	@Param		search		query	string	false	"event name or tag, team name"
//	@Param		page		query	int		false	"1-based page"
//	@Param		pageSize	query	int		false	"page size"
//	@Success	200	{object}	response.Response{data=pagination.OffsetPage[standResponse]}
//	@Router		/infrastructure/stands [get]
func (h *Handler) listStands(ctx *gin.Context) {
	var query standsQuery
	if err := ctx.ShouldBindQuery(&query); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := query.OffsetParams.Validate(); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	filter := infrastructureUseCase.StandsFilter{Search: strings.TrimSpace(query.Search), Page: query.Page, PageSize: query.PageSize}
	if query.EventID != "" {
		id, err := uuid.FromString(query.EventID)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return
		}
		filter.EventID = uuid.NullUUID{UUID: id, Valid: true}
	}
	statuses, err := parseStatuses(query.Status)
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	filter.Statuses = statuses
	switch query.Kind {
	case "", platformStandKindEvent, platformStandKindModerators:
		filter.Kind = query.Kind
	default:
		response.AbortWithBadRequest(ctx, errInvalidKind)
		return
	}
	page, err := h.useCase.ListStands(ctx, filter)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]standResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, standResponse{
			EventID: item.EventID, EventName: item.EventName, EventTag: item.EventTag, TeamID: item.TeamID, TeamName: item.TeamName, Moderators: item.Moderators,
			Status: item.Status.String(), Reason: item.Reason, UpdatedAt: item.UpdatedAt, StatusChangedAt: item.StatusChangedAt, Generation: item.Generation,
			Resources: resourcesOf(item.Resources),
		})
	}
	response.AbortWithData(ctx, pagination.NewOffsetPage(items, page.Total, page.Page, page.PageSize))
}

// listStandEvents godoc
//
//	@Summary	List events that have stands
//	@Description	Feeds the event filter of the stand table. Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Success	200	{object}	response.Response{data=[]standEventResponse}
//	@Router		/infrastructure/stands/events [get]
func (h *Handler) listStandEvents(ctx *gin.Context) {
	events, err := h.useCase.ListStandEvents(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := make([]standEventResponse, 0, len(events))
	for _, event := range events {
		out = append(out, standEventResponse{ID: event.ID, Name: event.Name, Tag: event.Tag})
	}
	response.AbortWithData(ctx, out)
}

// getSummary godoc
//
//	@Summary	Infrastructure dashboard summary
//	@Description	Stand counts (failed, active, ...) and cluster CPU/RAM usage in percent. Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Success	200	{object}	response.Response{data=summaryResponse}
//	@Router		/infrastructure/summary [get]
func (h *Handler) getSummary(ctx *gin.Context) {
	v, err := h.useCase.InfrastructureSummary(ctx)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, summaryResponse{
		Stands:     standCounts(v.Stands),
		Moderators: standCounts(v.Moderators),
		TestLabs:   testLabCountsResponse{Total: v.TestLabs.Total, Active: v.TestLabs.Active, Expired: v.TestLabs.Expired},
		Capacity:   capacityUsageResponse{Available: v.Capacity.Available, CPUPercent: v.Capacity.CPUPercent, MemoryPercent: v.Capacity.MemoryPercent},
	})
}

// recreateStand godoc
//
//	@Summary	Recreate one team stand
//	@Description	Replaces every Lab of the team stand; the LabGroup and issued VPN configurations survive. The event and team ids are in the route and recorded in the audit log. Requires infrastructure.write (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		eventID	path	string	true	"event ID"
//	@Param		teamID	path	string	true	"team ID"
//	@Success	200	{object}	response.Response{data=recreatedStandResponse}
//	@Router		/infrastructure/stands/{eventID}/{teamID}/recreate [post]
func (h *Handler) recreateStand(ctx *gin.Context) {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx.Request.Context())
	if !ok {
		response.AbortWithUnauthenticated(ctx)
		return
	}
	eventID, err := uuid.FromString(ctx.Param("eventID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	teamID, err := uuid.FromString(ctx.Param("teamID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	audit.SetTarget(ctx, "event:"+eventID.String()+" team:"+teamID.String())
	v, err := h.useCase.RecreateTeamStand(ctx, eventID, teamID, claims.UserID)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, recreatedStandResponse{EventID: eventID, TeamID: v.TeamID, Status: v.Status.String(), Reason: v.Reason, Generation: v.Generation, UpdatedAt: v.UpdatedAt})
}

// listTestLabs godoc
//
//	@Summary	List catalog test labs
//	@Description	Catalog test labs (exercise test deploys) running on the infrastructure, newest first, with author, exercise, lease and live state. Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		search		query	string	false	"exercise name, author name or email"
//	@Param		page		query	int		false	"1-based page"
//	@Param		pageSize	query	int		false	"page size"
//	@Success	200	{object}	response.Response{data=pagination.OffsetPage[testLabResponse]}
//	@Router		/infrastructure/test-labs [get]
func (h *Handler) listTestLabs(ctx *gin.Context) {
	var query testLabsQuery
	if err := ctx.ShouldBindQuery(&query); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	if err := query.OffsetParams.Validate(); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	page, err := h.useCase.ListTestLabs(ctx, strings.TrimSpace(query.Search), query.Page, query.PageSize)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	items := make([]testLabResponse, 0, len(page.Items))
	for _, item := range page.Items {
		items = append(items, testLabResponse{
			ID: item.ID, GroupName: item.GroupName, ExerciseID: item.ExerciseID, ExerciseName: item.ExerciseName, VariantNumber: item.VariantNumber,
			AuthorID: item.AuthorID, AuthorName: item.AuthorName, AuthorEmail: item.AuthorEmail,
			CreatedAt: item.CreatedAt, ExpiresAt: item.ExpiresAt, Expired: item.Expired, Status: item.Status, Resources: resourcesOf(item.Resources),
			Queue: labview.Queue(item.Queue), ImageWarning: item.ImageWarning,
		})
	}
	response.AbortWithData(ctx, pagination.NewOffsetPage(items, page.Total, page.Page, page.PageSize))
}

// terminateTestLab godoc
//
//	@Summary	End a catalog test lab
//	@Description	Destroys the lab group of a test deploy and removes its lease, like when the author stops it. The lab id is in the route and recorded in the audit log. Requires infrastructure.write (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		labID	path	string	true	"test lab ID"
//	@Success	200	{object}	response.Response
//	@Router		/infrastructure/test-labs/{labID}/terminate [post]
func (h *Handler) terminateTestLab(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("labID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	audit.SetTarget(ctx, "test-lab:"+id.String())
	if err := h.useCase.TerminateTestLab(ctx, id); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithSuccess(ctx)
}

type observationWindow struct {
	from, to time.Time
	cursor   uuid.UUID
	pageSize int32
}

// endOfTime is the observation cursor's time part while no cursor is given.
var endOfTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)

// maxObservationCursor means "start from the newest row".
var maxObservationCursor = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))

func parseObservationWindow(ctx *gin.Context) (observationWindow, bool) {
	window := observationWindow{from: time.Now().Add(-24 * time.Hour), to: time.Now(), cursor: maxObservationCursor, pageSize: observationsDefaultPageSize}
	for _, bound := range []struct {
		key  string
		dest *time.Time
	}{{"from", &window.from}, {"to", &window.to}} {
		if value := ctx.Query(bound.key); value != "" {
			parsed, err := time.Parse(time.RFC3339, value)
			if err != nil {
				response.AbortWithBadRequest(ctx, err)
				return observationWindow{}, false
			}
			*bound.dest = parsed
		}
	}
	if value := ctx.Query("cursor"); value != "" {
		parsed, err := uuid.FromString(value)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return observationWindow{}, false
		}
		window.cursor = parsed
	}
	if value := ctx.Query("pageSize"); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 1 || parsed > pagination.MaxPageSize {
			response.AbortWithBadRequest(ctx, pagination.ErrInvalidPageSize)
			return observationWindow{}, false
		}
		window.pageSize = int32(parsed)
	}
	return window, true
}

func parseStatuses(value string) ([]eventStandModel.Status, error) {
	switch value {
	case "":
		return nil, nil
	case "active":
		return []eventStandModel.Status{eventStandModel.StatusCreating, eventStandModel.StatusReady}, nil
	case "creating":
		return []eventStandModel.Status{eventStandModel.StatusCreating}, nil
	case "ready":
		return []eventStandModel.Status{eventStandModel.StatusReady}, nil
	case "failed":
		return []eventStandModel.Status{eventStandModel.StatusFailed}, nil
	case "removed":
		return []eventStandModel.Status{eventStandModel.StatusRemoved}, nil
	default:
		return nil, errInvalidStatus
	}
}

var errInvalidStatus = errors.New("invalid stand status filter")
var errInvalidKind = errors.New("invalid stand kind filter")

// Stand kinds of the list filter.
const (
	platformStandKindEvent      = "event"
	platformStandKindModerators = "moderators"
)

// teamName blanks the technical moderators-team name; clients label the team.
func teamName(name string, moderators bool) string {
	if moderators {
		return ""
	}
	return name
}

func resourcesOf(v infrastructureUseCase.ResourcesView) resourcesResponse {
	return resourcesResponse{Known: v.Known, UsageAvailable: v.Available, CPUMillicores: v.CPUMillicores, MemoryBytes: v.MemoryBytes, RequestedCPUMillicores: v.RequestedCPU, RequestedMemoryBytes: v.RequestedMemory}
}

func standCounts(v infrastructureUseCase.StandCountsView) standCountsResponse {
	return standCountsResponse{Total: v.Total, Creating: v.Creating, Ready: v.Ready, Failed: v.Failed, Removed: v.Removed, Active: v.Active}
}

func capacityItems(items []infrastructureUseCase.CapacityMonitoringView) []capacityResponse {
	out := make([]capacityResponse, 0, len(items))
	for _, item := range items {
		out = append(out, capacityResponse{ID: item.ID, AgentID: item.AgentID, Sequence: item.Sequence, ObservedAt: item.ObservedAt, ReceivedAt: item.ReceivedAt, SchemaVersion: item.SchemaVersion, Snapshot: item.Snapshot, Payload: item.Payload})
	}
	return out
}

func monitoringFilterIDs(ctx *gin.Context) (uuid.NullUUID, uuid.NullUUID, bool) {
	var eventID, teamID uuid.NullUUID
	for _, filter := range []struct {
		key  string
		dest *uuid.NullUUID
	}{{"eventId", &eventID}, {"teamId", &teamID}} {
		if value := ctx.Query(filter.key); value != "" {
			parsed, err := uuid.FromString(value)
			if err != nil {
				response.AbortWithBadRequest(ctx, err)
				return uuid.NullUUID{}, uuid.NullUUID{}, false
			}
			*filter.dest = uuid.NullUUID{UUID: parsed, Valid: true}
		}
	}
	return eventID, teamID, true
}

// getStandDetail godoc
//
//	@Summary	Live state of one team stand
//	@Description	Per-lab live state of a stand: launch queue (Phase Queued), devices with their snapshot state, image warnings. A lab the agent cannot answer for has Live null and LiveUnavailable true. Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		eventID	path	string	true	"event ID"
//	@Param		teamID	path	string	true	"team ID"
//	@Success	200	{object}	response.Response{data=labview.StandDetailResponse}
//	@Router		/infrastructure/stands/{eventID}/{teamID}/detail [get]
func (h *Handler) getStandDetail(ctx *gin.Context) {
	eventID, teamID, ok := parseStand(ctx)
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
//
//	@Summary	Reset one device of any stand Lab to its base image
//	@Description	Discards the device's snapshots and restarts it from the image. 409 (71405) when the device keeps no state, 404 (31406) when the Lab or device is not there, 503 (71407) while it is still being restarted. Requires infrastructure.write (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		eventID		path	string	true	"event ID"
//	@Param		teamID		path	string	true	"team ID"
//	@Param		challengeID	path	string	true	"event challenge ID"
//	@Param		device		path	string	true	"device name"
//	@Success	200	{object}	response.Response
//	@Router		/infrastructure/stands/{eventID}/{teamID}/challenges/{challengeID}/devices/{device}/reset [post]
func (h *Handler) resetStandDevice(ctx *gin.Context) {
	eventID, teamID, ok := parseStand(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	device := ctx.Param("device")
	audit.SetTarget(ctx, "event:"+eventID.String()+" team:"+teamID.String()+" challenge:"+challengeID.String()+" device:"+device)
	if err = h.useCase.ResetStandDevice(ctx, eventID, teamID, challengeID, device); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}

// rescueStandDevice godoc
//
//	@Summary	Start one device of any stand Lab in rescue mode, or back to normal
//	@Description	Enable=true starts the device from its latest snapshot with a shell instead of the image entrypoint; Enable=false returns it to the normal start. Errors as for reset. Requires infrastructure.write (super_admin only).
//	@Tags		infrastructure
//	@Accept		json
//	@Produce	json
//	@Param		eventID		path	string	true	"event ID"
//	@Param		teamID		path	string	true	"team ID"
//	@Param		challengeID	path	string	true	"event challenge ID"
//	@Param		device		path	string	true	"device name"
//	@Param		body		body	rescueDeviceRequest	true	"rescue switch"
//	@Success	200	{object}	response.Response
//	@Router		/infrastructure/stands/{eventID}/{teamID}/challenges/{challengeID}/devices/{device}/rescue [post]
func (h *Handler) rescueStandDevice(ctx *gin.Context) {
	eventID, teamID, ok := parseStand(ctx)
	if !ok {
		return
	}
	challengeID, err := uuid.FromString(ctx.Param("challengeID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req rescueDeviceRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	device := ctx.Param("device")
	audit.SetTarget(ctx, "event:"+eventID.String()+" team:"+teamID.String()+" challenge:"+challengeID.String()+" device:"+device)
	if err = h.useCase.RescueStandDevice(ctx, eventID, teamID, challengeID, device, req.Enable); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}

// getTestLabDetail godoc
//
//	@Summary	Live state of one catalog test lab
//	@Description	Launch queue, devices with their snapshot state and image warnings of a test lab. Requires infrastructure.read (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		labID	path	string	true	"test lab ID"
//	@Success	200	{object}	response.Response{data=testLabDetailResponse}
//	@Router		/infrastructure/test-labs/{labID}/detail [get]
func (h *Handler) getTestLabDetail(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("labID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.GetTestLabDetail(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	live := labview.Status(v.Live)
	topology := make(map[string]labview.DeviceInfo, len(v.Topology))
	for name, d := range v.Topology {
		topology[name] = labview.DeviceInfo{Name: d.Name, Type: string(d.Type)}
	}
	labview.AnnotateDevices(&live, topology)
	response.AbortWithData(ctx, testLabDetailResponse{ID: v.ID, GroupName: v.GroupName, Status: v.Status, Live: live})
}

// resetTestLabDevice godoc
//
//	@Summary	Reset one device of a catalog test lab to its base image
//	@Description	Errors as for stand devices (71405, 31406, 71407). Requires infrastructure.write (super_admin only).
//	@Tags		infrastructure
//	@Produce	json
//	@Param		labID	path	string	true	"test lab ID"
//	@Param		device	path	string	true	"device name"
//	@Success	200	{object}	response.Response
//	@Router		/infrastructure/test-labs/{labID}/devices/{device}/reset [post]
func (h *Handler) resetTestLabDevice(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("labID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	device := ctx.Param("device")
	audit.SetTarget(ctx, "test-lab:"+id.String()+" device:"+device)
	if err = h.useCase.ResetTestLabDevice(ctx, id, device); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}

// rescueTestLabDevice godoc
//
//	@Summary	Start one device of a catalog test lab in rescue mode, or back to normal
//	@Description	Errors as for stand devices. Requires infrastructure.write (super_admin only).
//	@Tags		infrastructure
//	@Accept		json
//	@Produce	json
//	@Param		labID	path	string	true	"test lab ID"
//	@Param		device	path	string	true	"device name"
//	@Param		body	body	rescueDeviceRequest	true	"rescue switch"
//	@Success	200	{object}	response.Response
//	@Router		/infrastructure/test-labs/{labID}/devices/{device}/rescue [post]
func (h *Handler) rescueTestLabDevice(ctx *gin.Context) {
	id, err := uuid.FromString(ctx.Param("labID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	var req rescueDeviceRequest
	if err = ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	device := ctx.Param("device")
	audit.SetTarget(ctx, "test-lab:"+id.String()+" device:"+device)
	if err = h.useCase.RescueTestLabDevice(ctx, id, device, req.Enable); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}

func parseStand(ctx *gin.Context) (eventID, teamID uuid.UUID, ok bool) {
	eventID, err := uuid.FromString(ctx.Param("eventID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, false
	}
	teamID, err = uuid.FromString(ctx.Param("teamID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, uuid.Nil, false
	}
	return eventID, teamID, true
}
