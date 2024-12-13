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
		GetLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) ([]*model.LaboratoryInfo, error)
		CreateLaboratories(ctx context.Context, mask, count int, labsGroupID uuid.UUID) ([]uuid.UUID, error)
		DeleteLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error
		StartLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error
		StopLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error

		AddLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, configs []model.LaboratoryChallenge, flagEnvVars []model.FlagVariable) error
		DeleteLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error
		StartLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error
		StopLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error
		ResetLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error

		GetVPNClientConfig(ctx context.Context, userID, groupID uuid.UUID, destCIDR string) (string, error)
		DeleteVPNClients(ctx context.Context, userID, groupID uuid.UUID) error
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

func (s *LaboratoryService) GetLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) ([]*model.LaboratoryInfo, error) {
	labs, err := s.repository.GetLaboratories(ctx, labsGroupID, labIDs)
	if err != nil {
		return nil, model.ErrLaboratory.WithError(err).WithMessage("Failed to get laboratories").Err()
	}

	return labs, nil
}

func (s *LaboratoryService) CreateLaboratories(ctx context.Context, networkMask, count int, labsGroupID uuid.UUID) ([]uuid.UUID, error) {
	ids, err := s.repository.CreateLaboratories(ctx, networkMask, count, labsGroupID)
	if err != nil {
		return nil, model.ErrLaboratory.WithError(err).WithMessage("Failed to create laboratory").Err()
	}

	return ids, nil
}

func (s *LaboratoryService) DeleteLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error {
	if err := s.repository.DeleteLaboratories(ctx, labsGroupID, labIDs); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to delete laboratories").Err()
	}

	return nil
}

func (s *LaboratoryService) StartLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error {
	if err := s.repository.StartLaboratories(ctx, labsGroupID, labIDs); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to start laboratories").Err()
	}

	return nil
}

func (s *LaboratoryService) StopLaboratories(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID) error {
	if err := s.repository.StopLaboratories(ctx, labsGroupID, labIDs); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to stop laboratories").Err()
	}

	return nil
}

// laboratory challenges

func (s *LaboratoryService) AddLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, configs []model.LaboratoryChallenge, flagEnvVars []model.FlagVariable) error {
	if err := s.repository.AddLaboratoriesChallenges(ctx, labsGroupID, labIDs, configs, flagEnvVars); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to add challenges to laboratories").Err()
	}

	return nil
}

func (s *LaboratoryService) DeleteLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	if err := s.repository.DeleteLaboratoriesChallenges(ctx, labsGroupID, labIDs, challengeIDs); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to delete challenges from laboratories").Err()
	}

	return nil
}

func (s *LaboratoryService) StartLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	if err := s.repository.StartLaboratoriesChallenges(ctx, labsGroupID, labIDs, challengeIDs); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to start challenges in laboratories").Err()
	}

	return nil
}

func (s *LaboratoryService) StopLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	if err := s.repository.StopLaboratoriesChallenges(ctx, labsGroupID, labIDs, challengeIDs); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to stop challenges in laboratories").Err()
	}

	return nil
}

func (s *LaboratoryService) ResetLaboratoriesChallenges(ctx context.Context, labsGroupID uuid.UUID, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	if err := s.repository.ResetLaboratoriesChallenges(ctx, labsGroupID, labIDs, challengeIDs); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to reset challenges in laboratories").Err()
	}

	return nil
}

// vpn

func (s *LaboratoryService) GetVPNClientConfig(ctx context.Context, userID, groupID uuid.UUID, labCIDR string) (string, error) {
	config, err := s.repository.GetVPNClientConfig(ctx, userID, groupID, labCIDR)
	if err != nil {
		return "", model.ErrLaboratory.WithError(err).WithMessage("Failed to get VPN client config").Err()
	}

	return config, nil
}

func (s *LaboratoryService) DeleteVPNClients(ctx context.Context, userID, groupID uuid.UUID) error {
	if err := s.repository.DeleteVPNClients(ctx, userID, groupID); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to delete VPN client").Err()
	}

	return nil
}
