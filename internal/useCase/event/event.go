package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	"time"
)

type (
	ISingleEventService interface {
		GetEventByID(ctx context.Context, eventID uuid.UUID) (*model.Event, error)
		GetEventByTag(ctx context.Context, eventTag string) (*model.Event, error)

		UpdateEvent(ctx context.Context, event *model.Event) error

		DeleteEvent(ctx context.Context, eventID uuid.UUID) error

		GetParticipantJoinEventStatus(ctx context.Context, eventID, userID uuid.UUID) (int32, error)
		CreateJoinEventRequest(ctx context.Context, eventID, userID uuid.UUID, status int32) error

		GetUserByID(ctx context.Context, userID uuid.UUID) (*model.User, error)
	}
)

func (u *EventUseCase) GetEvent(ctx context.Context, eventID uuid.UUID) (*model.Event, error) {
	return u.service.GetEventByID(ctx, eventID)
}

func (u *EventUseCase) GetEventInfo(ctx context.Context, eventID uuid.UUID) (*model.EventInfo, error) {
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, err
	}

	return &model.EventInfo{
		Type:                   event.Type,
		Participation:          event.Participation,
		Tag:                    event.Tag,
		Name:                   event.Name,
		Description:            event.Description,
		Rules:                  event.Rules,
		Picture:                event.Picture,
		Registration:           event.Registration,
		ScoreboardAvailability: event.ScoreboardAvailability,
		ParticipantsVisibility: event.ParticipantsVisibility,
		StartTime:              event.StartTime,
		FinishTime:             event.FinishTime,
	}, nil
}

func (u *EventUseCase) UpdateEvent(ctx context.Context, event *model.Event) error {
	// check if event time is changed
	// get old event
	oldEvent, err := u.GetEvent(ctx, event.ID)
	if err != nil {
		return err
	}

	// if any event time is changed, we need update workers
	// if event start time is changed we need to update start event worker
	if oldEvent.StartTime != event.StartTime {
		// update start event worker
		// task to create event team challenges on event start
		u.worker.AddTask(worker.Task{
			Do: func() {
				if err = u.service.CreateEventTeamsChallenges(ctx, event.ID); err != nil {
					log.Error().Err(err).Msg("failed to create event teams challenges")
				}
			},
			CheckIfNeedToDo: func() (bool, *time.Time) {
				e, err := u.service.GetEventByID(ctx, event.ID)
				if err != nil {
					log.Error().Err(err).Msg("failed to get event")
					return false, nil
				}

				next := e.StartTime.Add(-time.Minute)

				return e.StartTime.Add(-time.Minute).Before(time.Now().UTC()), &next
			},
			TimeToDo: event.StartTime.Add(-time.Minute),
		})
	}

	return u.service.UpdateEvent(ctx, event)
}

func (u *EventUseCase) DeleteEvent(ctx context.Context, eventID uuid.UUID) error {
	return u.service.DeleteEvent(ctx, eventID)
}

// for event participation

func (u *EventUseCase) GetJoinEventStatus(ctx context.Context, eventID uuid.UUID) (int32, error) {
	// get current userID
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.NoParticipationStatus, err
	}

	// if user is administrator, return true

	// get user role
	userRole, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return model.NoParticipationStatus, err
	}

	if userRole == model.AdministratorRole {
		return model.ApprovedParticipationStatus, nil
	}

	// get user participation status
	status, err := u.service.GetParticipantJoinEventStatus(ctx, eventID, userID)
	if err != nil {
		return model.NoParticipationStatus, err
	}

	return status, nil
}

func (u *EventUseCase) JoinEvent(ctx context.Context, eventID uuid.UUID) error {
	// get event
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return err
	}

	// if event type is competition, check if registration is closed or event is started.
	// if event type is training, check if registration is closed

	if event.Registration == model.ClosedRegistrationType ||
		(event.Type == model.CompetitionEventType && time.Now().After(event.StartTime)) {
		return model.ErrEventRegistrationClosed
	}

	// get join event status
	status, err := u.GetJoinEventStatus(ctx, eventID)

	if err != nil {
		return err
	}

	// if user already requested to join event, return error
	if status != model.NoParticipationStatus {
		return model.ErrEventAlreadyJoined
	}

	// get current userID
	userID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return err
	}

	participationStatus := model.PendingParticipationStatus

	// if registration is open, set status to approved
	if event.Registration == model.OpenRegistrationType {
		participationStatus = model.ApprovedParticipationStatus
	}

	// create join event request
	if err = u.service.CreateJoinEventRequest(ctx, eventID, userID, participationStatus); err != nil {
		return err
	}

	// if registration is open, create participant for user
	if event.Registration == model.OpenRegistrationType {
		// if event participation is individual, create team for user with name as user`s name
		if event.Participation == model.IndividualParticipationType {
			// get user
			user, err := u.service.GetUserByID(ctx, userID)
			if err != nil {
				return err
			}
			// create team for user with name as user`s name
			if err = u.CreateTeam(ctx, eventID, user.Name); err != nil {
				return err
			}
		}
	}

	return nil
}

// for helpful functions

func (u *EventUseCase) GetEventIDByTag(ctx context.Context, eventTag string) (uuid.UUID, error) {
	event, err := u.service.GetEventByTag(ctx, eventTag)
	if err != nil {
		return uuid.Nil, err
	}

	return event.ID, nil
}

func (u *EventUseCase) ShouldProxyEvent(ctx context.Context, eventTag string) bool {
	event, err := u.service.GetEventByTag(ctx, eventTag)
	if err != nil {
		return false
	}

	// if user is administrator, return true
	userRole, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err == nil {
		if userRole == model.AdministratorRole {
			return true
		}
	}

	// if event is published and not withdrawn
	if time.Now().UTC().After(event.PublishTime) && time.Now().UTC().Before(event.WithdrawTime) {
		return true
	}

	return false
}
