package eventLabModel

import (
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"time"
)

type Policy struct {
	SnapshotMode         string
	MaxActiveLabsPerTeam *int32
	RetentionMinutes     int32
}

var defaultPolicy = Policy{SnapshotMode: "skip", RetentionMinutes: 60}

func SetDefaultPolicy(p Policy) {
	defaultPolicy = p
	if p.MaxActiveLabsPerTeam != nil {
		n := *p.MaxActiveLabsPerTeam
		defaultPolicy.MaxActiveLabsPerTeam = &n
	}
}
func DefaultPolicy() Policy {
	p := defaultPolicy
	if p.MaxActiveLabsPerTeam != nil {
		n := *p.MaxActiveLabsPerTeam
		p.MaxActiveLabsPerTeam = &n
	}
	return p
}
func (p Policy) Validate() error {
	if (p.SnapshotMode != "skip" && p.SnapshotMode != "required") || p.RetentionMinutes < 0 || p.RetentionMinutes > 10080 || (p.MaxActiveLabsPerTeam != nil && (*p.MaxActiveLabsPerTeam < 1 || *p.MaxActiveLabsPerTeam > 1000)) {
		return ErrPolicyInvalid.Err()
	}
	return nil
}

// ValidatePreparation refuses an unsupported required barrier before provisioning.
// Capture support comes from the producer feature contract; it is never inferred
// from ordinary snapshot or pod-exit support.
func (p Policy) ValidatePreparation(t exerciseModel.Topology, captureSupported bool) error {
	if err := p.Validate(); err != nil {
		return err
	}
	if p.SnapshotMode != "required" {
		return nil
	}
	missing := !captureSupported
	for _, d := range t.Devices {
		if d.Type == exerciseModel.DeviceTypeContainer && (d.Persistence == nil || !d.Persistence.Enabled) {
			missing = true
		}
	}
	if missing {
		return ErrCaptureUnavailable.Err()
	}
	return nil
}

func (l *Lab) ManualCapabilities(progressive, reachable bool, now time.Time) (stop, restart bool) {
	if !progressive || !reachable || l.CloseReason == "solved" || l.DesiredState == "Deleted" {
		return false, false
	}
	retained := l.Allocation.StorageState == "Retained" || l.Allocation.StorageState == "None"
	deadline := l.RetentionUntil
	if l.ProtectedUntil != nil && (deadline == nil || l.ProtectedUntil.After(*deadline)) {
		deadline = l.ProtectedUntil
	}
	available := deadline == nil || now.Before(*deadline)
	return l.DesiredState == "Running" && l.ClosedAt == nil && l.AgentUID != "", l.DesiredState == "Stopped" && l.CloseReason == "manual" && retained && available && l.ActualState == "Stopped" && l.ObservedRevision == l.Revision && l.Allocation.RuntimeState == "Released" && l.AccessFenced && l.Allocation.ReleasedAt != nil && l.FailureCode == "" && (l.SnapshotMode == "skip" || l.SnapshotState == "Succeeded")
}
func (l *Lab) SetRetentionDeadline(until time.Time, now time.Time) {
	l.RetentionUntil = cloneTime(&until)
	l.UpdatedAt = now
}
