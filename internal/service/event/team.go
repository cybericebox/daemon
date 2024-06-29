package event

import (
	"context"
	"database/sql"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"strings"
)

type (
	ITeamRepository interface {
		CreateTeamInEvent(ctx context.Context, arg postgres.CreateTeamInEventParams) error
		GetEventTeamByName(ctx context.Context, arg postgres.GetEventTeamByNameParams) (postgres.GetEventTeamByNameRow, error)
		GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]postgres.GetEventTeamsRow, error)
		TeamExistsInEvent(ctx context.Context, arg postgres.TeamExistsInEventParams) (bool, error)

		GetEventParticipantTeam(ctx context.Context, arg postgres.GetEventParticipantTeamParams) (postgres.GetEventParticipantTeamRow, error)
		GetEventParticipantTeamID(ctx context.Context, arg postgres.GetEventParticipantTeamIDParams) (uuid.NullUUID, error)

		UpdateEventParticipantTeam(ctx context.Context, arg postgres.UpdateEventParticipantTeamParams) error
	}
)

func (s *EventService) GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]*model.Team, error) {
	teams, err := s.repository.GetEventTeams(ctx, eventID)
	if err != nil {
		return nil, err
	}

	result := make([]*model.Team, 0, len(teams))
	for _, team := range teams {
		result = append(result, &model.Team{
			ID:           team.ID,
			Name:         team.Name,
			LaboratoryID: team.LaboratoryID,
		})
	}

	return result, nil
}

func (s *EventService) GetParticipantTeam(ctx context.Context, eventID, userID uuid.UUID) (*model.Team, error) {
	team, err := s.repository.GetEventParticipantTeam(ctx, postgres.GetEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrTeamNotFound
		}
		return nil, err
	}

	return &model.Team{
		ID:           team.ID,
		Name:         team.Name,
		LaboratoryID: team.LaboratoryID,
	}, nil
}

func (s *EventService) CreateTeam(ctx context.Context, eventID uuid.UUID, name string, laboratoryID *uuid.UUID) error {
	// check if team exists
	exists, err := s.repository.TeamExistsInEvent(ctx, postgres.TeamExistsInEventParams{
		EventID: eventID,
		Name:    name,
	})
	if err != nil {
		return err
	}

	if exists {
		return model.ErrTeamExists
	}

	teamID := uuid.Must(uuid.NewV7())

	// create team
	if err = s.repository.CreateTeamInEvent(ctx, postgres.CreateTeamInEventParams{
		ID:       teamID,
		Name:     name,
		JoinCode: uuid.Must(uuid.NewV4()).String(),
		EventID:  eventID,
		LaboratoryID: uuid.NullUUID{
			UUID:  *laboratoryID,
			Valid: laboratoryID != nil,
		},
	}); err != nil {
		return err
	}

	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return err
	}

	// update participant team
	if err = s.repository.UpdateEventParticipantTeam(ctx, postgres.UpdateEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
		TeamID: uuid.NullUUID{
			UUID:  teamID,
			Valid: true,
		},
	}); err != nil {
		return err
	}

	return nil
}

func (s *EventService) JoinTeam(ctx context.Context, eventID uuid.UUID, name, joinCode string) error {
	team, err := s.repository.GetEventTeamByName(ctx, postgres.GetEventTeamByNameParams{
		EventID: eventID,
		Name:    name,
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return model.ErrTeamWrongCredentials
		}
		return err
	}

	if strings.Compare(team.JoinCode, joinCode) != 0 {
		return model.ErrTeamWrongCredentials
	}

	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return err
	}

	// update participant team
	if err = s.repository.UpdateEventParticipantTeam(ctx, postgres.UpdateEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
		TeamID: uuid.NullUUID{
			UUID:  team.ID,
			Valid: true,
		},
	}); err != nil {
		return err
	}

	return nil
}
