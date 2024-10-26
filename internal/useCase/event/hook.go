package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	"time"
)

type (
	IEventHookService interface {
		GetEventByID(ctx context.Context, eventID uuid.UUID) (*model.Event, error)
	}
)

// tasks

func (u *EventUseCase) AddCreateTeamsChallengesTask(ctx context.Context, event model.Event) {
	// task to create event team challenges on event start
	u.worker.AddTask(worker.Task{
		Do: func() {
			// create event teams challenges
			if err := u.CreateEventTeamsChallenges(ctx, event.ID); err != nil {
				log.Error().Err(err).Interface("eventID", event.ID).Msg("Failed to create event teams challenges")
			}
		},
		CheckIfNeedToDo: func() (bool, *time.Time) {
			e, err := u.service.GetEventByID(ctx, event.ID)
			if err != nil {
				log.Error().Err(err).Interface("eventID", event.ID).Msg("Failed to get event")
				return false, nil
			}

			// if event is already finished do not need to do
			if time.Now().After(e.FinishTime) {
				return false, nil
			}

			next := e.StartTime

			return time.Now().After(e.StartTime), &next
		},
		TimeToDo: event.StartTime,
	})
}

func (u *EventUseCase) AddDeleteEventTeamsChallengesInfrastructureTask(ctx context.Context, eventID uuid.UUID) {
	// task to remove event team challenges on event finish
	u.worker.AddTask(worker.Task{
		Do: func() {
			// create event teams challenges
			if err := u.DeleteEventTeamsChallengesInfrastructure(ctx, eventID); err != nil {
				log.Error().Err(err).Interface("eventID", eventID).Msg("Failed to create event teams challenges")
			}
		},
		CheckIfNeedToDo: func() (bool, *time.Time) {
			e, err := u.service.GetEventByID(ctx, eventID)
			if err != nil {
				log.Error().Err(err).Interface("eventID", eventID).Msg("Failed to get event")
				return false, nil
			}

			// if event is already finished do not need to do
			if time.Now().After(e.FinishTime) {
				return false, nil
			}

			next := e.FinishTime

			return time.Now().After(e.FinishTime), &next
		},
		TimeToDo: time.Now(),
	})
}

func (u *EventUseCase) AddDeleteEventParticipantVPNConfigsTask(ctx context.Context, eventID uuid.UUID) {
	// task to remove event participant vpn configs on event withdraw
	u.worker.AddTask(worker.Task{
		Do: func() {
			// create event teams challenges
			if err := u.DeleteEventParticipantVPNConfigs(ctx, eventID); err != nil {
				log.Error().Err(err).Interface("eventID", eventID).Msg("Failed to delete event participant vpn configs")
			}
		},
		CheckIfNeedToDo: func() (bool, *time.Time) {
			e, err := u.service.GetEventByID(ctx, eventID)
			if err != nil {
				log.Error().Err(err).Interface("eventID", eventID).Msg("Failed to get event")
				return false, nil
			}

			// if event is already withdraw do not need to do
			if time.Now().After(e.WithdrawTime) {
				return false, nil
			}

			next := e.WithdrawTime

			return time.Now().After(e.WithdrawTime), &next
		},
		TimeToDo: time.Now(),
	})

}

// hooks

func (u *EventUseCase) OnEventPublishes(ctx context.Context, event model.Event) {

}

func (u *EventUseCase) OnEventStarts(ctx context.Context, event model.Event) {
	// task to create event team challenges on event start
	u.AddCreateTeamsChallengesTask(ctx, event)
}

func (u *EventUseCase) OnEventFinishes(ctx context.Context, event model.Event) {
	// task to remove event team challenges on event finish
	u.AddDeleteEventTeamsChallengesInfrastructureTask(ctx, event.ID)
}

func (u *EventUseCase) OnEventWithdraws(ctx context.Context, event model.Event) {
	// task to remove event participant vpn configs on event withdraw
	u.AddDeleteEventParticipantVPNConfigsTask(ctx, event.ID)
}

func (u *EventUseCase) InitEventHooks(ctx context.Context, event model.Event) {
	// task on event publishes
	u.OnEventPublishes(ctx, event)
	// task on event starts
	u.OnEventStarts(ctx, event)
	// task on event finishes
	u.OnEventFinishes(ctx, event)
	// task on event withdraws
	u.OnEventWithdraws(ctx, event)
}

func (u *EventUseCase) UpdateEventHooks(ctx context.Context, event, oldEvent model.Event) {
	// if publish time is changed
	if event.PublishTime != oldEvent.PublishTime {
		// update publish event worker
		u.OnEventPublishes(ctx, event)
	}
	// if start time is changed
	if event.StartTime != oldEvent.StartTime {
		// update start event worker
		u.OnEventStarts(ctx, event)
	}
	// if finish time is changed
	if event.FinishTime != oldEvent.FinishTime {
		// update finish event worker
		u.OnEventFinishes(ctx, event)
	}
	// if withdraw time is changed
	if event.WithdrawTime != oldEvent.WithdrawTime {
		// update withdraw event worker
		u.OnEventWithdraws(ctx, event)
	}
}

func (u *EventUseCase) InitEventsHooks(ctx context.Context) error {
	// get all events
	events, err := u.GetEvents(ctx)
	if err != nil {
		return model.ErrEvent.WithError(err).WithMessage("Failed to get events").Cause()
	}

	for _, event := range events {
		u.InitEventHooks(ctx, *event)
	}

	return nil
}
