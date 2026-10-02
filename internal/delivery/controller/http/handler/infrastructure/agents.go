package infrastructure

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	infrastructureUseCase "github.com/cybericebox/daemon/internal/useCase/infrastructure"
)

type (
	// agentAdminResponse is one infrastructure agent. Source is admin (added in the admin) or env
	// (bootstrapped from the deployment config); both are managed the same way. Keys and the enrollment
	// token never leave the server.
	agentAdminResponse struct {
		ID       uuid.UUID `json:"ID"`
		Name     string    `json:"Name"`
		Source   string    `json:"Source"`
		Endpoint string    `json:"Endpoint"`
		Enabled  bool      `json:"Enabled"`
		// Priority orders the agents: the smaller is placed first, ties by name.
		Priority int  `json:"Priority"`
		HasCA    bool `json:"HasCA"`
		InUse    bool `json:"InUse"`
		// Tenant is the agent's tenant name (its certificate CN); AccessKeyID the key that signs the lab
		// access tokens now; RetiredKeys how many rotated-out keys still wait for removal at the agent.
		Tenant      string `json:"Tenant"`
		AccessKeyID string `json:"AccessKeyID"`
		RetiredKeys int    `json:"RetiredKeys"`
		// Groups is how many lab groups the agent holds.
		Groups int64 `json:"Groups"`
		// Capacity is the last known capacity (the tenant quota), kept while the agent is offline.
		Capacity agentCapacityResponse `json:"Capacity"`
		// Features is what the agent last reported it offers this platform (device state persistence, image
		// cache, scheduler, endpoints); null until its first report, and kept while it is offline.
		Features   *agentFeaturesResponse `json:"Features"`
		FeaturesAt *time.Time             `json:"FeaturesAt"`
		// ArchivedAt is set for a deleted agent (listed only with archived=1): its record stays for history.
		ArchivedAt *time.Time `json:"ArchivedAt"`
		// CertExpiresAt is the end of the client certificate's validity; it is renewed automatically
		// before that.
		CertExpiresAt *time.Time `json:"CertExpiresAt"`
		// Connected is false when the daemon holds no connection to the agent (its credentials could not
		// be opened); Healthy when it answered a ping just now.
		Connected bool      `json:"Connected"`
		Healthy   bool      `json:"Healthy"`
		LatencyMs int64     `json:"LatencyMs"`
		Error     string    `json:"Error"`
		CreatedAt time.Time `json:"CreatedAt"`
		UpdatedAt time.Time `json:"UpdatedAt"`
	}

	// agentCapacityResponse: SeenAt is null for an agent that was never seen (no capacity). With SeenAt set,
	// a null CPUMillicores or MemoryBytes means the tenant has no limit on that resource.
	agentCapacityResponse struct {
		CPUMillicores *int64     `json:"CPUMillicores"`
		MemoryBytes   *int64     `json:"MemoryBytes"`
		SeenAt        *time.Time `json:"SeenAt"`
	}

	agentFeaturesResponse struct {
		PersistenceAvailable        bool     `json:"PersistenceAvailable"`
		PersistenceDefaultDebounce  int64    `json:"PersistenceDefaultDebounceMs"`
		PersistenceWriteQuotaBytes  int64    `json:"PersistenceWriteQuotaBytes"`
		PersistenceMaxFileSizeBytes int64    `json:"PersistenceMaxFileSizeBytes"`
		PersistenceExcludedPaths    []string `json:"PersistenceExcludedPaths"`
		ImageCacheEnabled           bool     `json:"ImageCacheEnabled"`
		ImageCacheRegistries        []string `json:"ImageCacheRegistries"`
		SchedulerEnabled            bool     `json:"SchedulerEnabled"`
		// SchedulerMaxPods is the most pods starting at once; 0 = no limit.
		SchedulerMaxPods int32 `json:"SchedulerMaxPods"`
		// LabsDomain is the base domain of the lab web endpoints; VPNEndpoint is host:port of WireGuard.
		LabsDomain  string `json:"LabsDomain"`
		VPNEndpoint string `json:"VPNEndpoint"`
		// ProxyAccessTokenMaxTTLSeconds is the longest a web link can be opened; ProxySessionMaxTTLSeconds the
		// longest a proxy session lives (a longer event window means a new link is opened).
		ProxyAccessTokenMaxTTLSeconds int64 `json:"ProxyAccessTokenMaxTTLSeconds"`
		ProxySessionMaxTTLSeconds     int64 `json:"ProxySessionMaxTTLSeconds"`
		// ProxySessionIdleTTLSeconds is how long a proxy session lives without use (it slides while in use).
		ProxySessionIdleTTLSeconds int64 `json:"ProxySessionIdleTTLSeconds"`
	}

	agentsResponse struct {
		Items []agentAdminResponse `json:"Items"`
	}

	// enrollAgentRequest adds an agent: where it is and the one-time enrollment token the cluster
	// administrator gave. The platform generates every key itself; the token is never stored. CAPEM is
	// needed for self-signed development stands only.
	enrollAgentRequest struct {
		Name            string `json:"Name"`
		Endpoint        string `json:"Endpoint"`
		EnrollmentToken string `json:"EnrollmentToken"`
		CAPEM           string `json:"CAPEM"`
		Enabled         bool   `json:"Enabled"`
		Priority        int    `json:"Priority"`
	}

	// updateAgentRequest changes what an admin may change after enrollment. It is a partial update: a field
	// that is left out stays as it is. CAPEM empty or omitted keeps the stored server CA; ClearCA true removes
	// it. The endpoint, certificate and keys stay.
	updateAgentRequest struct {
		Name     *string `json:"Name"`
		CAPEM    *string `json:"CAPEM"`
		ClearCA  bool    `json:"ClearCA"`
		Enabled  *bool   `json:"Enabled"`
		Priority *int    `json:"Priority"`
	}
)

