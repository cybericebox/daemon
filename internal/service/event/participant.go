package event

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
)

type (
	IParticipantRepository interface {
		GetEventParticipants(ctx context.Context, eventID uuid.UUID) ([]postgres.EventParticipant, error)
		GetEventParticipantStatus(ctx context.Context, arg postgres.GetEventParticipantStatusParams) (int32, error)
		GetEventParticipantTeam(ctx context.Context, arg postgres.GetEventParticipantTeamParams) (postgres.GetEventParticipantTeamRow, error)

		CreateEventParticipant(ctx context.Context, arg postgres.CreateEventParticipantParams) error

		UpdateEventParticipantStatus(ctx context.Context, arg postgres.UpdateEventParticipantStatusParams) (int64, error)
		UpdateEventParticipantName(ctx context.Context, arg postgres.UpdateEventParticipantNameParams) (int64, error)
		DeleteEventParticipant(ctx context.Context, arg postgres.DeleteEventParticipantParams) (int64, error)
	}
)

func (s *EventService) GetEventParticipants(ctx context.Context, eventID uuid.UUID) ([]*model.Participant, error) {
	participants, err := s.repository.GetEventParticipants(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventParticipant.WithError(err).WithMessage("Failed to get event participants").Cause()
	}

	var res []*model.Participant
	for _, p := range participants {
		res = append(res, &model.Participant{
			UserID:         p.UserID,
			EventID:        p.EventID,
			TeamID:         p.TeamID,
			Name:           p.Name,
			ApprovalStatus: p.ApprovalStatus,
			CreatedAt:      p.CreatedAt,
		})
	}
	return res, nil
}

func (s *EventService) GetEventParticipantStatus(ctx context.Context, eventID, userID uuid.UUID) (int32, error) {
	status, err := s.repository.GetEventParticipantStatus(ctx, postgres.GetEventParticipantStatusParams{
		EventID: eventID,
		UserID:  userID,
	})
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return model.NoParticipationStatus, nil
		}
		return model.NoParticipationStatus, model.ErrEventParticipant.WithError(err).WithMessage("Failed to get event join status").Cause()
	}
	return status, nil
}

func (s *EventService) GetParticipantTeam(ctx context.Context, eventID, userID uuid.UUID) (*model.Team, error) {
	team, err := s.repository.GetEventParticipantTeam(ctx, postgres.GetEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
	})
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, model.ErrEventParticipantTeamNotFound.WithContext("eventID", eventID).WithContext("userID", userID).Cause()
		}
		return nil, model.ErrEventParticipant.WithError(err).WithMessage("Failed to get participant team").Cause()
	}

	return &model.Team{
		ID:           team.ID,
		EventID:      eventID,
		Name:         team.Name,
		JoinCode:     team.JoinCode,
		LaboratoryID: team.LaboratoryID,
	}, nil
}

func (s *EventService) CreateJoinEventRequest(ctx context.Context, participant model.Participant) error {
	if err := s.repository.CreateEventParticipant(ctx, postgres.CreateEventParticipantParams{
		EventID:        participant.EventID,
		UserID:         participant.UserID,
		ApprovalStatus: participant.ApprovalStatus,
		Name:           participant.Name,
	}); err != nil {
		if tools.IsUniqueViolationError(err) {
			return model.ErrEventParticipantExists.WithContext("eventID", participant.EventID).WithContext("userID", participant.UserID).Cause()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrEventParticipant.WithError(err).WithMessage("Failed to create join event request").Cause()
	}
	return nil
}

func (s *EventService) UpdateEventParticipantStatus(ctx context.Context, eventID, userID uuid.UUID, status int32) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
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
			return errCreator.Cause()
		}
		return model.ErrEventParticipant.WithError(err).WithMessage("Failed to update event participant status").Cause()
	}

	if affected == 0 {
		return model.ErrEventParticipantNotFound.WithContext("eventID", eventID).WithContext("userID", userID).Cause()
	}

	return nil
}

func (s *EventService) UpdateEventParticipantName(ctx context.Context, eventID, userID uuid.UUID, name string) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateEventParticipantName(ctx, postgres.UpdateEventParticipantNameParams{
		EventID: eventID,
		UserID:  userID,
		Name:    name,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrEventParticipant.WithError(err).WithMessage("Failed to update event participant name").Cause()
	}

	if affected == 0 {
		return model.ErrEventParticipantNotFound.WithContext("eventID", eventID).WithContext("userID", userID).Cause()
	}

	return nil
}

func (s *EventService) DeleteEventParticipant(ctx context.Context, eventID, userID uuid.UUID) error {
	affected, err := s.repository.DeleteEventParticipant(ctx, postgres.DeleteEventParticipantParams{
		EventID: eventID,
		UserID:  userID,
	})
	if err != nil {
		return model.ErrEventParticipant.WithError(err).WithMessage("Failed to delete event participant").Cause()
	}
	if affected == 0 {
		return model.ErrEventParticipantNotFound.WithContext("eventID", eventID).WithContext("userID", userID).Cause()
	}
	return nil
}
