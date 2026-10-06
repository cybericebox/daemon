package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

// SetEventInfrastructure lets a platform administrator change the event's
// infrastructure flag before publication. Route gate: events.write (audited
// by the gate). Allowing needs a connected Laboratory; turning it off is
// refused while attached sets need infrastructure.
func (u *EventUseCase) SetEventInfrastructure(ctx context.Context, id uuid.UUID, allowed bool, by uuid.UUID) (EventView, error) {
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	now := time.Now()
	if allowed != e.InfrastructureAllowed {
		if allowed {
			if u.infrastructureCapability == nil || u.infrastructureCapability.RequireLaboratories(ctx) != nil {
				return EventView{}, eventModel.ErrEventInfrastructureUnavailable.Err()
			}
		}
	}
	attached := 0
	if e.InfrastructureAllowed && !allowed {
		if attached, err = u.eventExercises.CountInfrastructure(ctx, id); err != nil {
			return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count infrastructure sets").Err()
		}
	}
	expected := e.UpdatedAt
	if err = e.SetInfrastructureAllowed(allowed, attached, by, now); err != nil {
		return EventView{}, err
	}
	if e.UpdatedAt.Equal(expected) {
		return toEventView(e, now), nil
	}
	affected, err := u.events.UpdateInfrastructure(ctx, e, expected)
	if err != nil {
		return EventView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update event infrastructure").Err()
	}
	if affected == 0 {
		if _, err = u.events.GetByID(ctx, id); err != nil {
			return EventView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventView{}, eventModel.ErrEventModified.Err()
	}
	return toEventView(e, now), nil
}
