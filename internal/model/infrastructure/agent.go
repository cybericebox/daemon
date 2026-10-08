package infrastructure

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
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
const (
	LabelInstance = "cybericebox.io/instance"
	LabelEvent    = "cybericebox.io/event"
	LabelTeam     = "cybericebox.io/team"
	LabelTask     = "cybericebox.io/task"
	LabelVersion  = "cybericebox.io/version"
	LabelKind     = "cybericebox.io/kind"
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
type LabMeta struct {
	InitialLifecycle *LabInitialLifecycle
	// Labels go onto the lab (the instance label is added by the adapter); GroupLabels onto its group.
	Labels      map[string]string
	GroupLabels map[string]string
	// DeployGroup groups labs the scheduler dispatches together, one lab after another (the task key
	// for events whose tasks appear at the same time for every team). Empty = independent.
	DeployGroup string
}

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
	PrewarmImages(ctx context.Context, images []string) ([]ImagePrewarm, error)
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
