package eventLabModel

import exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"

type Policy struct {
	SnapshotMode         string
	MaxActiveLabsPerTeam *int32
	RetentionMinutes     int32
}

func DefaultPolicy() Policy { return Policy{SnapshotMode: "skip", RetentionMinutes: 60} }
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
