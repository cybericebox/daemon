package event

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	storageModel "github.com/cybericebox/daemon/internal/model/storage"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/tools"
)

type (
	ISingleEventService interface {
		GetEventByID(ctx context.Context, eventID uuid.UUID) (*eventModel.Event, error)
		GetEventWithMetadataByID(ctx context.Context, eventID uuid.UUID) (*eventModel.Event, error)
		GetEventByTag(ctx context.Context, eventTag string) (*eventModel.Event, error)

		UpdateEvent(ctx context.Context, event eventModel.Event) error
		RefreshEventPicture(ctx context.Context, eventID uuid.UUID, picture string) error

		DeleteEvent(ctx context.Context, eventID uuid.UUID) error

		GetDownloadFileURL(
			ctx context.Context,
			params storageModel.DownloadFileParams,
		) (storageModel.DownloadFileURL, error)
		DeleteFiles(ctx context.Context, files ...storageModel.File) error
	}
)

// for administrators

func (u *EventUseCase) GetEvent(ctx context.Context, eventID uuid.UUID) (*eventModel.Event, error) {
	event, err := u.service.GetEventWithMetadataByID(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event by id").Err()
	}

	// check banner link if exists
	if event.Picture != "" {
		event.Picture, err = u.refreshPictureLink(ctx, eventID, event.Picture)
		if err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to refresh banner link").Err()
		}
	}

	return event, nil
}

func (u *EventUseCase) GetEventBannerDownloadLink(ctx context.Context, eventID uuid.UUID) (string, error) {
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}

	return event.Picture, nil
}

func (u *EventUseCase) UpdateEvent(ctx context.Context, event eventModel.Event) error {
	// get old event
	oldEvent, err := u.GetEvent(ctx, event.ID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get old event").Err()
	}

	// check if event banner is changed
	if !equalPictureURLs(oldEvent.Picture, event.Picture) {
		// delete old event banner if exists
		if oldEvent.Picture != "" {
			fileID, err := parsePictureURL(oldEvent.Picture)
			if err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to parse old picture url").Err()
			}

			if err = u.service.DeleteFiles(
				ctx,
				storageModel.File{ID: fileID, StorageType: storageModel.BannerStorageType},
			); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to delete old file").Err()
			}
		}
		// confirm new event banner if exists
		if event.Picture != "" {
			fileID, err := parsePictureURL(event.Picture)
			if err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to parse new picture url").Err()
			}

			if err = u.service.ConfirmUploadFiles(ctx, fileID); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to confirm file upload").Err()
			}
		}
	}

	// update event hooks if needed
	u.UpdateEventHooks(ctx, event, *oldEvent)

	if err = u.service.UpdateEvent(ctx, event); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to update event").Err()
	}
	return nil
}

func (u *EventUseCase) DeleteEvent(ctx context.Context, eventID uuid.UUID) error {
	// delete event banner if exists
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}

	// delete event challenges infrastructure
	if err = u.DeleteEventTeamsChallengesInfrastructure(ctx, eventID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event teams challenges infrastructure").Err()
	}

	// delete event participant vpn configs
	if err = u.DeleteEventParticipantVPNConfigs(ctx, eventID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event participant vpn configs").Err()
	}

	// delete event
	if err = u.service.DeleteEvent(ctx, eventID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event").Err()
	}

	// delete event banner if exists
	if event.Picture != "" {
		fileID, err := parsePictureURL(event.Picture)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to parse picture url").Err()
		}

		if err = u.service.DeleteFiles(
			ctx,
			storageModel.File{ID: fileID, StorageType: storageModel.BannerStorageType},
		); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to delete file").Err()
		}
	}

	return nil
}

// for participants

func (u *EventUseCase) GetEventInfo(ctx context.Context, eventID uuid.UUID) (*eventModel.EventInfo, error) {
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}

	return &eventModel.EventInfo{
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

// for helpful functions

func (u *EventUseCase) GetEventIDByTag(ctx context.Context, eventTag string) (uuid.UUID, error) {
	event, err := u.service.GetEventByTag(ctx, eventTag)
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event by tag").Err()
	}

	return event.ID, nil
}

