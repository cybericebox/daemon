package event

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

type (
	IChallengeCategoryService interface {
		GetEventChallengeCategories(ctx context.Context, eventID uuid.UUID) ([]*eventModel.ChallengeCategory, error)
		CreateEventChallengeCategory(ctx context.Context, category eventModel.ChallengeCategory) error
		UpdateEventChallengeCategory(ctx context.Context, category eventModel.ChallengeCategory) error
		DeleteEventChallengeCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error
		UpdateEventChallengeCategoriesOrder(ctx context.Context, orders []eventModel.Order) error
	}
)

// for administrators

func (u *EventUseCase) GetEventCategories(ctx context.Context, eventID uuid.UUID) (
	[]*eventModel.ChallengeCategory,
	error,
) {
	categories, err := u.service.GetEventChallengeCategories(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event categories").Err()
	}
	return categories, nil
}

func (u *EventUseCase) CreateEventCategory(ctx context.Context, category eventModel.ChallengeCategory) error {
	if err := u.service.CreateEventChallengeCategory(ctx, category); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create event category").Err()
	}
	return nil
}

func (u *EventUseCase) UpdateEventCategory(ctx context.Context, category eventModel.ChallengeCategory) error {
	if err := u.service.UpdateEventChallengeCategory(ctx, category); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update event category").Err()
	}
	return nil
}

func (u *EventUseCase) DeleteEventCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error {
	if err := u.service.DeleteEventChallengeCategory(ctx, eventID, categoryID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event category").Err()
	}
	return nil
}

func (u *EventUseCase) UpdateEventCategoriesOrder(
	ctx context.Context,
	orders []eventModel.Order,
) error {
	if err := u.service.UpdateEventChallengeCategoriesOrder(ctx, orders); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update event categories order").Err()
	}
	return nil
}

//
