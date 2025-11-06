package event

import (
	"context"
	"errors"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	ITeamService interface {
		GetEventTeams(ctx context.Context, eventID uuid.UUID, page, pageSize int) ([]*eventModel.Team, error)
		GetEventTeam(ctx context.Context, teamID uuid.UUID) (*eventModel.Team, error)

		GetEventParticipantTeam(ctx context.Context, eventID, userID uuid.UUID) (*eventModel.Team, error)

		CreateEventTeam(ctx context.Context, team eventModel.Team) (*uuid.UUID, error)
		AssignEventTeam(ctx context.Context, eventID, userID, teamID uuid.UUID) error
		GetEventTeamIDFromCredentials(ctx context.Context, team eventModel.Team) (*uuid.UUID, error)
		UnassignEventTeam(ctx context.Context, eventID, userID uuid.UUID) error
		UpdateEventTeamName(ctx context.Context, teamID uuid.UUID, name string) error
		DeleteEventTeam(ctx context.Context, teamID uuid.UUID) error

		CreateLaboratories(
			ctx context.Context,
			networkMask, count int,
			labsGroupID uuid.UUID,
		) ([]uuid.UUID, error)
	}
)

// for administrators

func (u *EventUseCase) GetEventTeams(ctx context.Context, eventID uuid.UUID, page, pageSize int) (
	[]*eventModel.Team,
	error,
) {
	teams, err := u.service.GetEventTeams(ctx, eventID, page, pageSize)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event teams").Err()
	}
	return teams, nil
}

func (u *EventUseCase) GetEventTeam(ctx context.Context, teamID uuid.UUID) (*eventModel.Team, error) {
	team, err := u.service.GetEventTeam(ctx, teamID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event team").Err()
	}
	return team, nil
}

func (u *EventUseCase) CreateEventTeam(ctx context.Context, eventID uuid.UUID, name string) error {
	team := eventModel.Team{
		EventID:      eventID,
		Name:         name,
		LaboratoryID: uuid.NullUUID{UUID: uuid.Nil, Valid: false},
	}
	if _, err := u.service.CreateEventTeam(ctx, team); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create team").Err()
	}
	return nil
}

func (u *EventUseCase) UpdateEventTeamName(ctx context.Context, teamID uuid.UUID, name string) error {
	if err := u.service.UpdateEventTeamName(ctx, teamID, name); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update team").Err()
	}
	return nil
}

func (u *EventUseCase) DeleteEventTeam(ctx context.Context, teamID uuid.UUID) error {
	if err := u.service.DeleteEventTeam(ctx, teamID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete team").Err()
	}
	return nil
}

func (u *EventUseCase) AssignEventTeam(ctx context.Context, eventID, teamID, userID uuid.UUID) error {
	// check if user is joined team
	team, err := u.service.GetEventParticipantTeam(ctx, eventID, userID)
	if err == nil {
		// if user is already in team with the same id, return ok
		if team.ID == teamID {
			return nil
		}
		return eventModel.ErrEventUserAlreadyInTeam.Err()
	} else {
		if !errors.Is(err, eventModel.ErrEventParticipantTeamNotFound.Err()) {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant team").Err()
		}
	}

	// assign user to team
	if err = u.service.AssignEventTeam(ctx, eventID, userID, teamID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to assign user to team").Err()
	}

	return nil
}

func (u *EventUseCase) UnassignEventTeam(ctx context.Context, eventID, userID uuid.UUID) error {
	// check if user is joined team
	if _, err := u.service.GetEventParticipantTeam(ctx, eventID, userID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get participant team").Err()
	}

	// unassign user from team
	if err := u.service.UnassignEventTeam(ctx, eventID, userID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to unassign user from team").Err()
	}

	return nil
}

// for participants