func toAgentResponse(v infrastructureUseCase.AgentAdminView) agentAdminResponse {
	return agentAdminResponse{
		ID: v.ID, Name: v.Name, Source: v.Source, Endpoint: v.Endpoint, Enabled: v.Enabled, Priority: v.Priority, HasCA: v.HasCA,
		Capacity: agentCapacityResponse{CPUMillicores: v.CapacityCPUMillicores, MemoryBytes: v.CapacityMemoryBytes, SeenAt: v.CapacitySeenAt}, ArchivedAt: v.ArchivedAt,
		InUse: v.InUse, Tenant: v.Tenant, AccessKeyID: v.AccessKeyID, RetiredKeys: v.RetiredKeys, Groups: v.Groups, CertExpiresAt: v.CertNotAfter,
		Connected: v.Probe.Connected, Healthy: v.Probe.Healthy, LatencyMs: v.Probe.Latency.Milliseconds(), Error: v.Probe.Error,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, Features: toFeaturesResponse(v.Features), FeaturesAt: v.FeaturesAt,
	}
}

func toFeaturesResponse(f *infraModel.AgentFeatures) *agentFeaturesResponse {
	if f == nil {
		return nil
	}
	return &agentFeaturesResponse{
		PersistenceAvailable: f.Persistence.Available, PersistenceDefaultDebounce: f.Persistence.DefaultDebounce,
		PersistenceWriteQuotaBytes: f.Persistence.WriteQuotaBytes, PersistenceMaxFileSizeBytes: f.Persistence.MaxFileSizeBytes,
		PersistenceExcludedPaths: f.Persistence.ExcludedPaths, ImageCacheEnabled: f.ImageCache.Enabled,
		ImageCacheRegistries: f.ImageCache.Registries, SchedulerEnabled: f.Scheduler.Enabled, SchedulerMaxPods: f.Scheduler.MaxPods,
		LabsDomain: f.Endpoints.LabsDomain, VPNEndpoint: f.Endpoints.VPNEndpoint,
		ProxyAccessTokenMaxTTLSeconds: f.Proxy.AccessTokenMaxTTLSeconds, ProxySessionMaxTTLSeconds: f.Proxy.SessionMaxTTLSeconds,
		ProxySessionIdleTTLSeconds: f.Proxy.SessionIdleTTLSeconds,
	}
}

func parseAgentID(ctx *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.FromString(ctx.Param("agentID"))
	if err != nil {
		response.AbortWithBadRequest(ctx, err)
		return uuid.Nil, false
	}
	return id, true
}

// listAgents godoc
//
//	@Summary		List infrastructure agents
//	@Description	The enrolled agents (Source admin: added in the admin; env: bootstrapped from AGENT_ENDPOINT and the enrollment token), with live state, held lab groups, capacity and the certificate expiry. Requires infrastructure.read (super_admin only).
//	@Tags			infrastructure
//	@Produce		json
//	@Param			archived	query		string	false	"1 also lists deleted (archived) agents"
//	@Success		200	{object}	response.Response{data=agentsResponse}
//	@Router			/infrastructure/agents [get]
func (h *Handler) listAgents(ctx *gin.Context) {
	v, err := h.useCase.ListAgents(ctx, ctx.Query("archived") == "1")
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := agentsResponse{Items: make([]agentAdminResponse, 0, len(v.Items))}
	for _, item := range v.Items {
		out.Items = append(out.Items, toAgentResponse(item))
	}
	ctx.Header("Cache-Control", "no-store")
	response.AbortWithData(ctx, out)
}

