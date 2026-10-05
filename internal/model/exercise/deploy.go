package exerciseModel

import (
	"time"

	"github.com/gofrs/uuid"
)

// Deploy-time value types shared by the exercise use case (orchestration and
// placeholder resolution) and the infrastructure adapter (which maps the agent's
// wire types onto them). They describe a deployed variant lab, not a stored
// entity, so they carry no identity or invariants.

// Deploy lifecycle phases, mirroring the infrastructure lab phase. The UI drives
// its deploy screen off these: anything other than Ready/Failed is "in progress".
const (
	DeployPhasePending      = "Pending"
	DeployPhaseProvisioning = "Provisioning"
	DeployPhaseReady        = "Ready"
	DeployPhaseFailed       = "Failed"
	// DeployPhaseQueued: the operator's launch pacing has not admitted the lab yet.
	DeployPhaseQueued = "Queued"
)

// Reasons the scheduler does not dispatch the next pod of a lab, as the operator reports them.
const (
	QueueReasonInFlightLimit        = "InFlightLimit"
	QueueReasonWaitingForGroup      = "WaitingForGroup"
	QueueReasonWaitingForTurn       = "WaitingForTurn"
	QueueReasonPreparingImages      = "PreparingImages"
	QueueReasonInsufficientResource = "InsufficientResources"
	QueueReasonNoSchedulableNodes   = "NoSchedulableNodes"
	// QueueReasonTenantQuota: dispatching the next pod would pass the platform's resource quota.
	QueueReasonTenantQuota = "TenantQuota"
)

// States of one device pod on its way from the scheduler queue to Ready.
const (
	PodStateQueued   = "Queued"
	PodStateStarting = "Starting"
	PodStateStarted  = "Started"
	PodStateFailed   = "Failed"
)

// DeployHandle identifies a deployed variant lab. The group name doubles as the
// externally visible deploy id; the lab and VPN-client names are conventional,
// so the status/teardown paths reconstruct the handle from the group alone.
type DeployHandle struct {
	Group     string
	Lab       string
	Namespace string
	VPNClient string // empty when the variant needs no VPN
	// Flags are the values injected into the linked devices of a test deploy,
	// one per flag-linked task, so the author can compare them.
	Flags []DeployFlag
}

// DeployFlag is the resolved test flag of one device-linked task.
type DeployFlag struct {
	TaskID uuid.UUID `json:"task_id"`
	Name   string    `json:"name"`
	Flag   string    `json:"flag"`
}

// LabDeployStatus is the runtime status of a deployed variant lab.
type LabDeployStatus struct {
	Phase        string
	Ready        bool
	VPNCIDR      string
	InternetCIDR string
	Devices      []LabDeployedDevice
	Access       []LabAccess
	// VPNConfig is the tester's WireGuard config, present once a VPN-enabled lab
	// has provisioned its client. Empty for labs without VPN or before it is ready.
	VPNConfig string
	// Queue is the place in the operator's scheduler queue; nil for a lab the scheduler does not track.
	Queue *LabQueue
	// ImageWarning lists the lab images that could not be pinned to a digest (pulled by tag);
	// GroupImageWarning is the same for the group's VPN or gateway image. Empty when fine.
	ImageWarning      string
	GroupImageWarning string
	// VPNConnected says the author's WireGuard client shook hands recently; VPNLastHandshake is
	// that time (zero when it never connected). Filled by the use case, not the agent view.
	VPNConnected bool
	// CPUMillicores and MemoryBytes are the current use of the whole lab (all its devices summed);
	// UsageAvailable is false when the agent could not measure any device.
	CPUMillicores, MemoryBytes int64
	UsageAvailable             bool
	VPNLastHandshake           time.Time
	// VPNProbeURL is the tester page the group's VPN pod serves inside the tunnel; empty when unknown. Filled by the use case.
	VPNProbeURL string
	// SolvedTasks are the tasks the author has already checked correctly; filled by the use case.
	SolvedTasks []uuid.UUID
}

// LabDeployedDevice is one materialised device's readiness. Reason is the
// container waiting/termination reason reported by the agent (for example
// ImagePullBackOff); empty while the device is healthy or unknown.
type LabDeployedDevice struct {
	Name   string
	Ready  bool
	Reason string
	// Snapshot is set only for a device with state persistence.
	Snapshot *DeviceSnapshot
	// Scheduling is the pod's way through the scheduler; nil when the scheduler does not track it.
	Scheduling *PodScheduling
}

// PodScheduling is where one device pod is between the scheduler queue and Ready. Failure is set
// for a pod that did not start in time; it clears when the pod becomes Ready.
type PodScheduling struct {
	// State is one of the PodState* values.
	State        string
	QueuedAt     time.Time
	DispatchedAt time.Time
	StartedAt    time.Time
	Failure      *PodFailure
}

// PodFailure explains a pod that did not start. Reason is ImagePull, CrashLoop, Unschedulable,
// StartupTimeout or DoesNotFit; Message is the last error the node reported.
type PodFailure struct {
	Reason       string
	Message      string
	RestartCount int32
	At           time.Time
}

// DeviceSnapshot is the state of a device's writable-layer snapshots. A zero time means
// "never".
type DeviceSnapshot struct {
	LastSnapshotAt time.Time
	RestoredAt     time.Time
	SizeBytes      int64
	// Warning is set when the quota is exceeded or snapshots fail; the last good one is kept.
	Warning string
	// Rescue is true while the device runs in rescue mode (a shell instead of the entrypoint).
	Rescue bool
}

// LabQueue is the place of a Lab in the scheduler queue: the objects of the same deploy group are
// dispatched together, one object after another. Position is 1-based among the objects that still
// have pods to dispatch and 0 when all are dispatched; Reason says why the next pod waits and is
// empty when nothing waits. The phase is Queued while none of the lab's pods has been dispatched.
type LabQueue struct {
	Position int32
	Length   int32
	Reason   string
	// Message is the detail of the reason, for example "waiting for group intro".
	Message string
	// Group is the deploy group of the lab (the task for event labs); empty for an independent lab.
	Group string
	// Pods are the pods of the lab and Pending those not dispatched yet.
	Pods, Pending int32
}

// LabAccess is one externally reachable web endpoint of a deployed lab.
type LabAccess struct {
	Device   string
	Port     int32
	Protocol string
	URL      string
}

// WebURL is the web address of one device port, if it has one.
func (s LabDeployStatus) WebURL(device string, port int32) (string, bool) {
	for _, a := range s.Access {
		if a.Device == device && a.Port == port && a.URL != "" {
			return a.URL, true
		}
	}
	return "", false
}
