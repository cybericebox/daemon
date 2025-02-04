package labService

import (
	"context"
	"github.com/cybericebox/daemon/internal/model/laboratory"
	"github.com/gofrs/uuid"
)

type (
	LabService struct {
		repository IRepository
	}

	IRepository interface {
		GetLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) ([]*laboratoryModel.LaboratoryInfo, error)
		CreateLaboratories(ctx context.Context, mask, count int, labsGroupID uuid.UUID) ([]uuid.UUID, error)
		DeleteLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error
		StartLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error
		StopLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error

		AddLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, configs []laboratoryModel.LaboratoryChallenge, flagEnvVars []laboratoryModel.FlagVariable) error
		DeleteLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error
		StartLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error
		StopLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error
		ResetLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *LabService {
	return &LabService{
		repository: deps.Repository,
	}
}

func (s *LabService) GetLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) ([]*laboratoryModel.LaboratoryInfo, error) {
	labs, err := s.repository.GetLaboratories(ctx, labsGroupID, labIDs)
	if err != nil {
		return nil, laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to get laboratories").Err()
	}

	return labs, nil
}

func (s *LabService) CreateLaboratories(ctx context.Context, networkMask, count int, labsGroupID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.repository.CreateLaboratories(ctx, networkMask, count, labsGroupID)
	if err != nil {
		return nil, laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to create laboratory").Err()
	}

	return ids, nil
}

func (s *LabService) DeleteLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error {
	if err := s.repository.DeleteLaboratories(ctx, labsGroupID, labIDs); err != nil {
		return laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to delete laboratories").Err()
	}

	return nil
}

func (s *LabService) StartLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error {
	if err := s.repository.StartLaboratories(ctx, labsGroupID, labIDs); err != nil {
		return laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to start laboratories").Err()
	}

	return nil
}

func (s *LabService) StopLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error {
	if err := s.repository.StopLaboratories(ctx, labsGroupID, labIDs); err != nil {
		return laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to stop laboratories").Err()
	}

	return nil
}

// laboratory challenges

func (s *LabService) AddLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, configs []laboratoryModel.LaboratoryChallenge, flagEnvVars []laboratoryModel.FlagVariable) error {
	if err := s.repository.AddLaboratoriesChallenges(ctx, labsGroupID, labIDs, configs, flagEnvVars); err != nil {
		return laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to add challenges to laboratories").Err()
	}

	return nil
}

func (s *LabService) DeleteLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	if err := s.repository.DeleteLaboratoriesChallenges(ctx, labsGroupID, labIDs, challengeIDs); err != nil {
		return laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to delete challenges from laboratories").Err()
	}

	return nil
}

func (s *LabService) StartLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	if err := s.repository.StartLaboratoriesChallenges(ctx, labsGroupID, labIDs, challengeIDs); err != nil {
		return laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to start challenges in laboratories").Err()
	}

	return nil
}

func (s *LabService) StopLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	if err := s.repository.StopLaboratoriesChallenges(ctx, labsGroupID, labIDs, challengeIDs); err != nil {
		return laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to stop challenges in laboratories").Err()
	}

	return nil
}

func (s *LabService) ResetLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	if err := s.repository.ResetLaboratoriesChallenges(ctx, labsGroupID, labIDs, challengeIDs); err != nil {
		return laboratoryModel.ErrLaboratory.WithError(err).WithMessage("Failed to reset challenges in laboratories").Err()
	}

	return nil
}