// createAgent godoc
//
//	@Summary		Add (enroll) an infrastructure agent
//	@Description	Enrolls the agent with the one-time token: the platform generates its mutual-TLS key and the signing key pair of the lab access tokens, the agent signs the client certificate (the tenant is its CN) and trusts the access public key. Both private keys are stored encrypted. 400 (21414) when the agent rejects the token (used, expired, unknown), 409 (41413) when the endpoint is already added. Requires infrastructure.write (super_admin only).
//	@Tags			infrastructure
//	@Accept			json
//	@Produce		json
//	@Param			body	body		enrollAgentRequest	true	"agent"
//	@Success		200		{object}	response.Response{data=agentAdminResponse}
//	@Router			/infrastructure/agents [post]
func (h *Handler) createAgent(ctx *gin.Context) {
	var req enrollAgentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.EnrollAgent(ctx, infraModel.AgentEnrollment{Name: req.Name, Endpoint: req.Endpoint, Token: req.EnrollmentToken, CAPEM: req.CAPEM, Enabled: req.Enabled, Priority: req.Priority})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	audit.SetTarget(ctx, "agent:"+v.ID.String())
	response.AbortWithData(ctx, toAgentResponse(v))
}

// updateAgent godoc
//
//	@Summary		Change an infrastructure agent
//	@Description	Name, server CA, switch and priority; the endpoint, certificate and keys stay. Requires infrastructure.write (super_admin only).
//	@Tags			infrastructure
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path		string			true	"agent ID"
//	@Param			body	body		updateAgentRequest	true	"agent"
//	@Success		200		{object}	response.Response{data=agentAdminResponse}
//	@Router			/infrastructure/agents/{agentID} [put]
func (h *Handler) updateAgent(ctx *gin.Context) {
	id, ok := parseAgentID(ctx)
	if !ok {
		return
	}
	audit.SetTarget(ctx, "agent:"+id.String())
	var req updateAgentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.UpdateAgent(ctx, id, infraModel.AgentUpdate{Name: req.Name, CAPEM: req.CAPEM, ClearCA: req.ClearCA, Enabled: req.Enabled, Priority: req.Priority})
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toAgentResponse(v))
}

// deleteAgent godoc
//
//	@Summary		Delete an infrastructure agent
//	@Description	Soft delete: the record stays for history, its keys, certificate and endpoint are wiped and its name gets an archive suffix. 409 (71411) while the agent holds lab groups; 409 (71416) when future reservations would lose capacity and confirm is not 1 (read delete-preview first). The reservations stay and become not covered. Requires infrastructure.write (super_admin only).
//	@Tags			infrastructure
//	@Produce		json
//	@Param			agentID	path		string	true	"agent ID"
//	@Param			confirm	query		string	false	"1 confirms the consequences listed by delete-preview"
//	@Success		200		{object}	response.Response
//	@Router			/infrastructure/agents/{agentID} [delete]
func (h *Handler) deleteAgent(ctx *gin.Context) {
	id, ok := parseAgentID(ctx)
	if !ok {
		return
	}
	audit.SetTarget(ctx, "agent:"+id.String())
	if err := h.useCase.DeleteAgent(ctx, id, ctx.Query("confirm") == "1"); err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, gin.H{})
}

// deletePreviewResponse says what deleting the agent would do. RunningGroups > 0 blocks the delete;
// FutureReservations need confirm=1.
type deletePreviewResponse struct {
	RunningGroups      int64                       `json:"RunningGroups"`
	FutureReservations []reservationImpactResponse `json:"FutureReservations"`
}

type reservationImpactResponse struct {
	ReservationID uuid.UUID `json:"ReservationID"`
	EventID       uuid.UUID `json:"EventID"`
	EventName     string    `json:"EventName"`
	CPUMillicores int64     `json:"CPUMillicores"`
	MemoryBytes   int64     `json:"MemoryBytes"`
	StartsAt      time.Time `json:"StartsAt"`
	EndsAt        time.Time `json:"EndsAt"`
}

