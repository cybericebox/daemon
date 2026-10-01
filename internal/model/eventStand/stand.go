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
	DefaultDeployLeadMinutes    int32 = 30
	DefaultTeardownDelayMinutes int32 = 60
	minDeployLeadMinutes        int32 = 5
	maxDeployLeadMinutes        int32 = 1440
	maxTeardownDelayMinutes     int32 = 10080

	// DeployTimeout fails a Lab that the agent accepted but never reported
	// ready, so a stuck image pull or crash loop reaches the moderators.
	DeployTimeout = 20 * time.Minute
	// reasonMaxLen keeps agent error text readable in lists and notifications.
	reasonMaxLen = 300
)

// Timing is the event's stand schedule: deploy N minutes before the start and
// tear down N minutes after the effective finish.
type Timing struct {
	DeployLeadMinutes    int32
	TeardownDelayMinutes int32
}

func DefaultTiming() Timing {
	return Timing{DeployLeadMinutes: DefaultDeployLeadMinutes, TeardownDelayMinutes: DefaultTeardownDelayMinutes}
}

func NewTiming(deployLeadMinutes, teardownDelayMinutes int32) (Timing, error) {
	if deployLeadMinutes < minDeployLeadMinutes || deployLeadMinutes > maxDeployLeadMinutes ||
		teardownDelayMinutes < 0 || teardownDelayMinutes > maxTeardownDelayMinutes {
		return Timing{}, ErrStandSettingsInvalid.Err()
	}
	return Timing{DeployLeadMinutes: deployLeadMinutes, TeardownDelayMinutes: teardownDelayMinutes}, nil
}

// DeployAt is the instant stands start deploying for an event starting at start.
func (t Timing) DeployAt(start time.Time) time.Time {
	return start.Add(-time.Duration(t.DeployLeadMinutes) * time.Minute)
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
}

// Assess derives the stand status: any failed Lab fails the stand, any Lab or
// assignment still being prepared keeps it creating, otherwise it is ready. A
// team without infrastructure challenges has an (empty) ready stand.
func Assess(c LabCounters) (Status, string) {
	switch {
	case c.FailedLabs > 0:
		return StatusFailed, Reason(c.FailureReason)
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
	// Queued or some of its pods are not dispatched yet, nothing times out, and the timeout counts
	// from the last dispatch.
	if status.Phase == exerciseModel.DeployPhaseQueued || (status.Queue != nil && status.Queue.Pending > 0) {
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
