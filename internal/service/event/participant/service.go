package participantService

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	ParticipantService struct {
		repository IRepository
	}

	IRepository interface {
		GetEventParticipants(ctx context.Context, eventID uuid.UUID) ([]postgres.GetEventParticipantsRow, error)
		GetEventParticipantsPaged(ctx context.Context, arg postgres.GetEventParticipantsPagedParams) ([]postgres.GetEventParticipantsPagedRow, error)
		GetEventParticipantStatus(ctx context.Context, arg postgres.GetEventParticipantStatusParams) (int32, error)
		GetEventParticipantTeam(ctx context.Context, arg postgres.GetEventParticipantTeamParams) (postgres.GetEventParticipantTeamRow, error)

		CreateEventParticipant(ctx context.Context, arg postgres.CreateEventParticipantParams) error

		UpdateEventParticipantStatus(ctx context.Context, arg postgres.UpdateEventParticipantStatusParams) (int64, error)
		DeleteEventParticipant(ctx context.Context, arg postgres.DeleteEventParticipantParams) (int64, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *ParticipantService {
	return &ParticipantService{
		repository: deps.Repository,
	}
}

func (s *ParticipantService) GetEventParticipants(ctx context.Context, eventID uuid.UUID, page int) ([]*eventModel.Participant, error) {
	if page == config.AllPages {
		participants, err := s.repository.GetEventParticipants(ctx, eventID)
		if err != nil {
			return nil, eventModel.ErrEventParticipant.WithError(err).WithMessage("Failed to get event participants").Err()
		}

		res := make([]*eventModel.Participant, 0, len(participants))
		for _, p := range participants {
			res = append(res, &eventModel.Participant{
				UserID:         p.UserID,
				EventID:        p.EventID,
				TeamID:         p.TeamID,
				TeamName:       p.TeamName,
				Hidden:         p.Hidden,
				Name:           p.Name,
				Email:          p.Email,
				ApprovalStatus: p.ApprovalStatus,
				UpdatedAt:      p.UpdatedAt.Time,
				UpdatedBy:      p.UpdatedBy,
				CreatedAt:      p.CreatedAt,
			})
		}
		return res, nil
	}

	// one page
	participants, err := s.repository.GetEventParticipantsPaged(ctx, postgres.GetEventParticipantsPagedParams{
		EventID: eventID,
		Limit:   config.DefaultOnePageLimit,
		Offset:  int32(page * config.DefaultOnePageLimit),
	})
	if err != nil {
		return nil, eventModel.ErrEventParticipant.WithError(err).WithMessage("Failed to get event participants").Err()
	}

	res := make([]*eventModel.Participant, 0, len(participants))
	for _, p := range participants {
		res = append(res, &eventModel.Participant{
			UserID:         p.UserID,
			EventID:        p.EventID,
			TeamID:         p.TeamID,
			TeamName:       p.TeamName,
			Hidden:         p.Hidden,
			Name:           p.Name,
			Email:          p.Email,
			ApprovalStatus: p.ApprovalStatus,
			UpdatedAt:      p.UpdatedAt.Time,
			UpdatedBy:      p.UpdatedBy,
			CreatedAt:      p.CreatedAt,
		})
	}
	return res, nil
}

func (s *ParticipantService) GetEventParticipantStatus(ctx context.Context, eventID, userID uuid.UUID) (int32, error) {
	status, err := s.repository.GetEventParticipantStatus(ctx, postgres.GetEventParticipantStatusParams{
		EventID: eventID,
		UserID:  userID,
	})
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return eventModel.NoParticipationStatus, nil
		}
		return eventModel.NoParticipationStatus, eventModel.ErrEventParticipant.WithError(err).WithMessage("Failed to get event join status").Err()
	}
	return status, nil
}

func (s *ParticipantService) GetEventParticipantTeam(ctx context.Context, eventID, userID uuid.UUID) (*eventModel.Team, error) {
	team, err := s.repository.GetEventParticipantTeam(ctx, postgres.GetEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
	})
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventParticipantTeamNotFound.WithContext("eventID", eventID).WithContext("userID", userID).Err()
		}
		return nil, eventModel.ErrEventParticipant.WithError(err).WithMessage("Failed to get participant team").Err()
	}

	return &eventModel.Team{
		ID:           team.ID,
		EventID:      eventID,
		Name:         team.Name,
		JoinCode:     team.JoinCode,
		LaboratoryID: team.LaboratoryID,
	}, nil
}

func (s *ParticipantService) CreateJoinEventRequest(ctx context.Context, participant eventModel.Participant) error {
	if err := s.repository.CreateEventParticipant(ctx, postgres.CreateEventParticipantParams{
		EventID:        participant.EventID,
		UserID:         participant.UserID,
		ApprovalStatus: participant.ApprovalStatus,
	}); err != nil {
		if tools.IsUniqueViolationError(err) {
			return eventModel.ErrEventParticipantExists.WithContext("eventID", participant.EventID).WithContext("userID", participant.UserID).Err()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return eventModel.ErrEventParticipant.WithError(err).WithMessage("Failed to create join event request").Err()
	}
	return nil
}

func (s *ParticipantService) UpdateEventParticipantStatus(ctx context.Context, eventID, userID uuid.UUID, status int32) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	affected, err := s.repository.UpdateEventParticipantStatus(ctx, postgres.UpdateEventParticipantStatusParams{
		EventID:        eventID,
		UserID:         userID,
		ApprovalStatus: status,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return eventModel.ErrEventParticipant.WithError(err).WithMessage("Failed to update event participant status").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventParticipantNotFound.WithContext("eventID", eventID).WithContext("userID", userID).Err()
	}

	return nil
}

func (s *ParticipantService) DeleteEventParticipant(ctx context.Context, eventID, userID uuid.UUID) error {
	affected, err := s.repository.DeleteEventParticipant(ctx, postgres.DeleteEventParticipantParams{
		EventID: eventID,
		UserID:  userID,
	})
	if err != nil {
		return eventModel.ErrEventParticipant.WithError(err).WithMessage("Failed to delete event participant").Err()
	}
	if affected == 0 {
		return eventModel.ErrEventParticipantNotFound.WithContext("eventID", eventID).WithContext("userID", userID).Err()
	}
	return nil
}
