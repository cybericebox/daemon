package infrastructure

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// Agent sources: an agent an administrator enrolled, or the agent bootstrapped from the deployment
// config (AGENT_ENDPOINT with a one-time enrollment token). Both are ordinary enrolled agents kept in the
// database; the source only says how the agent was added.
const (
	AgentSourceEnv   = "env"
	AgentSourceAdmin = "admin"
)

// AgentRegistration is an agent known to the platform. Secrets are never part of it.
type AgentRegistration struct {
	ID         uuid.UUID
	Key        string
	Name       string
	Configured bool
	CreatedAt  time.Time
	UpdatedAt  time.Time
	// Source is AgentSourceEnv or AgentSourceAdmin.
	Source string
	// Endpoint is host:port of the agent.
	Endpoint string
	// Enabled agents receive new lab groups; a disabled one only serves the groups it holds.
	Enabled bool
	// Priority orders agents: the smaller is placed first, ties by name.
	Priority int
	// HasCA is true when a CA for the agent's server certificate is stored.
	HasCA bool
	// Tenant is the tenant name of an enrolled agent: the CN of its client certificate, also the
	// issuer of the lab access tokens. Empty for the environment agent.
	Tenant string
	// AccessKeyID is the id (kid) of the key that signs the lab access tokens now.
	AccessKeyID string
	// CertNotAfter is the end of the client certificate's validity; nil when unknown.
	CertNotAfter *time.Time
	// The last known capacity (the tenant quota), kept while the agent is offline. CapacitySeenAt is nil
	// for an agent that was never seen (no capacity); with it set, a nil value means no limit on that
	// resource.
	CapacityCPUMillicores *int64
	CapacityMemoryBytes   *int64
	CapacitySeenAt        *time.Time
	// MaxDevice is the largest device the agent last reported it can place (per resource, the largest allocatable of a
	// lab node net of the platform reserve); nil for an agent that does not report it. The agent never reveals its nodes.
	MaxDevice *AgentDevice
	// Maintenance is the windows the cluster operator announced on the agent, as it last reported them (the ones that
	// apply to the platform's tenant). MaintenanceReported is false for an agent that has not reported them (an older
	// agent, or the CRD is not installed).
	Maintenance         []AgentMaintenanceWindow
	MaintenanceReported bool
	// Features is what the agent last reported the tenant can use (persistence, image cache, scheduler,
	// endpoints, certificate); nil until the first report. FeaturesAt is when it was read.
	Features   *AgentFeatures
	FeaturesAt *time.Time
	// ArchivedAt is set for a deleted agent: the record stays for history, its keys and endpoint are gone.
	ArchivedAt *time.Time
}

// AgentDevice is the largest device an agent can place.
type AgentDevice struct {
	CPUMillicores int64 `json:"cpu_millicores"`
	MemoryBytes   int64 `json:"memory_bytes"`
}

// AgentMaintenanceWindow is a maintenance window announced on an agent by the cluster operator (never by the platform
// admin). In it the agent gives the platform no capacity, or the capacity the window leaves.
type AgentMaintenanceWindow struct {
	Name   string    `json:"name"`
	Reason string    `json:"reason,omitempty"`
	From   time.Time `json:"from"`
	// To is nil for a window without an end.
	To         *time.Time `json:"to,omitempty"`
	AllTenants bool       `json:"all_tenants"`
	// HasCapacity is false when the window leaves nothing; otherwise the capacity it leaves (a resource it does not
	// name is zero).
	HasCapacity   bool  `json:"has_capacity"`
	CPUMillicores int64 `json:"cpu_millicores"`
	MemoryBytes   int64 `json:"memory_bytes"`
}

