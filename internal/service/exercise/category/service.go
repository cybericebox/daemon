package categoryService

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	CategoryService struct {
		repository IRepository
	}

	IRepository interface {
		CreateExerciseCategory(ctx context.Context, arg postgres.CreateExerciseCategoryParams) error
		GetExerciseCategoryByID(ctx context.Context, id uuid.UUID) (postgres.ExerciseCategory, error)
		GetExerciseCategories(ctx context.Context, arg postgres.GetExerciseCategoriesParams) ([]postgres.ExerciseCategory, error)

		UpdateExerciseCategory(ctx context.Context, arg postgres.UpdateExerciseCategoryParams) (int64, error)

		DeleteExerciseCategory(ctx context.Context, id uuid.UUID) (int64, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *CategoryService {
	return &CategoryService{
		repository: deps.Repository,
	}
}

func (s *CategoryService) GetExerciseCategories(ctx context.Context, page int) ([]*exerciseModel.ExerciseCategory, error) {

	categories, err := s.repository.GetExerciseCategories(ctx, postgres.GetExerciseCategoriesParams{
		Limit:  config.DefaultOnePageLimit,
		Offset: int32(page * config.DefaultOnePageLimit),
	})
	if err != nil {
		return nil, exerciseModel.ErrExerciseCategory.WithError(err).WithMessage("Failed to get exercise categories").Err()
	}

	result := make([]*exerciseModel.ExerciseCategory, 0, len(categories))
	for _, category := range categories {
		result = append(result, &exerciseModel.ExerciseCategory{
			ID:          category.ID,
			Name:        category.Name,
			Description: category.Description,
			UpdatedAt:   category.UpdatedAt.Time,
			UpdatedBy:   category.UpdatedBy,
			CreatedAt:   category.CreatedAt,
		})
	}

	return result, nil
}

func (s *CategoryService) GetExerciseCategory(ctx context.Context, categoryID uuid.UUID) (*exerciseModel.ExerciseCategory, error) {
	category, err := s.repository.GetExerciseCategoryByID(ctx, categoryID)
	if err != nil {
		return nil, exerciseModel.ErrExerciseCategory.WithError(err).WithMessage("Failed to get exercise category").WithContext("categoryID", categoryID).Err()
	}

	return &exerciseModel.ExerciseCategory{
		ID:          category.ID,
		Name:        category.Name,
		Description: category.Description,
		UpdatedAt:   category.UpdatedAt.Time,
		UpdatedBy:   category.UpdatedBy,
		CreatedAt:   category.CreatedAt,
	}, nil
}

func (s *CategoryService) CreateExerciseCategory(ctx context.Context, category exerciseModel.ExerciseCategory) error {
	if err := s.repository.CreateExerciseCategory(ctx, postgres.CreateExerciseCategoryParams{
		ID:          uuid.Must(uuid.NewV7()),
		Name:        category.Name,
		Description: category.Description,
	}); err != nil {
		errCreator, has := tools.UniqueViolationError(err, exerciseModel.ErrExerciseCategoryCategoryExists)
		if has {
			return errCreator.Err()
		}

		return exerciseModel.ErrExerciseCategory.WithError(err).WithMessage("Failed to create exercise category").Err()
	}

	return nil
}

func (s *CategoryService) UpdateExerciseCategory(ctx context.Context, category exerciseModel.ExerciseCategory) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	affected, err := s.repository.UpdateExerciseCategory(ctx, postgres.UpdateExerciseCategoryParams{
		ID:          category.ID,
		Name:        category.Name,
		Description: category.Description,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {

		errCreator, has := tools.UniqueViolationError(err, exerciseModel.ErrExerciseCategoryCategoryExists)
		if has {
			return errCreator.Err()
		}

		errCreator, has = tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return exerciseModel.ErrExerciseCategory.WithError(err).WithMessage("Failed to update exercise category").WithContext("categoryID", category.ID).Err()
	}

	if affected == 0 {
		return exerciseModel.ErrExerciseCategoryCategoryNotFound.WithMessage("Exercise category not found").WithContext("categoryID", category.ID).Err()
	}

	return nil
}

func (s *CategoryService) DeleteExerciseCategory(ctx context.Context, categoryID uuid.UUID) error {
	affected, err := s.repository.DeleteExerciseCategory(ctx, categoryID)
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err, true)
		if has {
			return errCreator.Err()
		}
		return exerciseModel.ErrExerciseCategory.WithError(err).WithMessage("Failed to delete exercise category").WithContext("categoryID", categoryID).Err()
	}

	if affected == 0 {
		return exerciseModel.ErrExerciseCategoryCategoryNotFound.WithMessage("Exercise category not found").WithContext("categoryID", categoryID).Err()
	}
	return nil
}
