package infrastructure

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
)

// ConfiguredPrimaryAgentKey is the stable identity of the single agent wired
// from process configuration today. Its connection details deliberately never
// enter the database: they remain deployment secrets/configuration.
const ConfiguredPrimaryAgentKey = "configured-primary"

// Agent sources: the single agent of the deployment environment, or an agent an administrator
// configured. While any admin agent exists the environment agent is not used.
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
	// Endpoint is host:port of an admin agent; empty for the environment one (its connection stays
	// deployment configuration).
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
	// ArchivedAt is set for a deleted agent: the record stays for history, its keys and endpoint are gone.
	ArchivedAt *time.Time
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

// LabMeta is what a deploy tells the infrastructure about a lab besides its topology.
type LabMeta struct {
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