// AgentFeatures is what a laboratory agent offers the platform's tenant. The platform keeps no copy of
// these settings in its own configuration: it stores the last report of each agent.
type AgentFeatures struct {
	Lifecycle   LifecycleCapabilities `json:"lifecycle"`
	Persistence PersistenceFeature    `json:"persistence"`
	ImageCache  ImageCacheFeature     `json:"image_cache"`
	Scheduler   SchedulerFeature      `json:"scheduler"`
	Endpoints   EndpointsFeature      `json:"endpoints"`
	Certificate CertificateFeature    `json:"certificate"`
	Proxy       ProxyFeature          `json:"proxy"`
	Limits      LimitsFeature         `json:"limits"`
	// TenantQuota is the cluster owner's limit for this platform; the calendar's capacity comes from it.
	TenantQuota TenantQuotaFeature `json:"tenant_quota"`
	// Reservations is how the agent's reservations work.
	Reservations ReservationsFeature `json:"reservations"`
	// Checks, Terminal and Bootstrap are the optional parts of contract 1.5 (nil: the agent reports none, it is older than the
	// fields: nothing of it is offered).
	Checks    *ChecksFeature    `json:"checks,omitempty"`
	Terminal  *TerminalFeature  `json:"terminal,omitempty"`
	Bootstrap *BootstrapFeature `json:"bootstrap,omitempty"`
	// VPN is the VPN gateway option of the agent (nil: an older agent, which neither enforces the block of lab-initiated
	// connections nor accepts the key that allows them).
	VPN *VPNFeature `json:"vpn,omitempty"`
	// PersistenceUnavailable and LifecycleUnavailable say why a feature the cluster is configured for is held back
	// (nil: nothing is). Only a fixed code and a count of nodes: never node names, paths or text of the agent.
	PersistenceUnavailable *UnavailableReason `json:"persistence_unavailable,omitempty"`
	LifecycleUnavailable   *UnavailableReason `json:"lifecycle_unavailable,omitempty"`
}

// ChecksFeature: lab checks are switched on and their limits. Terminal: the browser terminal is switched on.
// Bootstrap: the limits of a device bootstrap (it is always available on a 1.5 agent).
type (
	ChecksFeature struct {
		Enabled             bool  `json:"enabled"`
		MaxChecksPerLab     int32 `json:"max_checks_per_lab"`
		MaxPartsPerCheck    int32 `json:"max_parts_per_check"`
		MaxTimeLimitSeconds int32 `json:"max_time_limit_seconds"`
		MaxScheduleSeconds  int32 `json:"max_schedule_seconds"`
		MaxDetailBytes      int32 `json:"max_detail_bytes"`
	}
	TerminalFeature struct {
		Enabled bool `json:"enabled"`
	}
	BootstrapFeature struct {
		MaxScriptBytes      int32 `json:"max_script_bytes"`
		MaxFiles            int32 `json:"max_files"`
		MaxFileBytes        int32 `json:"max_file_bytes"`
		MaxTotalBytes       int32 `json:"max_total_bytes"`
		MaxTimeLimitSeconds int32 `json:"max_time_limit_seconds"`
		MaxAttempts         int32 `json:"max_attempts"`
	}
)

// VPNFeature says the agent enforces the block of connections the lab starts to VPN clients and accepts spec.vpn.reverseConnections.
type VPNFeature struct {
	ReverseConnections bool `json:"reverse_connections"`
}

// EnforcesVPNReverse says the agent has reported that it enforces the block (and so accepts the key).
func (f AgentFeatures) EnforcesVPNReverse() bool { return f.VPN != nil && f.VPN.ReverseConnections }

// RequireLabOptions refuses a lab that needs an option or a size this laboratory does not offer, instead of starting a lab
// that would not work: a task with checks needs the checks option, a device with a terminal the terminal option, a bootstrap
// or a service command an agent that carries them at all (contract 1.5), and every size must be within the agent's limits.
// The error's public context "options" names every option or limit that fails, for example "checks",
// "terminal", "bootstrap.max_files (at most 8)". A limit of 0 is "not reported" and refuses nothing.
func (f AgentFeatures) RequireLabOptions(n exerciseModel.LabNeeds) error {
	var refused []string
	over := func(name string, need int, limit int32) {
		if limit > 0 && need > int(limit) {
			refused = append(refused, name+" (at most "+strconv.Itoa(int(limit))+")")
		}
	}
	if n.Checks {
		if f.Checks == nil || !f.Checks.Enabled {
			refused = append(refused, "checks")
		} else {
			over("checks.max_checks_per_lab", n.ChecksPerLab, f.Checks.MaxChecksPerLab)
			over("checks.max_parts_per_check", n.MaxParts, f.Checks.MaxPartsPerCheck)
			over("checks.max_time_limit_seconds", n.MaxCheckTimeLimit, f.Checks.MaxTimeLimitSeconds)
			over("checks.max_schedule_seconds", n.MaxCheckSchedule, f.Checks.MaxScheduleSeconds)
		}
	}
	if n.Terminal && (f.Terminal == nil || !f.Terminal.Enabled) {
		refused = append(refused, "terminal")
	}
	if n.Bootstrap {
		if f.Bootstrap == nil {
			refused = append(refused, "bootstrap")
		} else {
			over("bootstrap.max_files", n.MaxFiles, f.Bootstrap.MaxFiles)
			over("bootstrap.max_script_bytes", n.MaxScriptBytes, f.Bootstrap.MaxScriptBytes)
			over("bootstrap.max_file_bytes", n.MaxFileBytes, f.Bootstrap.MaxFileBytes)
			over("bootstrap.max_total_bytes", n.MaxBootstrapBytes, f.Bootstrap.MaxTotalBytes)
			over("bootstrap.max_time_limit_seconds", n.MaxBootstrapLimit, f.Bootstrap.MaxTimeLimitSeconds)
		}
	}
	if len(refused) == 0 {
		return nil
	}
	return exerciseModel.ErrLabOptionUnavailable.WithPublicContext("options", strings.Join(refused, "; ")).Err()
}

