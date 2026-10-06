// Package infrastructure is the application-side seam to the cyber-range
// infrastructure agent (the laboratory LabManager). The agent is OPTIONAL: when
// it is not connected this use case reports it as unavailable and blocks any
// operation that needs it with an explicit error, so callers (and the admin /
// event-moderator UI) know there is no infrastructure to use.
package infrastructure

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabObservationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/infrastructureAgentRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/platformStandRepo"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
)

// Agent is the port to the infrastructure agent. It is nil when no agent is
// configured (the daemon then runs without infrastructure). Besides the health
// probe it exposes the lab-deploying operations the exercise use case drives to
// stand a variant's topology up for testing.
type Agent interface {
	// Health probes the agent connection (a gRPC Ping under the hood).
	Health(ctx context.Context) error
	// DeployLab stands up a variant's topology: it creates the lab group, the
	// lab (translated spec + env) and, when the topology needs it, a VPN client.
	// The lab provisions asynchronously — poll LabStatus.
	DeployLab(ctx context.Context, group, lab string, meta infraModel.LabMeta, topo exerciseModel.Topology) error
	// LabStatus reports a deployed lab's runtime status (phase, devices, access,
	// VPN config), resolving the group's namespace itself.
	LabStatus(ctx context.Context, group, lab string) (exerciseModel.LabDeployStatus, error)
	// EnsureVPNGroup creates a team's VPN group before any challenge Lab exists.
	EnsureVPNGroup(ctx context.Context, group string) error
	// EnsureLabClient creates or returns one named, participant-specific VPN
	// client for an existing group. An empty config means it is still reconciling.
	EnsureLabClient(ctx context.Context, group, client string) (string, error)
	// DestroyLabGroup tears a deploy down (cascades to lab, clients, namespace).
	DestroyLabGroup(ctx context.Context, group string) error
	// DeleteLab removes one Lab and keeps its group; a Lab that is already gone is fine.
	DeleteLab(ctx context.Context, group, lab string) error
	// DeleteLabClient removes one named VPN client of a group; one that is already gone is fine.
	DeleteLabClient(ctx context.Context, group, client string) error
	// ReconcileLabGroupAccess replaces the complete access policy of a group.
	ReconcileLabGroupAccess(ctx context.Context, group string, policies []labAccessModel.ClientPolicy) error
	// LabClientHandshake is a client's last WireGuard handshake; zero when it never connected.
	LabClientHandshake(ctx context.Context, group, client string) (time.Time, error)
}

type (
	InfrastructureUseCase struct {
		agent        Agent
		agents       *infrastructureAgentRepo.Repository
		observations *eventLabObservationRepo.Repository
		stands       *platformStandRepo.Repository
		now          func() time.Time
	}

	Dependencies struct {
		// Agent is nil when infrastructure is not configured.
		Agent        Agent
		Agents       *infrastructureAgentRepo.Repository
		Observations *eventLabObservationRepo.Repository
		Stands       *platformStandRepo.Repository
		// Now is the clock; time.Now when nil.
		Now func() time.Time
	}

	// Status is the availability snapshot the UI reads to decide, up front,
	// whether infrastructure-dependent actions (deploy/test a task) are offered.
	AvailabilityMode string

	Status struct {
		// Connected is true when an agent is configured and wired.
		Connected bool
		// Healthy is true when the configured agent answers a live health check.
		Healthy bool
		// Mode is one of missing_config, unhealthy or available.
		Mode         AvailabilityMode
		Agents       []AgentView
		Capabilities Capabilities
		Warning      *Warning
	}

	AgentView struct {
		ID         uuid.UUID `json:"id"`
		Key        string    `json:"key"`
		Name       string    `json:"name"`
		Configured bool      `json:"configured"`
		Healthy    bool      `json:"healthy"`
	}

	Capabilities struct {
		Laboratories bool `json:"laboratories"`
	}
	Warning struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}

	// CurrentLabView is the merged current state of one team lab group.
	CurrentLabView struct {
		EventID      uuid.UUID
		EventName    string
		EventTeamID  uuid.UUID
		TeamName     string
		Moderators   bool
		LabGroupName string
		AgentID      string
		Sequence     int64
		ObservedAt   time.Time
		UpdatedAt    time.Time
		Payload      json.RawMessage
	}

	// LabMonitoringView is one immutable observation (a snapshot or a delta).
	LabMonitoringView struct {
		ID            uuid.UUID
		EventID       uuid.UUID
		EventName     string
		EventTeamID   uuid.UUID
		TeamName      string
		Moderators    bool
		LabGroupName  string
		AgentID       string
		Sequence      int64
		ObservedAt    time.Time
		ReceivedAt    time.Time
		SchemaVersion int32
		Snapshot      bool
		Payload       json.RawMessage
	}

	CapacityMonitoringView struct {
		ID            uuid.UUID
		AgentID       string
		Sequence      int64
		ObservedAt    time.Time
		ReceivedAt    time.Time
		SchemaVersion int32
		Snapshot      bool
		Payload       json.RawMessage
	}

	// Page is a keyset page: NextCursor is the id of the last returned row and
	// is set only when HasMore.
	Page[T any] struct {
		Items      []T
		HasMore    bool
		NextCursor *uuid.UUID
	}
)

