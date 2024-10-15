package event

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	ITeamService interface {
		GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]*model.Team, error)
		GetEventTeam(ctx context.Context, eventID, teamID uuid.UUID) (*model.Team, error)

		GetParticipantTeam(ctx context.Context, eventID, userID uuid.UUID) (*model.Team, error)

		CreateTeam(ctx context.Context, eventID uuid.UUID, name string, laboratoryID *uuid.UUID) (*uuid.UUID, error)
		JoinTeam(ctx context.Context, eventID, userID uuid.UUID, name, joinCode string) error
		AssignTeam(ctx context.Context, eventID, userID, teamID uuid.UUID) error
		LeaveTeam(ctx context.Context, eventID, userID uuid.UUID) error
		UpdateTeamName(ctx context.Context, eventID, teamID uuid.UUID, name string) error
		DeleteTeam(ctx context.Context, eventID, teamID uuid.UUID) error

		CreateLaboratories(ctx context.Context, networkMask, count int) ([]uuid.UUID, error)
	}
)

// for administrators

func (u *EventUseCase) GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]*model.Team, error) {
	teams, err := u.service.GetEventTeams(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get event teams").Cause()
	}
	return teams, nil
}

func (u *EventUseCase) GetEventTeam(ctx context.Context, eventID, teamID uuid.UUID) (*model.Team, error) {
	team, err := u.service.GetEventTeam(ctx, eventID, teamID)
	if err != nil {
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get event team").Cause()
	}
	return team, nil
}

func (u *EventUseCase) CreateEventTeam(ctx context.Context, eventID uuid.UUID, name string) error {
	if _, err := u.service.CreateTeam(ctx, eventID, name, nil); err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to create team").Cause()
	}
	return nil
}

func (u *EventUseCase) UpdateEventTeamName(ctx context.Context, eventID, teamID uuid.UUID, name string) error {
	if err := u.service.UpdateTeamName(ctx, eventID, teamID, name); err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to update team").Cause()
	}
	return nil
}

func (u *EventUseCase) DeleteEventTeam(ctx context.Context, eventID, teamID uuid.UUID) error {
	if err := u.service.DeleteTeam(ctx, eventID, teamID); err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to delete team").Cause()
	}
	return nil
}

func (u *EventUseCase) AssignEventTeam(ctx context.Context, eventID, teamID, userID uuid.UUID) error {
	// check if user is joined team
	team, err := u.service.GetParticipantTeam(ctx, eventID, userID)
	if err == nil {
		// if user is already in team with the same id, return ok
		if team.ID == teamID {
			return nil
		}
		return model.ErrEventTeamUserAlreadyInTeam.Err()
	} else {
		if !errors.Is(err, model.ErrEventParticipantTeamNotFound.Err()) {
			return model.ErrEventTeam.WithError(err).WithMessage("Failed to get participant team").Cause()
		}
	}

	// assign user to team
	if err = u.service.AssignTeam(ctx, eventID, userID, teamID); err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to assign user to team").Cause()
	}

	return nil
}

func (u *EventUseCase) UnassignEventTeam(ctx context.Context, eventID, userID uuid.UUID) error {
	// check if user is joined team
	if _, err := u.service.GetParticipantTeam(ctx, eventID, userID); err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to get participant team").Cause()
	}

	// unassign user from team
	if err := u.service.LeaveTeam(ctx, eventID, userID); err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to unassign user from team").Cause()
	}

	return nil
}

// for participants

func (u *EventUseCase) GetTeamsInfo(ctx context.Context, eventID uuid.UUID) ([]*model.TeamInfo, error) {
	teamsInfo := make([]*model.TeamInfo, 0)
	teams, err := u.GetEventTeams(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get event teams").Cause()
	}

	for _, team := range teams {
		teamsInfo = append(teamsInfo, &model.TeamInfo{
			ID:   team.ID,
			Name: team.Name,
		})
	}

	return teamsInfo, nil
}

func (u *EventUseCase) GetSelfTeam(ctx context.Context, eventID uuid.UUID) (*model.Team, error) {
	// TODO: check it
	// if user is administrator, return default administrator team
	// get current user role
	role, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get user role from context").Cause()
	}

	if role == model.AdministratorRole {
		return &model.Team{
			Name: "Administrator",
		}, nil
	}

	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	// check if user is joined event
	joinedStatus, err := u.GetSelfJoinEventStatus(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get join event status").Cause()
	}
	// if status is not approved, return nil
	if joinedStatus != model.ApprovedParticipationStatus {
		return nil, model.ErrEventEventNotJoined.Cause()
	}

	// get user team
	team, err := u.service.GetParticipantTeam(ctx, eventID, userID)
	if err != nil {
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get participant team").Cause()
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventTeam.WithError(err).WithMessage("Failed to get event").Cause()
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

func (u *EventUseCase) CreateTeam(ctx context.Context, eventID uuid.UUID, name string) error {
	// check if user is joined team
	_, err := u.GetSelfTeam(ctx, eventID)
	if err == nil {
		return model.ErrEventTeamUserAlreadyInTeam.Cause()
	} else {
		if !errors.Is(err, model.ErrEventParticipantTeamNotFound.Err()) {
			return model.ErrEventTeam.WithError(err).WithMessage("Failed to get self team").Cause()
		}
	}

	// create laboratory
	IDs, err := u.service.CreateLaboratories(ctx, 26, 1)
	if err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to create laboratory").Cause()
	}

	// create team
	teamID, err := u.service.CreateTeam(ctx, eventID, name, &IDs[0])
	if err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to create team").Cause()
	}

	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	// assign user to team
	if err = u.service.AssignTeam(ctx, eventID, userID, *teamID); err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to assign user to team").Cause()
	}

	return nil
}

func (u *EventUseCase) JoinTeam(ctx context.Context, eventID uuid.UUID, name, joinCode string) error {
	// check if user is joined team
	_, err := u.GetSelfTeam(ctx, eventID)
	if err == nil {
		return model.ErrEventTeamUserAlreadyInTeam.Cause()
	} else {
		if !errors.Is(err, model.ErrEventParticipantTeamNotFound.Err()) {
			return model.ErrEventTeam.WithError(err).WithMessage("Failed to get self team").Cause()
		}
	}

	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	// join team
	if err = u.service.JoinTeam(ctx, eventID, userID, name, joinCode); err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to join team").Cause()
	}

	return nil
}

func (u *EventUseCase) LeaveTeam(ctx context.Context, eventID uuid.UUID) error {
	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	// leave team
	if err = u.service.LeaveTeam(ctx, eventID, userID); err != nil {
		return model.ErrEventTeam.WithError(err).WithMessage("Failed to leave team").Cause()
	}

	return nil
}

// other

func (u *EventUseCase) ProtectEventTeams(ctx context.Context, eventID uuid.UUID) (bool, error) {
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return true, model.ErrEventTeam.WithError(err).WithMessage("Failed to get event by id").Cause()
	}

	// if event scoreboard is public, then return true
	if event.ParticipantsVisibility == model.PublicParticipantsVisibilityType {
		return false, nil
	}

	// protect by default
	return true, nil
}
