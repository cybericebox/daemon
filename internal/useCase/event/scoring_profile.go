package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

func (u *EventUseCase) GetEventScoringProfile(ctx context.Context, id uuid.UUID) (EventScoringProfileView, error) {
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventScoringProfileView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventScoringProfileView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return EventScoringProfileView{Profile: e.ScoringProfile, ForceEventScoring: e.ForceEventScoring, StaticPoints: e.StaticPoints, UpdatedAt: e.UpdatedAt}, nil
}

func (u *EventUseCase) UpdateEventScoringProfile(ctx context.Context, id uuid.UUID, in UpdateEventScoringProfileInput, by uuid.UUID) (EventScoringProfileView, error) {
	e, err := u.events.GetByID(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventScoringProfileView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventScoringProfileView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	in.Profile = in.Profile.Normalized()
	if err = in.Profile.ValidateFor(e.Lifecycle); err != nil {
		return EventScoringProfileView{}, err
	}
	expected, now := e.UpdatedAt, time.Now()
	if err = e.UpdateScoringProfile(in.Profile, in.ForceEventScoring, in.StaticPoints, by, now); err != nil {
		return EventScoringProfileView{}, err
	}
	affected, err := u.events.UpdateScoringProfile(ctx, e, expected)
	if err != nil {
		return EventScoringProfileView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update event scoring profile").Err()
	}
	if affected == 0 {
		if _, err = u.events.GetByID(ctx, id); err != nil {
			return EventScoringProfileView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventScoringProfileView{}, eventModel.ErrEventModified.Err()
	}
	return EventScoringProfileView{Profile: e.ScoringProfile, ForceEventScoring: e.ForceEventScoring, StaticPoints: e.StaticPoints, UpdatedAt: e.UpdatedAt}, nil
}
