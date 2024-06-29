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
	EventUseCase struct {
		service IEventService
		worker  Worker
	}

	IEventService interface {
		IChallengeService
		IChallengeCategoryService
		ISingleEventService
		ITeamService
		IScoreService

		GetEvents(ctx context.Context) ([]*model.Event, error)
		CreateEvent(ctx context.Context, event *model.Event) (*model.Event, error)

		CreateEventTeamsChallenges(ctx context.Context, eventID uuid.UUID) error
	}

	Worker interface {
		AddTask(task worker.Task)
	}

	Dependencies struct {
		Service IEventService
		Worker  Worker
	}
)

func NewUseCase(deps Dependencies) *EventUseCase {
	return &EventUseCase{
		service: deps.Service,
		worker:  deps.Worker,
	}
}

func (u *EventUseCase) GetEvents(ctx context.Context) ([]*model.Event, error) {
	return u.service.GetEvents(ctx)
}

func (u *EventUseCase) GetEventsInfo(ctx context.Context) ([]*model.EventInfo, error) {
	eventsInfo := make([]*model.EventInfo, 0)
	events, err := u.GetEvents(ctx)
	if err != nil {
		return nil, err

	}

	for _, event := range events {

		eventsInfo = append(eventsInfo, &model.EventInfo{
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
		})
	}

	return eventsInfo, nil
}

func (u *EventUseCase) CreateEvent(ctx context.Context, event *model.Event) error {
	event, err := u.service.CreateEvent(ctx, event)
	if err != nil {
		return err
	}

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

	return nil
}

func (u *EventUseCase) CreateEventTeamsChallengesTasks(ctx context.Context) error {
	// get all events
	events, err := u.GetEvents(ctx)
	if err != nil {
		return err
	}

	for _, event := range events {
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

	return nil
}
