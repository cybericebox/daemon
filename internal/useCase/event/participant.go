package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	laboratoryModel "github.com/cybericebox/daemon/internal/model/laboratory"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	IParticipantService interface {
		GetEventParticipantStatus(ctx context.Context, eventID, userID uuid.UUID) (int32, error)
		GetEventParticipants(ctx context.Context, eventID uuid.UUID, page, pageSize int) (
			[]*eventModel.Participant,
			error,
		)
		CreateJoinEventRequest(ctx context.Context, participant eventModel.Participant) error

		GetUserByID(ctx context.Context, userID uuid.UUID) (*userModel.User, error)

		UpdateEventParticipantStatus(ctx context.Context, eventID, userID uuid.UUID, status int32) error
		DeleteEventParticipant(ctx context.Context, eventID, userID uuid.UUID) error

		GetVPNClientConfig(ctx context.Context, ids laboratoryModel.VPNClientIDs) (string, error)
		RemoveVPNClients(ctx context.Context, ids laboratoryModel.VPNClientIDs) error
	}
)

// for administrators

func (u *EventUseCase) GetEventParticipants(
	ctx context.Context,
	eventID uuid.UUID,
	page, pageSize int,
) ([]*eventModel.Participant, error) {
	participants, err := u.service.GetEventParticipants(ctx, eventID, page, pageSize)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participants").Err()
	}

	return participants, nil
}

func (u *EventUseCase) UpdateEventParticipantStatus(
	ctx context.Context,
	eventID, userID uuid.UUID,
	status int32,
) error {
	// update event participant status
	if err := u.service.UpdateEventParticipantStatus(ctx, eventID, userID, status); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update event participant status").Err()
	}

	return nil
}

func (u *EventUseCase) DeleteEventParticipant(ctx context.Context, eventID, userID uuid.UUID) error {
	// delete event participant
	if err := u.service.DeleteEventParticipant(ctx, eventID, userID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event participant").Err()
	}

	return nil
}

func (u *EventUseCase) DeleteEventParticipantVPNConfigs(ctx context.Context, eventID uuid.UUID) error {
	if err := u.service.RemoveVPNClients(
		ctx, laboratoryModel.VPNClientIDs{
			EventID: eventID,
		},
	); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event participant vpn configs").Err()
	}

	return nil
}

// for participants

func (u *EventUseCase) GetSelfJoinEventStatus(ctx context.Context, eventID uuid.UUID) (int32, error) {
	// TODO: check it
	// if user is administrator, return status as approved
	userRole, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return eventModel.NoParticipationStatus, model.ErrPlatform.WithError(err).WithMessage("Failed to get user role from context").Err()
	}

	if userRole == userModel.AdministratorRole {
		return eventModel.ApprovedParticipationStatus, nil
	}

	// get current userID
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return eventModel.NoParticipationStatus, model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	// get user participation status
	status, err := u.service.GetEventParticipantStatus(ctx, eventID, userID)
	if err != nil {
		return eventModel.NoParticipationStatus, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant status").Err()
	}

	return status, nil
}

func (u *EventUseCase) JoinEvent(ctx context.Context, eventID uuid.UUID) error {
	// get event
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event by id").Err()
	}

	// if event type is competition, check if registration is closed or event is started.
	// if event type is training, check if registration is closed

	if event.Registration == eventModel.ClosedRegistrationType ||
		(event.Type == eventModel.CompetitionEventType && time.Now().After(event.StartTime)) {
		return eventModel.ErrEventRegistrationClosed.Err()
	}

	// get join event status
	status, err := u.GetSelfJoinEventStatus(ctx, eventID)

	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get join event status").Err()
	}

	// if user already requested to join event, pass
	if status != eventModel.NoParticipationStatus {
		return nil
	}

	// create join event request
	// get current userID
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	// get user
	user, err := u.service.GetUserByID(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get user by id").Err()
	}

	participationStatus := eventModel.PendingParticipationStatus

	// if registration is open, set status to approved
	if event.Registration == eventModel.OpenRegistrationType {
		participationStatus = eventModel.ApprovedParticipationStatus
	}

	// create join event request
	if err = u.service.CreateJoinEventRequest(
		ctx, eventModel.Participant{
			UserID:         user.ID,
			EventID:        eventID,
			ApprovalStatus: participationStatus,
		},
	); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create join event request").Err()
	}

	// if registration is open and event participation is individual, create team for user with name as user`s name
	if event.Registration == eventModel.OpenRegistrationType && event.Participation == eventModel.IndividualParticipationType {
		// create team for user with name as user`s name
		if err = u.CreateTeam(ctx, eventID, user.Name); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to create team").Err()
		}
	}

	return nil
}

// vpn config for participant

func (u *EventUseCase) GetSelfVPNConfig(ctx context.Context, eventID uuid.UUID) (string, error) {

	// TODO: check it
	// if user is administrator, return empty config
	// get current user role
	role, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get user role from context").Err()
	}

	if role == userModel.AdministratorRole {
		return "", nil
	}

	// check if user is joined team

	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get user id from context").Err()
	}

	team, err := u.GetSelfTeam(ctx, eventID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get self team").Err()
	}

	config, err := u.service.GetVPNClientConfig(
		ctx, laboratoryModel.VPNClientIDs{
			EventID: eventID,
			UserID:  userID,
			TeamID:  team.ID,
		},
	)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get participant vpn config").Err()
	}

	return config, nil
}
