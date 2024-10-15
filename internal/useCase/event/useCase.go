package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/worker"
	"github.com/gofrs/uuid"
	"time"
)

type (
	EventUseCase struct {
		service IEventService
		worker  Worker
	}

	IEventService interface {
		IEventHookService
		ISingleEventService
		IParticipantService
		ITeamService
		IChallengeService
		IChallengeCategoryService
		ITeamChallengeService
		IChallengeSolutionService
		IScoreService

		GetEvents(ctx context.Context) ([]*model.Event, error)
		CreateEvent(ctx context.Context, event model.Event) error

		ConfirmFileUpload(ctx context.Context, fileID uuid.UUID) error
		GetUploadFileData(ctx context.Context, storageType string, expires ...time.Duration) (*model.UploadFileData, error)
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

// for administrators

func (u *EventUseCase) GetEvents(ctx context.Context) ([]*model.Event, error) {
	events, err := u.service.GetEvents(ctx)
	if err != nil {
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to get events").Cause()
	}
	return events, nil
}

func (u *EventUseCase) CreateEvent(ctx context.Context, newEvent model.Event) error {
	if err := u.service.CreateEvent(ctx, newEvent); err != nil {
		return model.ErrEvent.WithError(err).WithMessage("Failed to create event").Cause()
	}

	// if event banner is not empty confirm that it is saved
	if newEvent.Picture != "" {
		fileID, err := parsePictureURL(newEvent.Picture)
		if err != nil {
			return model.ErrEvent.WithError(err).WithMessage("Failed to parse picture URL").Cause()
		}
		if err = u.service.ConfirmFileUpload(ctx, fileID); err != nil {
			return model.ErrEvent.WithError(err).WithMessage("Failed to confirm file upload").Cause()
		}
	}

	// create event hooks
	u.InitEventHooks(ctx, newEvent)

	//TODO: create team for administrators
	return nil
}

func (u *EventUseCase) GetUploadBannerData(ctx context.Context) (*model.UploadFileData, error) {
	uploadBannerData, err := u.service.GetUploadFileData(ctx, model.BannerStorageType)
	if err != nil {
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to get upload banner data").Cause()
	}
	return uploadBannerData, nil
}

// for participants

func (u *EventUseCase) GetEventsInfo(ctx context.Context) ([]*model.EventInfo, error) {
	eventsInfo := make([]*model.EventInfo, 0)
	events, err := u.GetEvents(ctx)
	if err != nil {
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to get events").Cause()
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
