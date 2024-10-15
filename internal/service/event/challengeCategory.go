package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog/log"
)

type (
	IChallengeCategoryRepository interface {
		GetEventChallengeCategories(ctx context.Context, eventID uuid.UUID) ([]postgres.EventChallengeCategory, error)

		CreateEventChallengeCategory(ctx context.Context, arg postgres.CreateEventChallengeCategoryParams) error

		UpdateEventChallengeCategory(ctx context.Context, arg postgres.UpdateEventChallengeCategoryParams) (int64, error)
		UpdateEventChallengeCategoryOrder(ctx context.Context, arg []postgres.UpdateEventChallengeCategoryOrderParams) *postgres.UpdateEventChallengeCategoryOrderBatchResults

		DeleteEventChallengeCategory(ctx context.Context, arg postgres.DeleteEventChallengeCategoryParams) (int64, error)
	}
)

func (s *EventService) GetEventCategories(ctx context.Context, eventID uuid.UUID) ([]*model.ChallengeCategory, error) {
	categories, err := s.repository.GetEventChallengeCategories(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to get event categories from repository").Cause()
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

func (s *EventService) CreateEventCategory(ctx context.Context, category model.ChallengeCategory) error {
	if err := s.repository.CreateEventChallengeCategory(ctx, postgres.CreateEventChallengeCategoryParams{
		ID:         uuid.Must(uuid.NewV7()),
		EventID:    category.EventID,
		Name:       category.Name,
		OrderIndex: category.Order,
	}); err != nil {
		if tools.IsUniqueViolationError(err) {
			return model.ErrEventChallengeCategoryCategoryExists.WithMessage("Event category already exists").Cause()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to create event category").Cause()
	}

	return nil
}

func (s *EventService) UpdateEventCategory(ctx context.Context, category model.ChallengeCategory) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateEventChallengeCategory(ctx, postgres.UpdateEventChallengeCategoryParams{
		EventID: category.EventID,
		ID:      category.ID,
		Name:    category.Name,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		if tools.IsUniqueViolationError(err) {
			return model.ErrEventChallengeCategoryCategoryExists.WithMessage("Event category already exists").Cause()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to update event category").Cause()
	}

	if affected == 0 {
		return model.ErrEventChallengeCategoryCategoryNotFound.WithMessage("Event category not found").WithContext("categoryID", category.ID).Cause()
	}

	return nil
}

func (s *EventService) DeleteEventCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error {
	affected, err := s.repository.DeleteEventChallengeCategory(ctx, postgres.DeleteEventChallengeCategoryParams{
		EventID: eventID,
		ID:      categoryID,
	})
	if err != nil {
		return model.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to delete event category").Cause()
	}

	if affected == 0 {
		return model.ErrEventChallengeCategoryCategoryNotFound.WithMessage("Event category not found").WithContext("categoryID", categoryID).Cause()
	}

	//remain rest categories order
	categories, err := s.repository.GetEventChallengeCategories(ctx, eventID)
	if err != nil {
		return model.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to get event categories from repository").Cause()
	}

	orderParams := make([]postgres.UpdateEventChallengeCategoryOrderParams, 0, len(categories))
	for _, category := range categories {
		orderParams = append(orderParams, postgres.UpdateEventChallengeCategoryOrderParams{
			EventID:    eventID,
			ID:         category.ID,
			OrderIndex: category.OrderIndex,
		})
	}

	if err = s.updateEventCategoriesOrder(ctx, orderParams); err != nil {
		return model.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to update event categories order after delete").Cause()
	}

	return nil
}

func (s *EventService) UpdateEventCategoriesOrder(ctx context.Context, eventID uuid.UUID, orders []model.Order) error {
	params := make([]postgres.UpdateEventChallengeCategoryOrderParams, 0, len(orders))

	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	for _, order := range orders {
		params = append(params, postgres.UpdateEventChallengeCategoryOrderParams{
			EventID:    eventID,
			ID:         order.ID,
			OrderIndex: order.Index,
			UpdatedBy: uuid.NullUUID{
				UUID:  currentUserID,
				Valid: true,
			},
		})
	}

	return s.updateEventCategoriesOrder(ctx, params)
}

func (s *EventService) updateEventCategoriesOrder(ctx context.Context, orderParams []postgres.UpdateEventChallengeCategoryOrderParams) error {
	batchResult := s.repository.UpdateEventChallengeCategoryOrder(ctx, orderParams)
	defer func() {
		if err := batchResult.Close(); err != nil {
			log.Error().Err(err).Msg("Failed to close batch result")
		}
	}()

	var errs error
	batchResult.Exec(func(i int, affected int64, err error) {
		if err != nil {
			errCreator, has := tools.ForeignKeyViolationError(err)
			if has {
				errs = multierror.Append(errs, errCreator.Cause())
				return
			}
			errs = multierror.Append(errs, model.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to update event challenge category order").WithContext("categoryID", orderParams[i].ID).Cause())
		}
		if affected == 0 {
			errs = multierror.Append(errs, model.ErrEventChallengeCategoryCategoryNotFound.WithMessage("Event category not found").WithContext("categoryID", orderParams[i].ID).Cause())
		}
	})

	if errs != nil {
		return model.ErrEventChallengeCategory.WithError(errs).WithMessage("Failed to update event challenge category order").Cause()
	}

	return nil
}
