package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/lib/pkg/worker"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
)

type (
	IEventHookService interface {
		GetEventByID(ctx context.Context, eventID uuid.UUID) (*eventModel.Event, error)
	}
)

// tasks

func (u *EventUseCase) AddCreateTeamsChallengesTask(ctx context.Context, event eventModel.Event) {
	// task to create event team challenges on event start
	u.worker.AddTask(
		worker.NewTask().
			WithKey(event.ID.String(), "create_teams_challenges").
			WithDo(
				func(ctx context.Context) error {
					// create event teams challenges
					if err := u.CreateEventTeamsChallenges(ctx, event.ID); err != nil {
						return model.ErrPlatform.WithError(err).
							WithMessage("Failed to create event teams challenges").
							WithContext("eventID", event.ID.String()).Err()
					}

					return nil
				},
			).
			WithCheckIfNeedToDo(
				func(ctx context.Context) (bool, *time.Time, error) {
					e, err := u.service.GetEventByID(ctx, event.ID)
					if err != nil {
						log.Error().Err(err).Interface("eventID", event.ID).Msg("Failed to get event")
						return false, nil, model.ErrPlatform.WithError(err).
							WithMessage("Failed to get event").
							WithContext("eventID", event.ID.String()).Err()
					}

					// if event is already finished do not need to do
					if time.Now().After(e.FinishTime) {
						return false, nil, nil
					}

					next := e.StartTime

					return time.Now().After(e.StartTime), &next, nil
				},
			).
			WithTimeToDo(event.StartTime).
			Create(),
	)
}

func (u *EventUseCase) AddDeleteEventTeamsChallengesInfrastructureTask(ctx context.Context, event eventModel.Event) {
	// task to remove event team challenges on event finish
	u.worker.AddTask(
		worker.NewTask().
			WithKey(event.ID.String(), "delete_teams_challenges").
			WithDo(
				func(ctx context.Context) error {
					// delete event teams challenges
					if err := u.DeleteEventTeamsChallengesInfrastructure(ctx, event.ID); err != nil {
						return model.ErrPlatform.WithError(err).
							WithMessage("Failed to delete event teams challenges").
							WithContext("eventID", event.ID.String()).Err()
					}

					return nil
				},
			).
			WithCheckIfNeedToDo(
				func(ctx context.Context) (bool, *time.Time, error) {
					e, err := u.service.GetEventByID(ctx, event.ID)
					if err != nil {
						return false, nil, model.ErrPlatform.WithError(err).
							WithMessage("Failed to get event").
							WithContext("eventID", event.ID.String()).Err()
					}

					// if event is already finished do not need to do
					if time.Now().After(e.FinishTime) {
						return false, nil, nil
					}

					next := e.FinishTime

					return time.Now().After(e.FinishTime), &next, nil
				},
			).
			WithTimeToDo(event.FinishTime).
			Create(),
	)
}

func (u *EventUseCase) AddDeleteEventParticipantVPNConfigsTask(ctx context.Context, event eventModel.Event) {
	// task to remove event participant vpn configs on event withdraw
	u.worker.AddTask(
		worker.NewTask().
			WithKey(event.ID.String(), "delete_participant_vpn_configs").
			WithDo(
				func(ctx context.Context) error {
					// delete event participant vpn configs
					if err := u.DeleteEventParticipantVPNConfigs(ctx, event.ID); err != nil {
						return model.ErrPlatform.WithError(err).
							WithMessage("Failed to delete event participant vpn configs").
							WithContext("eventID", event.ID.String()).Err()
					}

					return nil
				},
			).
			WithCheckIfNeedToDo(
				func(ctx context.Context) (bool, *time.Time, error) {
					e, err := u.service.GetEventByID(ctx, event.ID)
					if err != nil {
						return false, nil, model.ErrPlatform.WithError(err).
							WithMessage("Failed to get event").
							WithContext("eventID", event.ID.String()).Err()
					}

					// if event is already withdraw do not need to do
					if time.Now().After(e.WithdrawTime) {
						return false, nil, nil
					}

					next := e.WithdrawTime

					return time.Now().After(e.WithdrawTime), &next, nil
				},
			).
			WithTimeToDo(event.WithdrawTime).
			Create(),
	)

}

// hooks

func (u *EventUseCase) OnEventPublishes(ctx context.Context, event eventModel.Event) {

}

func (u *EventUseCase) OnEventStarts(ctx context.Context, event eventModel.Event) {
	// task to create event team challenges on event start
	u.AddCreateTeamsChallengesTask(ctx, event)
}

func (u *EventUseCase) OnEventFinishes(ctx context.Context, event eventModel.Event) {
	// task to remove event team challenges on event finish
	u.AddDeleteEventTeamsChallengesInfrastructureTask(ctx, event)
}

func (u *EventUseCase) OnEventWithdraws(ctx context.Context, event eventModel.Event) {
	// task to remove event participant vpn configs on event withdraw
	u.AddDeleteEventParticipantVPNConfigsTask(ctx, event)
}

func (u *EventUseCase) InitEventHooks(ctx context.Context, event eventModel.Event) {
	// task on event publishes
	u.OnEventPublishes(ctx, event)
	// task on event starts
	u.OnEventStarts(ctx, event)
	// task on event finishes
	u.OnEventFinishes(ctx, event)
	// task on event withdraws
	u.OnEventWithdraws(ctx, event)
}

func (u *EventUseCase) UpdateEventHooks(ctx context.Context, event, oldEvent eventModel.Event) {
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
	events, err := u.GetEvents(ctx, config.AllPages, 0)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get events").Err()
	}

	for _, event := range events {
		u.InitEventHooks(ctx, *event)
	}

	return nil
}
