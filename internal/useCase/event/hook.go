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

// hooks

func (u *EventUseCase) OnEventStarts(ctx context.Context, event model.Event) {
	// task to create event team challenges on event start
	u.AddCreateTeamsChallengesTask(ctx, event)
}

func (u *EventUseCase) OnEventFinishes(ctx context.Context, event model.Event) {
	// task to remove event team challenges on event finish
	u.AddDeleteEventTeamsChallengesInfrastructureTask(ctx, event.ID)
}

func (u *EventUseCase) InitEventHooks(ctx context.Context, event model.Event) {
	// task on event starts
	u.OnEventStarts(ctx, event)
	// task on event finishes
	u.OnEventFinishes(ctx, event)
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