// UnavailableReason is why some lab nodes of an agent do not confirm a feature: a code of the fixed list, how many
// nodes do not confirm it and how many were asked.
type UnavailableReason struct {
	Code        string `json:"code"`
	NodesFailed int    `json:"nodes_failed"`
	NodesAsked  int    `json:"nodes_asked"`
}

// UnavailableCodes is the allow-list of codes the laboratory agent may report (agent API contract 1.4).
var UnavailableCodes = []string{
	"CONTAINERD_ROOT_MISSING", "SNAPSHOTTER_MISSING", "RUNTIME_SOCKET_UNAVAILABLE", "SNAPSHOT_REGISTRY_UNREACHABLE",
	"CGROUP_V2_MISSING", "PERSISTENCE_DISABLED", "OBSERVATION_DISABLED", "NODE_UNCONFIRMED",
}

// ParseUnavailableReason reads the agent's `<CODE>:<n>/<m>`. Anything else (a free text, an unknown code, a count out
// of range) is dropped: what the agent says is never passed on as it is.
func ParseUnavailableReason(raw string) *UnavailableReason {
	code, counts, found := strings.Cut(raw, ":")
	if !found || !slices.Contains(UnavailableCodes, code) {
		return nil
	}
	failedText, askedText, found := strings.Cut(counts, "/")
	if !found || len(failedText) > 6 || len(askedText) > 6 {
		return nil
	}
	failed, errFailed := strconv.Atoi(failedText)
	asked, errAsked := strconv.Atoi(askedText)
	if errFailed != nil || errAsked != nil || failed < 0 || asked < 1 || failed > asked || failedText[0] == '+' || askedText[0] == '+' {
		return nil
	}
	return &UnavailableReason{Code: code, NodesFailed: failed, NodesAsked: asked}
}

// ReservationsFeature: every group and lab runs under a reservation (Required), and a reservation turns Ending
// NoticeSeconds before its end.
type ReservationsFeature struct {
	Required      bool  `json:"required"`
	NoticeSeconds int64 `json:"ending_notice_seconds"`
}

// TenantQuotaFeature is the tenant quota; Has* false means no limit on that resource.
type TenantQuotaFeature struct {
	HasCPU        bool  `json:"has_cpu"`
	CPUMillicores int64 `json:"cpu_millicores"`
	HasMemory     bool  `json:"has_memory"`
	MemoryBytes   int64 `json:"memory_bytes"`
}

// PersistenceFeature is device state persistence for the tenant.
type PersistenceFeature struct {
	// Available is true when the cluster enables persistence and the tenant is allowed; only then a
	// topology may ask for it.
	Available        bool     `json:"available"`
	DefaultDebounce  int64    `json:"default_debounce_ms"`
	WriteQuotaBytes  int64    `json:"write_quota_bytes"`
	MaxFileSizeBytes int64    `json:"max_file_size_bytes"`
	ExcludedPaths    []string `json:"excluded_paths,omitempty"`
}

// ImageCacheFeature is the agent's platform image cache.
type ImageCacheFeature struct {
	Enabled    bool     `json:"enabled"`
	Registries []string `json:"registries,omitempty"`
}