func NewInfrastructureUseCase(deps Dependencies) *InfrastructureUseCase {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &InfrastructureUseCase{agent: deps.Agent, agents: deps.Agents, observations: deps.Observations, stands: deps.Stands, now: now}
}

// InfrastructureAvailable reports whether an infrastructure agent is connected.
// This is the cheap, up-front signal (no network call) the admin / event
// moderator UI uses to enable or disable lab-deploying actions.
func (u *InfrastructureUseCase) InfrastructureAvailable() bool {
	return u.hasAgent()
}

// hasAgent is true when an agent is wired and, for a fleet, has at least one member: agents may be
// added in the admin while the daemon runs.
func (u *InfrastructureUseCase) hasAgent() bool {
	if u.agent == nil {
		return false
	}
	if reporter, ok := u.agent.(infraModel.AvailabilityReporter); ok {
		return reporter.Available()
	}
	return true
}

// LaboratoriesConfigured is true when infrastructure is configured at all (an agent exists). The
// periodic lab jobs stay quiet while it is not, instead of failing every tick.
func (u *InfrastructureUseCase) LaboratoriesConfigured() bool { return u.hasAgent() }

// RequireInfrastructure blocks an operation that needs the agent when it is not
// connected. Explicit error, never a silent no-op — the caller must know
// infrastructure is absent.
func (u *InfrastructureUseCase) RequireInfrastructure() error {
	if !u.hasAgent() {
		return infraModel.ErrInfrastructureUnavailable.Err()
	}
	return nil
}

// RequireLaboratories additionally verifies the live connection. This is used
// on runtime paths, while the status endpoint exposes the same capability to UI.
func (u *InfrastructureUseCase) RequireLaboratories(ctx context.Context) error {
	if !u.hasAgent() || u.agent.Health(ctx) != nil {
		return infraModel.ErrInfrastructureUnavailable.Err()
	}
	return nil
}

// InfrastructureStatus returns the availability snapshot, running a live health
// check when an agent is connected.
func (u *InfrastructureUseCase) InfrastructureStatus(ctx context.Context) (Status, error) {
	status := Status{Mode: AvailabilityMode("missing_config")}
	if u.agents != nil {
		registered, err := u.agents.List(ctx)
		if err != nil {
			return Status{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load infrastructure agents").Err()
		}
		status.Agents = make([]AgentView, 0, len(registered))
		for _, agent := range registered {
			status.Agents = append(status.Agents, AgentView{ID: agent.ID, Key: agent.Key, Name: agent.Name, Configured: agent.Configured})
		}
	}
	if !u.hasAgent() {
		status.Warning = &Warning{Code: "infrastructure_unavailable", Message: "No infrastructure agent is configured"}
		return status, nil
	}
	status.Connected = true
	status.Healthy = u.agent.Health(ctx) == nil
	if len(status.Agents) > 0 {
		status.Agents[0].Healthy = status.Healthy
	}
	if !status.Healthy {
		status.Mode = AvailabilityMode("unhealthy")
		status.Warning = &Warning{Code: "infrastructure_unavailable", Message: "The infrastructure agent is not healthy"}
		return status, nil
	}
	status.Mode = AvailabilityMode("available")
	status.Capabilities.Laboratories = true
	return status, nil
}

// CurrentLabMonitoring exposes the merged current state of every lab group
// through the platform infrastructure surface, never through an event-manager
// route. Only active events are listed unless includeRecent is set.
func (u *InfrastructureUseCase) CurrentLabMonitoring(ctx context.Context, includeRecent bool) ([]CurrentLabView, error) {
	now := u.now()
	items, err := u.observations.CurrentPlatform(ctx, includeRecent, now.Add(-labMonitoringModel.RecentWindow), now)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to load laboratory monitoring").Err()
	}
	out := make([]CurrentLabView, 0, len(items))
	for _, item := range items {
		out = append(out, CurrentLabView{
			EventID: item.EventID, EventName: item.EventName, EventTeamID: item.EventTeamID, TeamName: item.TeamName, Moderators: item.Moderators,
			LabGroupName: item.LabGroupName, AgentID: item.AgentID, Sequence: item.Sequence, ObservedAt: item.ObservedAt, UpdatedAt: item.UpdatedAt,
			Payload: labMonitoringModel.SanitizePayload(item.Payload),
		})
	}
	return out, nil
}

