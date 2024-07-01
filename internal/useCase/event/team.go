package event

import (
	"context"
	"errors"
	"fmt"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	ITeamService interface {
		GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]*model.Team, error)
		GetParticipantTeam(ctx context.Context, eventID, userID uuid.UUID) (*model.Team, error)

		GetParticipantVPNConfig(ctx context.Context, participantID, labCIDR string) (string, error)

		CreateTeam(ctx context.Context, eventID uuid.UUID, name string, laboratoryID *uuid.UUID) error
		JoinTeam(ctx context.Context, eventID uuid.UUID, name, joinCode string) error

		CreateLaboratory(ctx context.Context, networkMask int) (uuid.UUID, error)
		GetLaboratories(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LabInfo, error)
	}
)

func (u *EventUseCase) GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]*model.Team, error) {
	return u.service.GetEventTeams(ctx, eventID)
}

func (u *EventUseCase) GetEventTeamsInfo(ctx context.Context, eventID uuid.UUID) ([]*model.TeamInfo, error) {
	teamsInfo := make([]*model.TeamInfo, 0)
	teams, err := u.GetEventTeams(ctx, eventID)
	if err != nil {
		return nil, err
	}

	for _, team := range teams {
		teamsInfo = append(teamsInfo, &model.TeamInfo{
			ID:   team.ID,
			Name: team.Name,
		})
	}

	return teamsInfo, nil
}

func (u *EventUseCase) CreateTeam(ctx context.Context, eventID uuid.UUID, name string) error {
	// check if user is joined team
	_, err := u.GetSelfTeam(ctx, eventID)
	if err == nil {
		return model.ErrUserAlreadyInTeam
	} else {
		if !errors.Is(err, model.ErrTeamNotFound) {
			return err
		}
	}

	// create laboratory
	laboratoryID, err := u.service.CreateLaboratory(ctx, 26)
	if err != nil {
		return err
	}

	// create team
	if err = u.service.CreateTeam(ctx, eventID, name, &laboratoryID); err != nil {
		return err
	}

	return nil
}

func (u *EventUseCase) JoinTeam(ctx context.Context, eventID uuid.UUID, name, joinCode string) error {
	// check if user is joined team
	_, err := u.GetSelfTeam(ctx, eventID)
	if err == nil {
		return model.ErrUserAlreadyInTeam
	} else {
		if !errors.Is(err, model.ErrTeamNotFound) {
			return err
		}
	}

	// join team
	if err = u.service.JoinTeam(ctx, eventID, name, joinCode); err != nil {
		return err
	}

	return nil
}

func (u *EventUseCase) GetVPNConfig(ctx context.Context, eventID uuid.UUID) (string, error) {
	// if user is administrator, return empty config
	// get current user role
	role, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return "", err
	}

	if role == model.AdministratorRole {
		return "", nil
	}

	// check if user is joined team

	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return "", err
	}

	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return "", err
	}

	// get lab cidr by id
	labs, err := u.service.GetLaboratories(ctx, team.LaboratoryID.UUID)
	if err != nil {
		return "", err
	}

	config, err := u.service.GetParticipantVPNConfig(ctx, fmt.Sprintf("%s-%s", eventID.String(), userID.String()), labs[0].CIDR)
	if err != nil {
		return "", err
	}

	return config, nil
}

func (u *EventUseCase) GetSelfTeam(ctx context.Context, eventID uuid.UUID) (*model.Team, error) {
	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return nil, err
	}

	// if user is administrator, return default administrator team
	// get current user role
	role, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return nil, err
	}

	if role == model.AdministratorRole {
		return &model.Team{
			Name: "Administrator",
		}, nil
	}

	// check if user is joined event
	joinedStatus, err := u.GetJoinEventStatus(ctx, eventID)
	if err != nil {
		return nil, err
	}
	// if status is not approved, return nil
	if joinedStatus != model.ApprovedParticipationStatus {
		return nil, model.ErrEventNotJoined
	}

	// get user team
	team, err := u.service.GetParticipantTeam(ctx, eventID, userID)
	if err != nil {
		return nil, err
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}

	// if event participation is individual, return name only
	if event.Participation == model.IndividualParticipationType {
		return &model.Team{
			ID:           team.ID,
			Name:         team.Name,
			LaboratoryID: team.LaboratoryID,
		}, nil
	}

	// return only team name and join code
	return &model.Team{
		ID:           team.ID,
		Name:         team.Name,
		JoinCode:     team.JoinCode,
		LaboratoryID: team.LaboratoryID,
	}, nil

}