// SchedulerFeature is the agent's launch queue; MaxPods 0 means no limit.
type SchedulerFeature struct {
	Enabled bool  `json:"enabled"`
	MaxPods int32 `json:"max_pods"`
}

// EndpointsFeature are the hosts the agent hands out.
type EndpointsFeature struct {
	LabsDomain  string `json:"labs_domain"`
	VPNEndpoint string `json:"vpn_endpoint"`
}

// ProxyFeature are the limits of the laboratory L7 proxy for web links: the longest a link can be opened
// and the longest a session lives (the proxy cuts a longer one).
type ProxyFeature struct {
	AccessTokenMaxTTLSeconds int64 `json:"access_token_max_ttl_seconds"`
	SessionMaxTTLSeconds     int64 `json:"session_max_ttl_seconds"`
	// SessionIdleTTLSeconds is how long a proxy session lives without use; the proxy extends it by itself, never
	// past the session end the link states.
	SessionIdleTTLSeconds int64 `json:"session_idle_ttl_seconds"`
}

// CertificateFeature is the client certificate of the platform's connection and the lifetime of the ones
// the agent issues now.
type CertificateFeature struct {
	// NotAfterUnix is 0 without a client certificate.
	NotAfterUnix     int64 `json:"not_after_unix"`
	IssuedTTLSeconds int64 `json:"issued_ttl_seconds"`
}

// ArchivedName is the name of a deleted agent: the archive time makes the name free to be used again.
func ArchivedName(name string, at time.Time) string {
	return name + " (archived " + at.UTC().Format("2006-01-02 15:04") + ")"
}

// RetiredKey is a rotated-out access key: the agent still trusts it until RemoveAccessKey, which
// waits until the longest token signed with it has expired.
type RetiredKey struct {
	KeyID     string    `json:"key_id"`
	RetiredAt time.Time `json:"retired_at"`
}

// AgentRecord is an enrolled admin agent with its stored material: the client certificate (public),
// and the client key and the access signing key as ciphertext bound to the agent id.
type AgentRecord struct {
	AgentRegistration
	CertPEM                    string
	KeyCiphertext              string
	CAPEM                      string
	AccessPrivateKeyCiphertext string
	AccessPublicKey            string
	RetiredAccessKeys          []RetiredKey
}

// Label keys the platform puts on every object it creates in the infrastructure. The instance
// label is immutable and ties the object to this platform instance (the agent's Monitoring
// stream is subscribed with it); the others select objects by event, team or task.
// The prefix "platform/" is not a domain (the backend is not tied to one) but keeps the keys unique and is a valid
// Kubernetes label key prefix.
const (
	LabelInstance = "platform/instance"
	LabelEvent    = "platform/event"
	LabelTeam     = "platform/team"
	LabelTask     = "platform/task"
	LabelVersion  = "platform/version"
	LabelKind     = "platform/kind"
)

// Kinds of a lab for LabelKind.
const (
	KindStand = "stand"
	KindTest  = "test"
)

// LabGroupInfo is one lab group as the platform sees it in the infrastructure (the orphan sweep).
type LabGroupInfo struct {
	// Agent is the registry id of the agent that holds the group.
	Agent  uuid.UUID
	Name   string
	Labels map[string]string
	// CreatedAt is zero when the agent does not report it.
	CreatedAt time.Time
	Phase     string
}

// LabGroupSweeper is the optional agent capability behind the orphan lab group sweep.
type LabGroupSweeper interface {
	// ListSweepableGroups lists, on every agent, the groups of kind stand or test that this platform
	// instance created. The agent answers only for the tenant of the client certificate. An agent that
	// cannot be read is skipped (its groups are simply not offered), and failed counts those agents.
	ListSweepableGroups(ctx context.Context) (groups []LabGroupInfo, failed int, err error)
	// DestroyLabGroupOn deletes the group on the agent that was listed with it. A group that is gone is fine.
	DestroyLabGroupOn(ctx context.Context, agent uuid.UUID, group string) error
}

// LabInitialLifecycle is the persisted intent of a newly created managed Lab.
// UID is acquired from the created object and never guessed before Create.
type LabInitialLifecycle struct {
	OperationID uuid.UUID
	Revision    int64
}

