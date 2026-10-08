package event

import (
	"context"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
)

// LabLifecycleInfrastructure accepts intent separately from certified runtime
// observations. A nil command error is never an allocation acknowledgement.
type LabLifecycleInfrastructure interface {
	StopLab(context.Context, eventLabModel.StopRequest) error
	StartLab(context.Context, eventLabModel.Target) error
	ObserveLab(context.Context, eventLabModel.Ref) (eventLabModel.Observation, error)
}