func (u *EventUseCase) GetTeamsInfo(ctx context.Context, eventID uuid.UUID, page, pageSize int) (
	[]*eventModel.TeamInfo,
	error,
) {
	teamsInfo := make([]*eventModel.TeamInfo, 0)
	teams, err := u.GetEventTeams(ctx, eventID, page, pageSize)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event teams").Err()
	}

	for _, team := range teams {
		teamsInfo = append(
			teamsInfo, &eventModel.TeamInfo{
				ID:   team.ID,
				Name: team.Name,
			},
		)
	}

	return teamsInfo, nil
}

func (u *EventUseCase) GetSelfTeam(ctx context.Context, eventID uuid.UUID) (*eventModel.Team, error) {
	// TODO: check it
	// if user is administrator, return default administrator team
	// get current user role
	role, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user role from context").Err()
	}

	if role == userModel.AdministratorRole {
		return &eventModel.Team{
			Name: "Administrator",
		}, nil
	}

	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	// check if user is joined event
	joinedStatus, err := u.GetSelfJoinEventStatus(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get join event status").Err()
	}
	// if status is not approved, return nil
	if joinedStatus != eventModel.ApprovedParticipationStatus {
		return nil, eventModel.ErrEventNotJoined.Err()
	}

	// get user team
	team, err := u.service.GetEventParticipantTeam(ctx, eventID, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get participant team").Err()
	}

	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}

	// if event participation is individual, return name only
	if event.Participation == eventModel.IndividualParticipationType {
		return &eventModel.Team{
			ID:           team.ID,
			Name:         team.Name,
			LaboratoryID: team.LaboratoryID,
		}, nil
	}

	// return only team name and join code
	return &eventModel.Team{
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
		return eventModel.ErrEventUserAlreadyInTeam.Err()
	} else {
		if !errors.Is(err, eventModel.ErrEventParticipantTeamNotFound.Err()) {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to get self team").Err()
		}
	}

	// create laboratory
	IDs, err := u.service.CreateLaboratories(ctx, 26, 1, uuid.Nil)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create laboratory").Err()
	}

	team := eventModel.Team{
		EventID:      eventID,
		Name:         name,
		LaboratoryID: uuid.NullUUID{UUID: IDs[0], Valid: true},
	}

	// create team
	teamID, err := u.service.CreateEventTeam(ctx, team)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create team").Err()
	}

	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	// assign user to team
	if err = u.service.AssignEventTeam(ctx, eventID, userID, *teamID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to assign user to team").Err()
	}

	return nil
}

func (u *EventUseCase) JoinTeam(ctx context.Context, eventID uuid.UUID, name, joinCode string) error {
	// check if user is joined team
	_, err := u.GetSelfTeam(ctx, eventID)
	if err == nil {
		return eventModel.ErrEventUserAlreadyInTeam.Err()
	} else {
		if !errors.Is(err, eventModel.ErrEventParticipantTeamNotFound.Err()) {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to get self team").Err()
		}
	}

	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	// check team credentials
	team := eventModel.Team{
		EventID:  eventID,
		Name:     name,
		JoinCode: joinCode,
	}
	teamID, err := u.service.GetEventTeamIDFromCredentials(ctx, team)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get team id from credentials").Err()
	}

	// join team
	if err = u.service.AssignEventTeam(ctx, eventID, userID, *teamID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to join team").Err()
	}

	return nil
}

func (u *EventUseCase) LeaveTeam(ctx context.Context, eventID uuid.UUID) error {
	// get current user id
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	// leave team
	if err = u.service.UnassignEventTeam(ctx, eventID, userID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to leave team").Err()
	}

	return nil
}

// other

func (u *EventUseCase) ProtectEventTeams(ctx context.Context, eventID uuid.UUID) (bool, error) {
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return true, model.ErrPlatform.WithError(err).WithMessage("Failed to get event by id").Err()
	}

	// if event scoreboard is public, then return true
	if event.ParticipantsVisibility == eventModel.PublicParticipantsVisibilityType {
		return false, nil
	}

	// protect by default
	return true, nil
}
