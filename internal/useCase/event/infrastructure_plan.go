package event

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// EventInfrastructurePlan is a derived, non-secret preflight projection. It
// lets both manager and participant UI establish whether a future/current event
// needs laboratories or VPN before any team-specific lab is provisioned.
type EventInfrastructurePlan struct {
	HasDynamicLabs        bool
	LaboratoriesAvailable bool
	RequiresVPN           bool
	CanStart              bool
	Reason                *string
}

func infrastructurePlan(topologies []exerciseModel.Topology, laboratoriesAvailable bool) EventInfrastructurePlan {
	plan := EventInfrastructurePlan{CanStart: true, LaboratoriesAvailable: laboratoriesAvailable}
	for _, topology := range topologies {
		if len(topology.Devices) == 0 {
			continue
		}
		plan.HasDynamicLabs = true
		if topology.VPN.Enabled {
			plan.RequiresVPN = true
		}
	}
	if plan.HasDynamicLabs && !laboratoriesAvailable {
		plan.CanStart = false
		reason := "infrastructure_unavailable"
		plan.Reason = &reason
	}
	return plan
}

func (u *EventUseCase) eventInfrastructurePlan(ctx context.Context, eventID uuid.UUID) (EventInfrastructurePlan, error) {
	// Production wiring always supplies the capability port. Keeping a static
	// fallback makes this aggregate safe for narrow/internal construction (for
	// example migration tools) rather than issuing database work against a
	// partially wired use case.
	if u.infrastructureCapability == nil {
		return EventInfrastructurePlan{CanStart: true}, nil
	}
	topologies, err := u.activeEventTopologies(ctx, eventID)
	if err != nil {
		return EventInfrastructurePlan{}, err
	}
	available := u.infrastructureCapability != nil && u.infrastructureCapability.RequireLaboratories(ctx) == nil
	return infrastructurePlan(topologies, available), nil
}

// requireEventLaboratories is the single runtime gate for an event. It runs
// before any command writes lab bindings, materializes a team assignment or
// queues access work. Static events deliberately pass without an agent.
func (u *EventUseCase) requireEventLaboratories(ctx context.Context, eventID uuid.UUID) (EventInfrastructurePlan, error) {
	plan, err := u.eventInfrastructurePlan(ctx, eventID)
	if err != nil || !plan.HasDynamicLabs {
		return plan, err
	}
	if err := u.infrastructureCapability.RequireLaboratories(ctx); err != nil {
		return plan, err
	}
	return plan, nil
}

// activeEventTopologies resolves the pinned version topologies of the event's
// active (not superseded) exercise attachments.
func (u *EventUseCase) activeEventTopologies(ctx context.Context, eventID uuid.UUID) ([]exerciseModel.Topology, error) {
	attachments, err := u.eventExercises.List(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event exercises for infrastructure plan").Err()
	}
	topologies := make([]exerciseModel.Topology, 0)
	for _, attachment := range attachments {
		if attachment.Status != eventExerciseModel.StatusActive || attachment.SupersededAt != nil {
			continue
		}
		resolver, ok := u.topologies.(VersionTopologyResolver)
		if !ok {
			return nil, model.ErrPlatform.WithMessage("Topology resolver is not configured").Err()
		}
		items, err := resolver.ResolveVersionTopologies(ctx, attachment.ExerciseVersionID)
		if err != nil {
			return nil, err
		}
		topologies = append(topologies, items...)
	}
	return topologies, nil
}

// hasInfrastructureChallenges reports whether the event actually carries lab
// challenges: the admin allowed infrastructure AND an active exercise has a
// topology with devices (the same test the attach-time infra check uses).
func (u *EventUseCase) hasInfrastructureChallenges(ctx context.Context, e eventModel.Event) (bool, error) {
	if !e.InfrastructureAllowed {
		return false, nil
	}
	topologies, err := u.activeEventTopologies(ctx, e.ID)
	if err != nil {
		return false, err
	}
	return infrastructurePlan(topologies, true).HasDynamicLabs, nil
}