// previewAgentDelete godoc
//
//	@Summary		What deleting an agent would do
//	@Description	The lab groups the agent holds (they block the delete) and the future reservations that would lose capacity. Requires infrastructure.read (super_admin only).
//	@Tags			infrastructure
//	@Produce		json
//	@Param			agentID	path		string	true	"agent ID"
//	@Success		200		{object}	response.Response{data=deletePreviewResponse}
//	@Router			/infrastructure/agents/{agentID}/delete-preview [get]
func (h *Handler) previewAgentDelete(ctx *gin.Context) {
	id, ok := parseAgentID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.PreviewAgentDelete(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	out := deletePreviewResponse{RunningGroups: v.RunningGroups, FutureReservations: make([]reservationImpactResponse, 0, len(v.FutureReservations))}
	for _, r := range v.FutureReservations {
		out.FutureReservations = append(out.FutureReservations, reservationImpactResponse{ReservationID: r.ReservationID, EventID: r.EventID, EventName: r.EventName, CPUMillicores: r.CPUMillicores, MemoryBytes: r.MemoryBytes, StartsAt: r.StartsAt, EndsAt: r.EndsAt})
	}
	response.AbortWithData(ctx, out)
}

type reconnectAgentRequest struct {
	EnrollmentToken string `json:"EnrollmentToken"`
	// CAPEM replaces the stored server CA; empty keeps it.
	CAPEM string `json:"CAPEM"`
}

// reconnectAgent godoc
//
//	@Summary		Reconnect an agent (re-enroll the same cluster)
//	@Description	Enrolls the same cluster again with a new one-time token for new keys (lost or compromised credentials). The record, priority and lab groups stay; the tenant must be the same (409, 71415). It is not a move: a move is a delete plus a new enrollment. Requires infrastructure.write (super_admin only).
//	@Tags			infrastructure
//	@Accept			json
//	@Produce		json
//	@Param			agentID	path		string					true	"agent ID"
//	@Param			body	body		reconnectAgentRequest	true	"token"
//	@Success		200		{object}	response.Response{data=agentAdminResponse}
//	@Router			/infrastructure/agents/{agentID}/reconnect [post]
func (h *Handler) reconnectAgent(ctx *gin.Context) {
	id, ok := parseAgentID(ctx)
	if !ok {
		return
	}
	audit.SetTarget(ctx, "agent:"+id.String())
	var req reconnectAgentRequest
	if err := ctx.ShouldBindJSON(&req); err != nil {
		response.AbortWithBadRequest(ctx, err)
		return
	}
	v, err := h.useCase.ReconnectAgent(ctx, id, req.EnrollmentToken, req.CAPEM)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toAgentResponse(v))
}

// checkAgent godoc
//
//	@Summary		Check an infrastructure agent now
//	@Description	Pings the agent and returns its state. Requires infrastructure.read (super_admin only).
//	@Tags			infrastructure
//	@Produce		json
//	@Param			agentID	path		string	true	"agent ID"
//	@Success		200		{object}	response.Response{data=agentAdminResponse}
//	@Router			/infrastructure/agents/{agentID}/check [post]
func (h *Handler) checkAgent(ctx *gin.Context) {
	id, ok := parseAgentID(ctx)
	if !ok {
		return
	}
	v, err := h.useCase.CheckAgent(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toAgentResponse(v))
}

// renewAgentCertificate godoc
//
//	@Summary		Renew an agent's client certificate now
//	@Description	Gets a new certificate for a new key over the agent's current connection. It is also renewed automatically 10 days before it ends (certificates are valid 30 days). The certificate must name the same tenant (409, 71415). Requires infrastructure.write (super_admin only).
//	@Tags			infrastructure
//	@Produce		json
//	@Param			agentID	path		string	true	"agent ID"
//	@Success		200		{object}	response.Response{data=agentAdminResponse}
//	@Router			/infrastructure/agents/{agentID}/renew-certificate [post]
func (h *Handler) renewAgentCertificate(ctx *gin.Context) {
	id, ok := parseAgentID(ctx)
	if !ok {
		return
	}
	audit.SetTarget(ctx, "agent:"+id.String())
	v, err := h.useCase.RenewAgentCertificate(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toAgentResponse(v))
}

// rotateAgentAccessKey godoc
//
//	@Summary		Rotate an agent's lab access signing key
//	@Description	Generates a new key pair, adds its public key at the agent (both keys work during the rotation), signs new tokens with it from now on and removes the old key 15 minutes later (after the longest token has expired). Requires infrastructure.write (super_admin only).
//	@Tags			infrastructure
//	@Produce		json
//	@Param			agentID	path		string	true	"agent ID"
//	@Success		200		{object}	response.Response{data=agentAdminResponse}
//	@Router			/infrastructure/agents/{agentID}/rotate-access-key [post]
func (h *Handler) rotateAgentAccessKey(ctx *gin.Context) {
	id, ok := parseAgentID(ctx)
	if !ok {
		return
	}
	audit.SetTarget(ctx, "agent:"+id.String())
	v, err := h.useCase.RotateAgentAccessKey(ctx, id)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, toAgentResponse(v))
}