func (u *EventUseCase) ShouldProxyEvent(ctx context.Context, eventTag string) bool {
	event, err := u.service.GetEventByTag(ctx, eventTag)
	if err != nil {
		log.Debug().Err(err).Msg("Failed to get event by tag")
		return false
	}

	// if user is administrator, return true
	userRole, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err == nil {
		log.Debug().Err(err).Msg("Failed to get user role from context")
		if userRole == userModel.AdministratorRole {
			return true
		}
	}

	// if event is published and not withdrawn
	if time.Now().UTC().After(event.PublishTime) && time.Now().UTC().Before(event.WithdrawTime) {
		return true
	}

	return false
}

//

func (u *EventUseCase) refreshPictureLink(ctx context.Context, eventID uuid.UUID, pictureLink string) (string, error) {
	// check if banner link is valid
	parsedURL, err := url.Parse(pictureLink)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to parse banner url").Err()
	}

	expires := parsedURL.Query().Get("X-Amz-Expires")
	date := parsedURL.Query().Get("X-Amz-Date")

	if expires == "" || date == "" {
		// try parse banner link as file id
		fileID, err := uuid.FromString(pictureLink)
		if err != nil {
			return "", model.ErrPlatform.WithError(err).WithMessage("Failed to parse banner url as file id").Err()
		}

		link, err := u.service.GetDownloadFileURL(
			ctx, storageModel.DownloadFileParams{
				StorageType: storageModel.BannerStorageType,
				FileID:      fileID,
				Expires:     time.Hour * 24,
			},
		)
		if err != nil {
			return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get banner link").Err()
		}
		pictureLink = string(link)

		// update event with new banner link
		if err = u.service.RefreshEventPicture(ctx, eventID, pictureLink); err != nil {
			return "", model.ErrPlatform.WithError(err).WithMessage("Failed to update event picture").Err()
		}

		return pictureLink, nil
	}

	// check if banner link is expired
	assignedDate, err := time.Parse("20060102T150405Z", date)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to parse banner link assign date").Err()
	}

	expiresDuration, err := time.ParseDuration(expires + "s")
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to parse banner link expires duration").Err()
	}

	if time.Now().After(assignedDate.Add(expiresDuration)) {

		splitURL := strings.Split(parsedURL.Path, "/")
		fileID, err := uuid.FromString(splitURL[len(splitURL)-1])
		if err != nil {
			return "", model.ErrPlatform.WithError(err).WithMessage("Failed to parse banner url as file id").Err()
		}

		link, err := u.service.GetDownloadFileURL(
			ctx, storageModel.DownloadFileParams{
				StorageType: storageModel.BannerStorageType,
				FileID:      fileID,
				Expires:     time.Hour * 24,
			},
		)
		if err != nil {
			return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get banner link").Err()
		}

		pictureLink = string(link)

		// update event with new banner link
		if err = u.service.RefreshEventPicture(ctx, eventID, pictureLink); err != nil {
			return "", model.ErrPlatform.WithError(err).WithMessage("Failed to update event picture").Err()
		}

		return pictureLink, nil
	}

	return pictureLink, nil
}

func parsePictureURL(pictureLink string) (uuid.UUID, error) {
	parsedURL, err := url.Parse(pictureLink)
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to parse picture url").Err()
	}

	splitURL := strings.Split(parsedURL.Path, "/")

	return uuid.FromStringOrNil(splitURL[len(splitURL)-1]), nil
}

func equalPictureURLs(pictureLink1, pictureLink2 string) bool {
	fileID1, err := parsePictureURL(pictureLink1)
	if err != nil {
		return false
	}

	fileID2, err := parsePictureURL(pictureLink2)
	if err != nil {
		return false
	}

	return fileID1 == fileID2
}
