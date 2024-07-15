package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	IChallengeCategoryService interface {
		GetEventCategories(ctx context.Context, eventID uuid.UUID) ([]*model.ChallengeCategory, error)
		CreateEventCategory(ctx context.Context, category model.ChallengeCategory) error
		UpdateEventCategory(ctx context.Context, category model.ChallengeCategory) error
		DeleteEventCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error
		UpdateEventCategoriesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error
	}
)

func (u *EventUseCase) GetEventCategories(ctx context.Context, eventID uuid.UUID) ([]*model.ChallengeCategory, error) {
	categories, err := u.service.GetEventCategories(ctx, eventID)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get event categories")
	}
	return categories, nil
}

func (u *EventUseCase) CreateEventCategory(ctx context.Context, category model.ChallengeCategory) error {
	if err := u.service.CreateEventCategory(ctx, category); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to create event category")
	}
	return nil
}

func (u *EventUseCase) UpdateEventCategory(ctx context.Context, category model.ChallengeCategory) error {
	if err := u.service.UpdateEventCategory(ctx, category); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to update event category")
	}
	return nil
}

func (u *EventUseCase) DeleteEventCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error {
	if err := u.service.DeleteEventCategory(ctx, eventID, categoryID); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete event category")
	}
	return nil
}

func (u *EventUseCase) UpdateEventCategoriesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {
	if err := u.service.UpdateEventCategoriesOrder(ctx, eventID, orders); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to update event categories order")
	}
	return nil
}
