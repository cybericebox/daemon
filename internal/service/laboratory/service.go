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
		GetLaboratories(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LaboratoryInfo, error)
		CreateLaboratories(ctx context.Context, mask, count int) ([]uuid.UUID, error)
		DeleteLaboratories(ctx context.Context, labIDs ...uuid.UUID) error
		AddLaboratoryChallenges(ctx context.Context, labID uuid.UUID, configs []model.LaboratoryChallenge) error
		DeleteLaboratoriesChallenges(ctx context.Context, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error

		StartChallenge(ctx context.Context, labID, challengeID uuid.UUID) error
		StopChallenge(ctx context.Context, labID, challengeID uuid.UUID) error
		ResetChallenge(ctx context.Context, labID, challengeID uuid.UUID) error

		GetVPNClientConfig(ctx context.Context, userID, groupID, destCIDR string) (string, error)
		DeleteVPNClients(ctx context.Context, userID, groupID string) error
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

func (s *LaboratoryService) GetLaboratories(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LaboratoryInfo, error) {
	labs, err := s.repository.GetLaboratories(ctx, labIDs...)
	if err != nil {
		return nil, model.ErrLaboratory.WithError(err).WithMessage("Failed to get laboratories").Cause()
	}

	return labs, nil
}

func (s *LaboratoryService) CreateLaboratories(ctx context.Context, networkMask, count int) ([]uuid.UUID, error) {
	ids, err := s.repository.CreateLaboratories(ctx, networkMask, count)
	if err != nil {
		return nil, model.ErrLaboratory.WithError(err).WithMessage("Failed to create laboratory").Cause()
	}

	return ids, nil
}

func (s *LaboratoryService) DeleteLaboratories(ctx context.Context, labIDs ...uuid.UUID) error {
	if err := s.repository.DeleteLaboratories(ctx, labIDs...); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to delete laboratories").Cause()
	}

	return nil
}

func (s *LaboratoryService) AddLaboratoryChallenges(ctx context.Context, labID uuid.UUID, configs []model.LaboratoryChallenge) error {
	if err := s.repository.AddLaboratoryChallenges(ctx, labID, configs); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to add challenges to laboratory").Cause()
	}

	return nil
}

func (s *LaboratoryService) DeleteLaboratoriesChallenges(ctx context.Context, labIDs []uuid.UUID, challengeIDs []uuid.UUID) error {
	if err := s.repository.DeleteLaboratoriesChallenges(ctx, labIDs, challengeIDs); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to delete challenges from laboratories").Cause()
	}

	return nil
}

func (s *LaboratoryService) StartChallenge(ctx context.Context, labID, challengeID uuid.UUID) error {
	if err := s.repository.StartChallenge(ctx, labID, challengeID); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to start challenge").Cause()
	}

	return nil
}

func (s *LaboratoryService) StopChallenge(ctx context.Context, labID, challengeID uuid.UUID) error {
	if err := s.repository.StopChallenge(ctx, labID, challengeID); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to stop challenge").Cause()
	}

	return nil
}

func (s *LaboratoryService) ResetChallenge(ctx context.Context, labID, challengeID uuid.UUID) error {
	if err := s.repository.ResetChallenge(ctx, labID, challengeID); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to reset challenge").Cause()
	}

	return nil
}

// vpn

func (s *LaboratoryService) GetVPNClientConfig(ctx context.Context, userID, groupID uuid.UUID, labCIDR string) (string, error) {
	config, err := s.repository.GetVPNClientConfig(ctx, userID.String(), groupID.String(), labCIDR)
	if err != nil {
		return "", model.ErrLaboratory.WithError(err).WithMessage("Failed to get VPN client config").Cause()
	}

	return config, nil
}

func (s *LaboratoryService) DeleteVPNClients(ctx context.Context, userID, groupID uuid.UUID) error {
	if err := s.repository.DeleteVPNClients(ctx, userID.String(), groupID.String()); err != nil {
		return model.ErrLaboratory.WithError(err).WithMessage("Failed to delete VPN client").Cause()
	}

	return nil
}
