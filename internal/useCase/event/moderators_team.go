package event

import (
	"context"
	"strings"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

// ModeratorsTeamMemberView is one member of the hidden moderators team: an
// event manager, by name.
type ModeratorsTeamMemberView struct {
	UserID uuid.UUID
	Name   string
	Role   int16
}

// ModeratorsTeamView is the read-only page of the hidden moderators team.
type ModeratorsTeamView struct {
	TeamID  uuid.UUID
	Members []ModeratorsTeamMemberView
}

// GetModeratorsTeam shows the hidden moderators team for the organizers'
// «Моя команда» preview: the team exists on every event, its members are the
// event managers. It records nothing and never appears in results.
func (u *EventUseCase) GetModeratorsTeam(ctx context.Context, eventID uuid.UUID) (ModeratorsTeamView, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return ModeratorsTeamView{}, eventModel.ErrEventNotFound.Err()
		}
		return ModeratorsTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	teamID, err := u.resolveModeratorsTeam(ctx, e)
	if err != nil {
		return ModeratorsTeamView{}, err
	}
	managers, err := u.managers.List(ctx, eventID)
	if err != nil {
		return ModeratorsTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event managers").Err()
	}
	view := ModeratorsTeamView{TeamID: teamID, Members: make([]ModeratorsTeamMemberView, 0, len(managers))}
	for _, manager := range managers {
		user, userErr := u.userProfiles.GetUserByID(ctx, manager.UserID)
		if userErr != nil {
			return ModeratorsTeamView{}, model.ErrPlatform.WithError(userErr).WithMessage("Failed to get event manager").Err()
		}
		name := strings.TrimSpace(user.FirstName + " " + user.LastName)
		if name == "" {
			name = user.Email
		}
		view.Members = append(view.Members, ModeratorsTeamMemberView{UserID: manager.UserID, Name: name, Role: int16(manager.Role)})
	}
	return view, nil
}
