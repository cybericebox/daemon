// Package eventStandModel owns the rules of a team stand: everything one team
// gets from Laboratory for an event (its LabGroup and one Lab per
// infrastructure challenge). Stands are managed only by moderators and by the
// schedule-driven engine; participants never deploy or reconcile them.
package eventStandModel

import (
	"sort"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// Status is the persisted stand status. StatusNotDeployed is never stored:
// a team without a stand row simply has no stand yet.
type Status int16

const (
	StatusNotDeployed Status = iota
	StatusCreating
	StatusReady
	StatusFailed
	// StatusRemoved is terminal: the stand was torn down after the event.
	StatusRemoved
)

func (s Status) String() string {
	switch s {
	case StatusCreating:
		return "creating"
	case StatusReady:
		return "ready"
	case StatusFailed:
		return "failed"
	case StatusRemoved:
		return "removed"
	default:
		return "not_deployed"
	}
}

const (
	DefaultTeardownDelayMinutes int32 = 60
	maxTeardownDelayMinutes     int32 = 10080

	// reasonMaxLen keeps agent error text readable in lists and notifications.
	reasonMaxLen = 300
)

// ReasonAwaitingReservation is the reason of a creating stand that waits for the event's resource reservation:
// nothing is deployed before it exists, then everything starts by itself.
const ReasonAwaitingReservation = "awaiting_reservation"

// ReasonImageUnavailable prefixes the reason of a Lab that was never deployed
// because one of its images cannot be pulled ("ImageUnavailable: <image>").
const ReasonImageUnavailable = "ImageUnavailable"

// ImageUnavailableReason is the stored failure reason of a Lab refused for image.
func ImageUnavailableReason(image string) string {
	return Reason(ReasonImageUnavailable + ": " + image)
}

// WaitAlarmAfter is how long a Lab may wait for something (room in the reservation, the cluster, the end of a
// deletion, the agent) before the stand reports it to the organizers like a failure.
const WaitAlarmAfter = 3 * time.Minute

// Wait reason codes of a pending Lab. They are stored on the binding and shown to organizers (the texts are the
// frontend's; WaitReasonText is the English fallback of the alarm).
const (
	WaitNotEnoughReserved   = "not_enough_reserved"
	WaitClusterReservation  = "cluster_reservation"
	WaitLabDeletion         = "lab_deletion"
	WaitReplacingStaleLab   = "replacing_stale_lab"
	WaitAgentUnavailable    = "agent_unavailable"
	WaitImagesUnavailable   = "images_unavailable"
	WaitGroupNotAllowed     = "group_not_allowed"
	WaitRevealReservation   = "reveal_reservation"
	WaitLabNotPrepared      = "lab_not_prepared"
	WaitLabSizingUnknown    = "lab_sizing_unknown"
	WaitLabNotAdmitted      = "lab_not_admitted"
	WaitAwaitingReservation = ReasonAwaitingReservation
)

// WaitReasonText is the English sentence of a wait reason code.
func WaitReasonText(code string) string {
	switch code {
	case WaitNotEnoughReserved:
		return "Waiting: the event reservation has no room for this lab"
	case WaitClusterReservation:
		return "Waiting: the team has no room in its cluster reservation yet"
	case WaitLabDeletion:
		return "Waiting: the previous lab of this name is still being deleted"
	case WaitReplacingStaleLab:
		return "Waiting: replacing a stale lab of the same name"
	case WaitAgentUnavailable:
		return "Waiting: the laboratory agent does not answer"
	case WaitImagesUnavailable:
		return "Waiting: the images of the lab are not available yet"
	case WaitGroupNotAllowed:
		return "Waiting: the team group may not start labs now"
	case WaitLabNotPrepared:
		return "Waiting: the lab of the team is not prepared yet"
	case WaitLabSizingUnknown:
		return "Waiting: the size of the lab is not known yet"
	case WaitLabNotAdmitted:
		return "Waiting: the lab has not been admitted by the reservation yet"
	case WaitRevealReservation:
		return "Waiting: the labs of the task are reserved for all teams together"
	default:
		return "Waiting: " + code
	}
}

// RetryBackoff is how long a failed Lab the agent never accepted waits before the next automatic try, after
// attempts earlier retries: 30 s, 1 min, 2 min, 5 min, then every 5 minutes.
func RetryBackoff(attempts int32) time.Duration {
	steps := []time.Duration{30 * time.Second, time.Minute, 2 * time.Minute, 5 * time.Minute}
	if attempts < 0 {
		attempts = 0
	}
	if int(attempts) >= len(steps) {
		return steps[len(steps)-1]
	}
	return steps[attempts]
}

// DeployTimeout fails a Lab that the agent accepted but never reported
// ready, so a stuck image pull or crash loop reaches the moderators
// (EVENT_STAND_DEPLOY_TIMEOUT, set once at start).
var DeployTimeout = 20 * time.Minute

// Timing is the event's stand schedule: tear down N minutes after the effective finish. The deploy lead is not a
// setting: the platform computes it (ComputeLead).
type Timing struct {
	TeardownDelayMinutes int32
}

func DefaultTiming() Timing {
	return Timing{TeardownDelayMinutes: DefaultTeardownDelayMinutes}
}

func NewTiming(teardownDelayMinutes int32) (Timing, error) {
	if teardownDelayMinutes < 0 || teardownDelayMinutes > maxTeardownDelayMinutes {
		return Timing{}, ErrStandSettingsInvalid.Err()
	}
	return Timing{TeardownDelayMinutes: teardownDelayMinutes}, nil
}

// TeardownAt is nil for an event without a finish: its stands live until
// withdrawal, which the laboratory cleanup job already handles.
func (t Timing) TeardownAt(effectiveFinish *time.Time) *time.Time {
	if effectiveFinish == nil {
		return nil
	}
	at := effectiveFinish.Add(time.Duration(t.TeardownDelayMinutes) * time.Minute)
	return &at
}

// LabCounters is the per-team projection the engine assesses after each pass.
type LabCounters struct {
	PendingLabs        int64
	FailedLabs         int64
	FailureReason      string
	MissingAssignments int64
	// StuckLabs are pending Labs that have waited for something other than the reservation for longer than
	// WaitAlarmAfter, StuckReason the code of one of them.
	StuckLabs   int64
	StuckReason string
}

// Assess derives the stand status: any failed Lab fails the stand, any Lab or
// assignment still being prepared keeps it creating, otherwise it is ready. A
// team without infrastructure challenges has an (empty) ready stand.
func Assess(c LabCounters) (Status, string) {
	switch {
	case c.FailedLabs > 0:
		return StatusFailed, Reason(c.FailureReason)
	case c.StuckLabs > 0:
		// A Lab that waits for long without anyone to blame needs a person: the stand reports it like a failure.
		return StatusFailed, Reason(WaitReasonText(c.StuckReason))
	case c.PendingLabs > 0 || c.MissingAssignments > 0:
		return StatusCreating, ""
	default:
		return StatusReady, ""
	}
}

// Outcome is the engine's reading of one Lab observation.
type Outcome int

const (
	OutcomeWaiting Outcome = iota
	OutcomeReady
	OutcomeFailed
)

// Classify reads an agent Lab status. deployedAt is when the agent accepted the
// Lab; a Lab that is still not ready after DeployTimeout (counted from its
// admission when it was queued) is failed with the names of its not ready devices.
func Classify(status exerciseModel.LabDeployStatus, deployedAt, now time.Time) (Outcome, string) {
	if status.Ready {
		return OutcomeReady, ""
	}
	if status.Phase == exerciseModel.DeployPhaseFailed || status.Phase == "Error" {
		return OutcomeFailed, Reason("Laboratory reported phase " + status.Phase + deviceSuffix(status))
	}
	for _, device := range status.Devices {
		if !device.Ready && fatalDeviceReason(device.Reason) {
			return OutcomeFailed, Reason(device.Reason + ": " + device.Name)
		}
		// The scheduler declares a pod failed when it did not start in time and still may become
		// Ready; a plain startup timeout is judged by the deploy timeout below.
		if f := failureOf(device); !device.Ready && f != nil && f.Reason != "StartupTimeout" {
			return OutcomeFailed, Reason(f.Reason + ": " + device.Name + schedulerMessage(f))
		}
	}
	// A lab waiting in the operator's scheduler queue is paced on purpose, not stuck: while it is
	// Queued (a withdrawn lab waiting for its retry too) or some of its pods are not dispatched yet,
	// or its sent pods wait for a node (the operator withdraws the group at its node timeout), nothing
	// times out. The timeout counts from the last dispatch.
	if status.Phase == exerciseModel.DeployPhaseQueued || (status.Queue != nil && (status.Queue.Pending > 0 || status.Queue.Reason == exerciseModel.QueueReasonNodeStarting)) {
		return OutcomeWaiting, ""
	}
	for _, device := range status.Devices {
		if device.Scheduling != nil && device.Scheduling.DispatchedAt.After(deployedAt) {
			deployedAt = device.Scheduling.DispatchedAt
		}
	}
	if now.Sub(deployedAt) >= DeployTimeout {
		return OutcomeFailed, Reason("Not ready after " + DeployTimeout.String() + deviceSuffix(status))
	}
	return OutcomeWaiting, ""
}

func failureOf(device exerciseModel.LabDeployedDevice) *exerciseModel.PodFailure {
	if device.Scheduling == nil {
		return nil
	}
	return device.Scheduling.Failure
}

func schedulerMessage(f *exerciseModel.PodFailure) string {
	if f.Message == "" {
		return ""
	}
	return " (" + f.Message + ")"
}

func fatalDeviceReason(reason string) bool {
	switch reason {
	case "ErrImagePull", "ImagePullBackOff", "CrashLoopBackOff", "CreateContainerConfigError", "InvalidImageName":
		return true
	default:
		return false
	}
}

func deviceSuffix(status exerciseModel.LabDeployStatus) string {
	pending := make([]string, 0, len(status.Devices))
	for _, device := range status.Devices {
		if !device.Ready {
			pending = append(pending, device.Name)
		}
	}
	if len(pending) == 0 {
		return ""
	}
	sort.Strings(pending)
	return "; devices not ready: " + strings.Join(pending, ", ")
}

// Reason normalizes agent text for storage and display.
func Reason(raw string) string {
	reason := strings.Join(strings.Fields(raw), " ")
	if len([]rune(reason)) > reasonMaxLen {
		reason = string([]rune(reason)[:reasonMaxLen-1]) + "…"
	}
	return reason
}

// InfrastructureOpen applies the strict availability barrier: infrastructure
// challenges open only after the start, and only once every admitted team's
// stand (including the moderators stand) has been ready at the same time.
// The barrier is sticky: openNow reports the first opening, to be recorded.
func InfrastructureOpen(started bool, openedAt *time.Time, allReady bool) (open, openNow bool) {
	if openedAt != nil {
		return true, false
	}
	if started && allReady {
		return true, true
	}
	return false, false
}

// ModeratorsTeamName is the stored name of the hidden moderators team. It is
// never displayed (the UI labels the team) and cannot collide with a real
// team because it embeds the event identifier.
func ModeratorsTeamName(eventID uuid.UUID) string {
	return "moderators:" + eventID.String()
}
