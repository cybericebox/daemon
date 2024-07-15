package laboratory

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	LaboratoryService struct {
		repository IRepository
	}
	IRepository interface {
		GetLabs(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LabInfo, error)
		CreateLab(ctx context.Context, mask int) (uuid.UUID, error)
		DeleteLabs(ctx context.Context, labIDs ...uuid.UUID) error
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewLaboratoryService(deps Dependencies) *LaboratoryService {
	return &LaboratoryService{
		repository: deps.Repository,
	}
}

func (s LaboratoryService) CreateLaboratory(ctx context.Context, networkMask int) (uuid.UUID, error) {
	id, err := s.repository.CreateLab(ctx, networkMask)
	if err != nil {
		return uuid.Nil, appError.NewError().WithError(err).WithMessage("failed to create laboratory")
	}

	return id, nil
}

func (s LaboratoryService) GetLaboratories(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LabInfo, error) {
	labs, err := s.repository.GetLabs(ctx, labIDs...)
	if err != nil {
		return nil, appError.NewError().WithError(err).WithMessage("failed to get laboratories")
	}

	return labs, nil
}

func (s LaboratoryService) DeleteLaboratories(ctx context.Context, labIDs ...uuid.UUID) error {
	if err := s.repository.DeleteLabs(ctx, labIDs...); err != nil {
		return appError.NewError().WithError(err).WithMessage("failed to delete laboratories")
	}

	return nil
}
