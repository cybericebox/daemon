package event

import (
	"context"
	"fmt"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"time"
)

type (
	IParticipantService interface {
		GetEventParticipantStatus(ctx context.Context, eventID, userID uuid.UUID) (int32, error)
		CreateJoinEventRequest(ctx context.Context, participant model.Participant) error

		GetUserByID(ctx context.Context, userID uuid.UUID) (*model.User, error)

		GetVPNClientConfig(ctx context.Context, clientID, labCIDR string) (string, error)

		GetLaboratories(ctx context.Context, labIDs ...uuid.UUID) ([]*model.LaboratoryInfo, error)
	}
)

// for administrators

// for participants

func (u *EventUseCase) GetSelfJoinEventStatus(ctx context.Context, eventID uuid.UUID) (int32, error) {
	//TODO: check it
	// if user is administrator, return status as approved
	userRole, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return model.NoParticipationStatus, model.ErrEventParticipant.WithError(err).WithMessage("Failed to get user role from context").Cause()
	}

	if userRole == model.AdministratorRole {
		return model.ApprovedParticipationStatus, nil
	}

	// get current userID
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.NoParticipationStatus, model.ErrEventParticipant.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	// get user participation status
	status, err := u.service.GetEventParticipantStatus(ctx, eventID, userID)
	if err != nil {
		return model.NoParticipationStatus, model.ErrEventParticipant.WithError(err).WithMessage("Failed to get event participant status").Cause()
	}

	return status, nil
}

func (u *EventUseCase) JoinEvent(ctx context.Context, eventID uuid.UUID) error {
	// get event
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return model.ErrEventParticipant.WithError(err).WithMessage("Failed to get event by id").Cause()
	}

	// if event type is competition, check if registration is closed or event is started.
	// if event type is training, check if registration is closed

	if event.Registration == model.ClosedRegistrationType ||
		(event.Type == model.CompetitionEventType && time.Now().After(event.StartTime)) {
		return model.ErrEventRegistrationClosed.Cause()
	}

	// get join event status
	status, err := u.GetSelfJoinEventStatus(ctx, eventID)

	if err != nil {
		return model.ErrEventParticipant.WithError(err).WithMessage("Failed to get join event status").Cause()
	}

	// if user already requested to join event, pass
	if status != model.NoParticipationStatus {
		return nil
	}

	// create join event request
	// get current userID
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrEventParticipant.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	// get user
	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return model.ErrEventParticipant.WithError(err).WithMessage("Failed to get user by id").Cause()
	}

	participationStatus := model.PendingParticipationStatus

	// if registration is open, set status to approved
	if event.Registration == model.OpenRegistrationType {
		participationStatus = model.ApprovedParticipationStatus
	}

	// create join event request
	if err = u.service.CreateJoinEventRequest(ctx, model.Participant{
		UserID:         user.ID,
		EventID:        eventID,
		Name:           user.Name,
		ApprovalStatus: participationStatus,
	}); err != nil {
		return model.ErrEventParticipant.WithError(err).WithMessage("Failed to create join event request").Cause()
	}

	// if registration is open and event participation is individual, create team for user with name as user`s name
	if event.Registration == model.OpenRegistrationType && event.Participation == model.IndividualParticipationType {
		// create team for user with name as user`s name
		if err = u.CreateTeam(ctx, eventID, user.Name); err != nil {
			return model.ErrEventParticipant.WithError(err).WithMessage("Failed to create team").Cause()
		}
	}

	return nil
}

// vpn config for participant

func (u *EventUseCase) GetSelfVPNConfig(ctx context.Context, eventID uuid.UUID) (string, error) {

	//TODO: check it
	// if user is administrator, return empty config
	// get current user role
	role, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return "", model.ErrEventParticipant.WithError(err).WithMessage("Failed to get user role from context").Cause()
	}

	if role == model.AdministratorRole {
		return "", nil
	}

	// check if user is joined team

	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return "", model.ErrEventParticipant.WithError(err).WithMessage("Failed to get user id from context").Cause()
	}

	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return "", model.ErrEventParticipant.WithError(err).WithMessage("Failed to get self team").Cause()
	}

	// get lab cidr by id
	labs, err := u.service.GetLaboratories(ctx, team.LaboratoryID.UUID)
	if err != nil {
		return "", model.ErrEventParticipant.WithError(err).WithMessage("Failed to get laboratories").Cause()
	}

	config, err := u.service.GetVPNClientConfig(ctx, fmt.Sprintf("%s-%s", eventID.String(), userID.String()), labs[0].CIDR)
	if err != nil {
		return "", model.ErrEventParticipant.WithError(err).WithMessage("Failed to get participant vpn config").Cause()
	}

	return config, nil
}
