package event

import (
	"context"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/lib/pkg/worker"

	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	storageModel "github.com/cybericebox/daemon/internal/model/storage"
)

type (
	EventUseCase struct {
		service IEventService
		worker  worker.Worker
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

		GetEvents(ctx context.Context, page, pageSize int) ([]*eventModel.Event, error)
		CreateEvent(ctx context.Context, event eventModel.Event) (*eventModel.Event, error)

		ConfirmUploadFiles(ctx context.Context, fileIDs ...uuid.UUID) error
		GetUploadFileData(ctx context.Context, params storageModel.UploadFileParams) (
			*storageModel.UploadFileData,
			error,
		)
	}

	Dependencies struct {
		Service IEventService
		Worker  worker.Worker
	}
)

func NewUseCase(deps Dependencies) *EventUseCase {
	return &EventUseCase{
		service: deps.Service,
		worker:  deps.Worker,
	}
}

// for administrators

func (u *EventUseCase) GetEvents(ctx context.Context, page, pageSize int) ([]*eventModel.Event, error) {
	events, err := u.service.GetEvents(ctx, page, pageSize)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get events").Err()
	}
	return events, nil
}

func (u *EventUseCase) CreateEvent(ctx context.Context, newEvent eventModel.Event) error {
	createdEvent, err := u.service.CreateEvent(ctx, newEvent)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create event").Err()
	}

	// if event banner is not empty confirm that it is saved
	if newEvent.Picture != "" {
		fileID, err := parsePictureURL(newEvent.Picture)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to parse picture URL").Err()
		}
		if err = u.service.ConfirmUploadFiles(ctx, fileID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to confirm file upload").Err()
		}
	}

	// create event hooks
	u.InitEventHooks(ctx, *createdEvent)

	// TODO: create team for administrators
	return nil
}

func (u *EventUseCase) GetUploadBannerData(ctx context.Context) (*storageModel.UploadFileData, error) {
	uploadBannerData, err := u.service.GetUploadFileData(
		ctx, storageModel.UploadFileParams{
			StorageType: storageModel.BannerStorageType,
		},
	)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get upload banner data").Err()
	}
	return uploadBannerData, nil
}

// for participants

func (u *EventUseCase) GetEventsInfo(ctx context.Context, page, pageSize int) ([]*eventModel.EventInfo, error) {
	eventsInfo := make([]*eventModel.EventInfo, 0)
	events, err := u.GetEvents(ctx, page, pageSize)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get events").Err()
	}

	for _, event := range events {
		eventsInfo = append(
			eventsInfo, &eventModel.EventInfo{
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
			},
		)
	}

	return eventsInfo, nil
}
