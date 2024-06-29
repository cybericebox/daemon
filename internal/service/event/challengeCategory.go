package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	IChallengeCategoryRepository interface {
		CreateEventChallengeCategory(ctx context.Context, arg postgres.CreateEventChallengeCategoryParams) error
		GetEventChallengeCategories(ctx context.Context, eventID uuid.UUID) ([]postgres.EventChallengeCategory, error)
		UpdateEventChallengeCategory(ctx context.Context, arg postgres.UpdateEventChallengeCategoryParams) error
		UpdateEventChallengeCategoryOrder(ctx context.Context, arg postgres.UpdateEventChallengeCategoryOrderParams) error
		DeleteEventChallengeCategory(ctx context.Context, arg postgres.DeleteEventChallengeCategoryParams) error

		WithTransaction(ctx context.Context) (withTx interface{}, commit func(), rollback func(), err error)
	}
)

func (s *EventService) GetEventCategories(ctx context.Context, eventID uuid.UUID) ([]*model.ChallengeCategory, error) {
	categories, err := s.repository.GetEventChallengeCategories(ctx, eventID)
	if err != nil {
		return nil, err
	}

	result := make([]*model.ChallengeCategory, 0, len(categories))
	for _, category := range categories {
		result = append(result, &model.ChallengeCategory{
			ID:      category.ID,
			Name:    category.Name,
			Order:   category.OrderIndex,
			EventID: category.EventID,
		})
	}

	return result, nil
}

func (s *EventService) CreateEventCategory(ctx context.Context, category *model.ChallengeCategory) error {
	if err := s.repository.CreateEventChallengeCategory(ctx, postgres.CreateEventChallengeCategoryParams{
		ID:         uuid.Must(uuid.NewV7()),
		EventID:    category.EventID,
		Name:       category.Name,
		OrderIndex: category.Order,
	}); err != nil {
		return err
	}

	return nil
}

func (s *EventService) UpdateEventCategory(ctx context.Context, category *model.ChallengeCategory) error {
	if err := s.repository.UpdateEventChallengeCategory(ctx, postgres.UpdateEventChallengeCategoryParams{
		EventID: category.EventID,
		ID:      category.ID,
		Name:    category.Name,
	}); err != nil {
		return err
	}

	return nil
}

func (s *EventService) DeleteEventCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error {
	if err := s.repository.DeleteEventChallengeCategory(ctx, postgres.DeleteEventChallengeCategoryParams{
		EventID: eventID,
		ID:      categoryID,
	}); err != nil {
		return err
	}

	return nil
}

func (s *EventService) UpdateEventCategoriesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {
	for _, order := range orders {
		if err := s.repository.UpdateEventChallengeCategoryOrder(ctx, postgres.UpdateEventChallengeCategoryOrderParams{
			EventID:    eventID,
			ID:         order.ID,
			OrderIndex: order.Index,
		}); err != nil {

			return err
		}
	}
	return nil
}
