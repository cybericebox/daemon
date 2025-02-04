package challengeCategoryService

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/rs/zerolog/log"
)

type (
	ChallengeCategoryService struct {
		repository IRepository
	}

	IRepository interface {
		GetEventChallengeCategories(ctx context.Context, eventID uuid.UUID) ([]postgres.EventChallengeCategory, error)
		GetEventChallengeCategoryByID(ctx context.Context, categoryID uuid.UUID) (postgres.EventChallengeCategory, error)
		CreateEventChallengeCategory(ctx context.Context, arg postgres.CreateEventChallengeCategoryParams) error

		UpdateEventChallengeCategory(ctx context.Context, arg postgres.UpdateEventChallengeCategoryParams) (int64, error)
		UpdateEventChallengeCategoryOrder(ctx context.Context, arg []postgres.UpdateEventChallengeCategoryOrderParams) *postgres.UpdateEventChallengeCategoryOrderBatchResults

		DeleteEventChallengeCategory(ctx context.Context, categoryID uuid.UUID) (int64, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *ChallengeCategoryService {
	return &ChallengeCategoryService{
		repository: deps.Repository,
	}
}

func (s *ChallengeCategoryService) GetEventChallengeCategories(ctx context.Context, eventID uuid.UUID) ([]*eventModel.ChallengeCategory, error) {
	categories, err := s.repository.GetEventChallengeCategories(ctx, eventID)
	if err != nil {
		return nil, eventModel.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to get event categories from repository").Err()
	}

	result := make([]*eventModel.ChallengeCategory, 0, len(categories))
	for _, category := range categories {
		result = append(result, &eventModel.ChallengeCategory{
			ID:        category.ID,
			EventID:   category.EventID,
			Name:      category.Name,
			Order:     category.OrderIndex,
			UpdatedAt: category.UpdatedAt.Time,
			UpdatedBy: category.UpdatedBy,
			CreatedAt: category.CreatedAt,
		})
	}

	return result, nil
}

func (s *ChallengeCategoryService) GetEventChallengeCategory(ctx context.Context, categoryID uuid.UUID) (*eventModel.ChallengeCategory, error) {
	category, err := s.repository.GetEventChallengeCategoryByID(ctx, categoryID)
	if err != nil {
		return nil, eventModel.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to get event category from repository").Err()
	}

	return &eventModel.ChallengeCategory{
		ID:        category.ID,
		EventID:   category.EventID,
		Name:      category.Name,
		Order:     category.OrderIndex,
		UpdatedAt: category.UpdatedAt.Time,
		UpdatedBy: category.UpdatedBy,
		CreatedAt: category.CreatedAt,
	}, nil
}

func (s *ChallengeCategoryService) CreateEventChallengeCategory(ctx context.Context, category eventModel.ChallengeCategory) error {
	if err := s.repository.CreateEventChallengeCategory(ctx, postgres.CreateEventChallengeCategoryParams{
		ID:         uuid.Must(uuid.NewV7()),
		EventID:    category.EventID,
		Name:       category.Name,
		OrderIndex: category.Order,
	}); err != nil {
		if tools.IsUniqueViolationError(err) {
			return eventModel.ErrEventChallengeCategoryCategoryExists.WithMessage("Event category already exists").Err()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return eventModel.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to create event category").Err()
	}

	return nil
}

func (s *ChallengeCategoryService) UpdateEventChallengeCategory(ctx context.Context, category eventModel.ChallengeCategory) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	affected, err := s.repository.UpdateEventChallengeCategory(ctx, postgres.UpdateEventChallengeCategoryParams{
		ID:   category.ID,
		Name: category.Name,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		if tools.IsUniqueViolationError(err) {
			return eventModel.ErrEventChallengeCategoryCategoryExists.WithMessage("Event category already exists").Err()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return eventModel.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to update event category").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventChallengeCategoryCategoryNotFound.WithMessage("Event category not found").WithContext("categoryID", category.ID).Err()
	}

	return nil
}

func (s *ChallengeCategoryService) DeleteEventChallengeCategory(ctx context.Context, eventID uuid.UUID, categoryID uuid.UUID) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	affected, err := s.repository.DeleteEventChallengeCategory(ctx, categoryID)
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err, true)
		if has {
			return errCreator.Err()
		}
		return eventModel.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to delete event category").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventChallengeCategoryCategoryNotFound.WithMessage("Event category not found").WithContext("categoryID", categoryID).Err()
	}

	//remain rest categories order
	categories, err := s.repository.GetEventChallengeCategories(ctx, eventID)
	if err != nil {
		return eventModel.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to get event categories from repository").Err()
	}

	orderParams := make([]postgres.UpdateEventChallengeCategoryOrderParams, 0, len(categories))
	for _, category := range categories {
		orderParams = append(orderParams, postgres.UpdateEventChallengeCategoryOrderParams{
			ID:         category.ID,
			OrderIndex: category.OrderIndex,
			UpdatedBy: uuid.NullUUID{
				UUID:  currentUserID,
				Valid: true,
			},
		})
	}

	if err = s.updateEventChallengeCategoriesOrder(ctx, orderParams); err != nil {
		return eventModel.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to update event categories order after delete").Err()
	}

	return nil
}

func (s *ChallengeCategoryService) UpdateEventChallengeCategoriesOrder(ctx context.Context, orders []eventModel.Order) error {
	params := make([]postgres.UpdateEventChallengeCategoryOrderParams, 0, len(orders))

	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	for _, order := range orders {
		params = append(params, postgres.UpdateEventChallengeCategoryOrderParams{
			ID:         order.ID,
			OrderIndex: order.Index,
			UpdatedBy: uuid.NullUUID{
				UUID:  currentUserID,
				Valid: true,
			},
		})
	}

	return s.updateEventChallengeCategoriesOrder(ctx, params)
}

func (s *ChallengeCategoryService) updateEventChallengeCategoriesOrder(ctx context.Context, orderParams []postgres.UpdateEventChallengeCategoryOrderParams) error {
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
				errs = multierror.Append(errs, errCreator.Err())
				return
			}
			errs = multierror.Append(errs, eventModel.ErrEventChallengeCategory.WithError(err).WithMessage("Failed to update event challenge category order").WithContext("categoryID", orderParams[i].ID).Err())
		}
		if affected == 0 {
			errs = multierror.Append(errs, eventModel.ErrEventChallengeCategoryCategoryNotFound.WithMessage("Event category not found").WithContext("categoryID", orderParams[i].ID).Err())
		}
	})

	if errs != nil {
		return eventModel.ErrEventChallengeCategory.WithError(errs).WithMessage("Failed to update event challenge category order").Err()
	}

	return nil
}