// LabMeta is what a deploy tells the infrastructure about a lab besides its topology.
type LabCreateDispatch struct{ GroupUID, DefinitionHash string }
type LabMeta struct {
	BeforeCreate     func(context.Context, LabCreateDispatch) error
	InitialLifecycle *LabInitialLifecycle
	// Labels go onto the lab (the instance label is added by the adapter); GroupLabels onto its group.
	Labels      map[string]string
	GroupLabels map[string]string
	// DeployGroup groups labs the scheduler dispatches together, one lab after another (the task key
	// for events whose tasks appear at the same time for every team). Empty = independent.
	DeployGroup string
	// PullCredentials are the registry credentials the agent pulls this lab's images with (never logged).
	PullCredentials []RegistryCredential
	// Checks are the laboratory checks of the lab's state tasks, resolved for this instance (secret material: expected
	// values; never logged, never returned). Empty: the lab has none.
	Checks []exerciseModel.LabCheck
	// Reservation is what the lab runs under, GroupReservation what its group runs under (independent: a test lab
	// runs under its run while the author's shared group runs under the group part).
	Reservation      ReservationRef
	GroupReservation ReservationRef
	// PartID and GroupPartID are the resolved part ids the fleet sends; callers leave them empty.
	PartID      string
	GroupPartID string
	// AgentEnforcesVPNReverse is set by the fleet from the agent's features: only then may the key of the VPN gateway option
	// be sent (an older agent rejects it). Callers leave it empty.
	AgentEnforcesVPNReverse bool
}

// Labels of a lab that has a VPN. LabelVPNReverse: "1" when its exercise allows lab-initiated connections to the VPN clients,
// "0" when it does not. LabelVPNReverseSent: "1" when the key that allows them was really requested from the laboratory, "0"
// when it was not (the default of the laboratory then applies). The status is derived from what was requested.
const (
	LabelVPNReverse     = "cybericebox.com/vpn-reverse"
	LabelVPNReverseSent = "cybericebox.com/vpn-reverse-sent"
)

// ImagePrewarm is the state of one image in the platform image cache.
type ImagePrewarm struct {
	Image string
	// State is queued, warming, done, failed or skipped (the registry is not served by the cache).
	State  string
	Error  string
	Digest string
}

// Image prewarm states.
const (
	PrewarmQueued  = "queued"
	PrewarmWarming = "warming"
	PrewarmDone    = "done"
	PrewarmFailed  = "failed"
	PrewarmSkipped = "skipped"
)

// ImagePrewarmer is the optional agent capability that fills the image cache before a burst of labs.
// The call is asynchronous and idempotent: repeat it to poll.
type ImagePrewarmer interface {
	PrewarmImages(ctx context.Context, images []string, creds []RegistryCredential) ([]ImagePrewarm, error)
}

// AvailabilityReporter is implemented by an agent port that can have no agent behind it (the fleet
// before any agent is configured).
type AvailabilityReporter interface {
	Available() bool
}

// DeviceController is the optional agent capability behind the organizer actions on one device
// of a lab with device state persistence. Errors are already API errors (ErrDeviceNotFound,
// ErrDeviceNotPersistent, ErrDeviceActionRetry).
type DeviceController interface {
	// ResetDevice discards the device's snapshots and restarts it from its base image.
	ResetDevice(ctx context.Context, group, lab, device string) error
	// RescueDevice starts the device from its latest snapshot with a shell (enable) or back to normal.
	RescueDevice(ctx context.Context, group, lab, device string, enable bool) error
}

// AgentSecretContext binds the ciphertext of one private key of an agent to the agent: a value
// copied to another row fails to open. field is "key" (the mTLS client key) or "access" (the signing
// key of the lab access tokens).
func AgentSecretContext(id uuid.UUID, field string) []byte {
	return []byte("infra.agent:" + id.String() + ":" + field)
}

// ErrEnrollmentDenied is what the agent adapter returns when the agent refuses the enrollment token or
// the request (a used, expired or unknown token is PERMISSION_DENIED).
var ErrEnrollmentDenied = errors.New("the agent denied the enrollment")

type LifecycleCapabilities struct{ PerLabStop, RequiredSnapshot, ConfirmedRuntime, RetainedRestart, FullGroupStop bool }

// AgentProfileReport is the stored node profile report of an agent: the report as the daemon encoded it, and when it
// was received.
type AgentProfileReport struct {
	AgentID    uuid.UUID
	Data       []byte
	ReceivedAt time.Time
}
