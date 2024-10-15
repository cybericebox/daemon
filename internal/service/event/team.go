package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"strings"
)

type (
	ITeamRepository interface {
		CreateTeamInEvent(ctx context.Context, arg postgres.CreateTeamInEventParams) error

		GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]postgres.GetEventTeamsRow, error)
		GetEventTeamByName(ctx context.Context, arg postgres.GetEventTeamByNameParams) (postgres.GetEventTeamByNameRow, error)
		GetEventTeamByID(ctx context.Context, arg postgres.GetEventTeamByIDParams) (postgres.GetEventTeamByIDRow, error)

		UpdateEventTeamName(ctx context.Context, arg postgres.UpdateEventTeamNameParams) (int64, error)
		DeleteEventTeam(ctx context.Context, arg postgres.DeleteEventTeamParams) (int64, error)

		UpdateEventParticipantTeam(ctx context.Context, arg postgres.UpdateEventParticipantTeamParams) (int64, error)
	}
)

func (s *EventService) GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]*model.Team, error) {
	teams, err := s.repository.GetEventTeams(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get teams from repository").Cause()
	}

	result := make([]*model.Team, 0, len(teams))
	for _, team := range teams {
		result = append(result, &model.Team{
			ID:           team.ID,
			EventID:      team.EventID,
			Name:         team.Name,
			JoinCode:     "",
			LaboratoryID: team.LaboratoryID,
			CreatedAt:    team.CreatedAt,
		})
	}

	return result, nil
}

func (s *EventService) GetEventTeam(ctx context.Context, eventID, teamID uuid.UUID) (*model.Team, error) {
	team, err := s.repository.GetEventTeamByID(ctx, postgres.GetEventTeamByIDParams{
		ID:      teamID,
		EventID: eventID,
	})
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, model.ErrEventTeamTeamNotFound.WithContext("teamID", teamID).Cause()
		}
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get team by id").Cause()
	}

	return &model.Team{
		ID:           team.ID,
		EventID:      team.EventID,
		Name:         team.Name,
		LaboratoryID: team.LaboratoryID,
		CreatedAt:    team.CreatedAt,
	}, nil
}

func (s *EventService) CreateTeam(ctx context.Context, eventID uuid.UUID, name string, laboratoryID *uuid.UUID) (*uuid.UUID, error) {
	teamID := uuid.Must(uuid.NewV7())

	// create team
	if err := s.repository.CreateTeamInEvent(ctx, postgres.CreateTeamInEventParams{
		ID:       teamID,
		Name:     name,
		JoinCode: uuid.Must(uuid.NewV4()).String(),
		EventID:  eventID,
		LaboratoryID: uuid.NullUUID{
			UUID:  *laboratoryID,
			Valid: laboratoryID != nil,
		},
	}); err != nil {
		if tools.IsUniqueViolationError(err) {
			return nil, model.ErrEventTeamTeamExists.WithContext("name", name).Cause()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return nil, errCreator.Cause()
		}
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to create team").Cause()
	}

	return &teamID, nil
}

func (s *EventService) UpdateTeamName(ctx context.Context, eventID, teamID uuid.UUID, name string) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateEventTeamName(ctx, postgres.UpdateEventTeamNameParams{
		ID:      teamID,
		EventID: eventID,
		Name:    name,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		if tools.IsUniqueViolationError(err) {
			return model.ErrEventTeamTeamExists.WithContext("name", name).Cause()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to update team name").Cause()
	}

	if affected == 0 {
		return model.ErrEventTeamTeamNotFound.WithContext("teamID", teamID).Cause()
	}

	return nil
}

func (s *EventService) DeleteTeam(ctx context.Context, eventID, teamID uuid.UUID) error {
	affected, err := s.repository.DeleteEventTeam(ctx, postgres.DeleteEventTeamParams{
		ID:      teamID,
		EventID: eventID,
	})
	if err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to delete team").Cause()
	}

	if affected == 0 {
		return model.ErrEventTeamTeamNotFound.WithContext("teamID", teamID).Cause()
	}

	return nil
}

func (s *EventService) JoinTeam(ctx context.Context, eventID, userID uuid.UUID, name, joinCode string) error {
	team, err := s.repository.GetEventTeamByName(ctx, postgres.GetEventTeamByNameParams{
		EventID: eventID,
		Name:    name,
	})
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return model.ErrEventTeamWrongCredentials.Cause()
		}
		return model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get team by name").Cause()
	}

	if strings.Compare(team.JoinCode, joinCode) != 0 {
		return model.ErrEventTeamWrongCredentials.Cause()
	}

	// update participant team
	affected, err := s.repository.UpdateEventParticipantTeam(ctx, postgres.UpdateEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
		TeamID: uuid.NullUUID{
			UUID:  team.ID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to update participant team").Cause()
	}

	if affected == 0 {
		return model.ErrEventParticipantNotFound.WithContext("userID", userID).Cause()
	}

	return nil
}

func (s *EventService) AssignTeam(ctx context.Context, eventID, userID, teamID uuid.UUID) error {
	// update participant team
	affected, err := s.repository.UpdateEventParticipantTeam(ctx, postgres.UpdateEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
		TeamID: uuid.NullUUID{
			UUID:  teamID,
			Valid: true,
		},
	})
	if err != nil {
		// if team not found
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to update participant team").Cause()
	}

	if affected == 0 {
		return model.ErrEventParticipantNotFound.WithContext("userID", userID).Cause()
	}

	return nil
}

func (s *EventService) LeaveTeam(ctx context.Context, eventID, userID uuid.UUID) error {
	affected, err := s.repository.UpdateEventParticipantTeam(ctx, postgres.UpdateEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
		TeamID: uuid.NullUUID{
			UUID:  uuid.Nil,
			Valid: false,
		},
	})
	if err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to update participant team").Cause()
	}

	if affected == 0 {
		return model.ErrEventParticipantNotFound.WithContext("userID", userID).Cause()
	}

	return nil
}
