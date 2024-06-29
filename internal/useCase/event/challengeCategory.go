package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	IChallengeCategoryService interface {
		GetEventCategories(ctx context.Context, eventID uuid.UUID) ([]*model.ChallengeCategory, error)
		CreateEventCategory(ctx context.Context, category *model.ChallengeCategory) error
		UpdateEventCategory(ctx context.Context, category *model.ChallengeCategory) error
		DeleteEventCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error
		UpdateEventCategoriesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error
	}
)

func (u *EventUseCase) GetEventCategories(ctx context.Context, eventID uuid.UUID) ([]*model.ChallengeCategory, error) {
	return u.service.GetEventCategories(ctx, eventID)
}

func (u *EventUseCase) CreateEventCategory(ctx context.Context, category *model.ChallengeCategory) error {
	return u.service.CreateEventCategory(ctx, category)
}

func (u *EventUseCase) UpdateEventCategory(ctx context.Context, category *model.ChallengeCategory) error {
	return u.service.UpdateEventCategory(ctx, category)
}

func (u *EventUseCase) DeleteEventCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error {
	return u.service.DeleteEventCategory(ctx, eventID, categoryID)
}

func (u *EventUseCase) UpdateEventCategoriesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {
	return u.service.UpdateEventCategoriesOrder(ctx, eventID, orders)
}