// ListLabMonitoring returns one keyset page of observations. It asks the
// repository for one row more than pageSize to learn whether another page
// exists.
func (u *InfrastructureUseCase) ListLabMonitoring(ctx context.Context, eventID, teamID uuid.NullUUID, from, to, cursorAt time.Time, cursorID uuid.UUID, pageSize int32) (Page[LabMonitoringView], error) {
	items, err := u.observations.ListPlatform(ctx, eventID, teamID, from, to, cursorAt, cursorID, pageSize+1)
	if err != nil {
		return Page[LabMonitoringView]{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list laboratory monitoring").Err()
	}
	hasMore := int32(len(items)) > pageSize
	if hasMore {
		items = items[:pageSize]
	}
	views := labMonitoringViews(items)
	return newPage(views, hasMore, func(view LabMonitoringView) uuid.UUID { return view.ID }), nil
}

// CurrentCapacityMonitoring returns one current cluster-capacity sample per
// agent. This is deliberately platform-only: it describes shared cluster
// headroom rather than an event's own laboratories.
func (u *InfrastructureUseCase) CurrentCapacityMonitoring(ctx context.Context) ([]CapacityMonitoringView, error) {
	items, err := u.observations.LatestCapacity(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to load laboratory capacity monitoring").Err()
	}
	return capacityMonitoringViews(items), nil
}

func (u *InfrastructureUseCase) ListCapacityMonitoring(ctx context.Context, from, to, cursorAt time.Time, cursorID uuid.UUID, pageSize int32) (Page[CapacityMonitoringView], error) {
	items, err := u.observations.ListCapacity(ctx, from, to, cursorAt, cursorID, pageSize+1)
	if err != nil {
		return Page[CapacityMonitoringView]{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list laboratory capacity monitoring").Err()
	}
	hasMore := int32(len(items)) > pageSize
	if hasMore {
		items = items[:pageSize]
	}
	return newPage(capacityMonitoringViews(items), hasMore, func(view CapacityMonitoringView) uuid.UUID { return view.ID }), nil
}

func newPage[T any](items []T, hasMore bool, id func(T) uuid.UUID) Page[T] {
	page := Page[T]{Items: items, HasMore: hasMore}
	if hasMore && len(items) > 0 {
		next := id(items[len(items)-1])
		page.NextCursor = &next
	}
	return page
}

func labMonitoringViews(items []labMonitoringModel.NamedObservation) []LabMonitoringView {
	out := make([]LabMonitoringView, 0, len(items))
	for _, item := range items {
		out = append(out, LabMonitoringView{
			ID: item.ID, EventID: item.EventID, EventName: item.EventName, EventTeamID: item.EventTeamID, TeamName: item.TeamName, Moderators: item.Moderators,
			LabGroupName: item.LabGroupName, AgentID: item.AgentID, Sequence: item.Sequence, ObservedAt: item.ObservedAt, ReceivedAt: item.ReceivedAt,
			SchemaVersion: item.SchemaVersion, Snapshot: item.Snapshot, Payload: labMonitoringModel.SanitizePayload(item.Payload),
		})
	}
	return out
}

func capacityMonitoringViews(items []labMonitoringModel.CapacityObservation) []CapacityMonitoringView {
	out := make([]CapacityMonitoringView, 0, len(items))
	for _, item := range items {
		out = append(out, CapacityMonitoringView{ID: item.ID, AgentID: item.AgentID, Sequence: item.Sequence, ObservedAt: item.ObservedAt, ReceivedAt: item.ReceivedAt, SchemaVersion: item.SchemaVersion, Snapshot: item.Snapshot, Payload: item.Payload})
	}
	return out
}
