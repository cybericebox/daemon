package laboratory

import (
	"context"
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
	return s.repository.CreateLab(ctx, networkMask)
}

func (s LaboratoryService) GetLaboratories(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LabInfo, error) {
	labs, err := s.repository.GetLabs(ctx, labIDs...)
	if err != nil {
		return nil, err
	}

	return labs, nil
}
