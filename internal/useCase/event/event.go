package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
	"net/url"
	"strings"
	"time"
)

type (
	ISingleEventService interface {
		GetEventByID(ctx context.Context, eventID uuid.UUID) (*model.Event, error)
		GetEventByTag(ctx context.Context, eventTag string) (*model.Event, error)

		UpdateEvent(ctx context.Context, event model.Event) error
		RefreshEventPicture(ctx context.Context, eventID uuid.UUID, picture string) error

		DeleteEvent(ctx context.Context, eventID uuid.UUID) error

		GetDownloadFileLink(ctx context.Context, params model.DownloadFileParams) (string, error)
		DeleteFiles(ctx context.Context, files ...model.File) error
	}
)

// for administrators

func (u *EventUseCase) GetEvent(ctx context.Context, eventID uuid.UUID) (*model.Event, error) {
	event, err := u.service.GetEventByID(ctx, eventID)
	if err != nil {
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to get event by id").Cause()
	}

	// check banner link if exists
	if event.Picture != "" {
		event.Picture, err = u.refreshPictureLink(ctx, eventID, event.Picture)
		if err != nil {
			return nil, model.ErrEvent.WithError(err).WithMessage("Failed to refresh banner link").Cause()
		}
	}

	return event, nil
}

func (u *EventUseCase) GetEventBannerDownloadLink(ctx context.Context, eventID uuid.UUID) (string, error) {
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return "", model.ErrEvent.WithError(err).WithMessage("Failed to get event").Cause()
	}

	return event.Picture, nil
}

func (u *EventUseCase) UpdateEvent(ctx context.Context, event model.Event) error {
	// get old event
	oldEvent, err := u.GetEvent(ctx, event.ID)
	if err != nil {
		return model.ErrEvent.WithError(err).WithMessage("Failed to get old event").Cause()
	}

	// check if event banner is changed
	if event.Picture != oldEvent.Picture {
		// delete old event banner if exists
		if oldEvent.Picture != "" {
			fileID, err := parsePictureURL(oldEvent.Picture)
			if err != nil {
				return model.ErrEvent.WithError(err).WithMessage("Failed to parse old picture url").Cause()
			}

			if err = u.service.DeleteFiles(ctx, model.File{ID: fileID, StorageType: model.BannerStorageType}); err != nil {
				return model.ErrEvent.WithError(err).WithMessage("Failed to delete old file").Cause()
			}
		}
		// confirm new event banner if exists
		if event.Picture != "" {
			fileID, err := parsePictureURL(event.Picture)
			if err != nil {
				return model.ErrEvent.WithError(err).WithMessage("Failed to parse new picture url").Cause()
			}

			if err = u.service.ConfirmFileUpload(ctx, fileID); err != nil {
				return model.ErrEvent.WithError(err).WithMessage("Failed to confirm file upload").Cause()
			}
		}
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

	if err = u.service.UpdateEvent(ctx, event); err != nil {
		return model.ErrEvent.WithError(err).WithMessage("Failed to update event").Cause()
	}
	return nil
}

func (u *EventUseCase) DeleteEvent(ctx context.Context, eventID uuid.UUID) error {
	// delete event banner if exists
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return model.ErrEvent.WithError(err).WithMessage("Failed to get event").Cause()
	}

	// delete event
	if err = u.service.DeleteEvent(ctx, eventID); err != nil {
		return model.ErrEvent.WithError(err).WithMessage("Failed to delete event").Cause()
	}

	// delete event banner if exists
	if event.Picture != "" {
		fileID, err := parsePictureURL(event.Picture)
		if err != nil {
			return model.ErrEvent.WithError(err).WithMessage("Failed to parse picture url").Cause()
		}

		if err = u.service.DeleteFiles(ctx, model.File{ID: fileID, StorageType: model.BannerStorageType}); err != nil {
			return model.ErrEvent.WithError(err).WithMessage("Failed to delete file").Cause()
		}
	}

	if err = u.DeleteEventTeamsChallengesInfrastructure(ctx, eventID); err != nil {
		return model.ErrEvent.WithError(err).WithMessage("Failed to delete event teams challenges infrastructure").Cause()
	}
	return nil
}

// for participants

func (u *EventUseCase) GetEventInfo(ctx context.Context, eventID uuid.UUID) (*model.EventInfo, error) {
	event, err := u.GetEvent(ctx, eventID)
	if err != nil {
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to get event").Cause()
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

// for helpful functions

func (u *EventUseCase) GetEventIDByTag(ctx context.Context, eventTag string) (uuid.UUID, error) {
	event, err := u.service.GetEventByTag(ctx, eventTag)
	if err != nil {
		return uuid.Nil, model.ErrEvent.WithError(err).WithMessage("Failed to get event by tag").Cause()
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

//

func (u *EventUseCase) refreshPictureLink(ctx context.Context, eventID uuid.UUID, pictureLink string) (string, error) {
	// check if banner link is valid
	parsedURL, err := url.Parse(pictureLink)
	if err != nil {
		return "", model.ErrEvent.WithError(err).WithMessage("Failed to parse banner url").Cause()
	}

	expires := parsedURL.Query().Get("X-Amz-Expires")
	date := parsedURL.Query().Get("X-Amz-Date")

	if expires == "" || date == "" {
		// try parse banner link as file id
		fileID, err := uuid.FromString(pictureLink)
		if err != nil {
			return "", model.ErrEvent.WithError(err).WithMessage("Failed to parse banner url as file id").Cause()
		}

		link, err := u.service.GetDownloadFileLink(ctx, model.DownloadFileParams{
			StorageType: model.BannerStorageType,
			FileID:      fileID,
			Expires:     time.Hour * 24,
		})
		if err != nil {
			return "", model.ErrEvent.WithError(err).WithMessage("Failed to get banner link").Cause()
		}
		pictureLink = link

		// update event with new banner link
		if err = u.service.RefreshEventPicture(ctx, eventID, pictureLink); err != nil {
			return "", model.ErrEvent.WithError(err).WithMessage("Failed to update event picture").Cause()
		}

		return pictureLink, nil
	}

	// check if banner link is expired
	assignedDate, err := time.Parse("20060102T150405Z", date)
	if err != nil {
		return "", model.ErrEvent.WithError(err).WithMessage("Failed to parse banner link assign date").Cause()
	}

	expiresDuration, err := time.ParseDuration(expires + "s")
	if err != nil {
		return "", model.ErrEvent.WithError(err).WithMessage("Failed to parse banner link expires duration").Cause()
	}

	if time.Now().After(assignedDate.Add(expiresDuration)) {

		splitURL := strings.Split(parsedURL.Path, "/")
		fileID, err := uuid.FromString(splitURL[len(splitURL)-1])
		if err != nil {
			return "", model.ErrEvent.WithError(err).WithMessage("Failed to parse banner url as file id").Cause()
		}

		link, err := u.service.GetDownloadFileLink(ctx, model.DownloadFileParams{
			StorageType: model.BannerStorageType,
			FileID:      fileID,
			Expires:     time.Hour * 24,
		})
		if err != nil {
			return "", model.ErrEvent.WithError(err).WithMessage("Failed to get banner link").Cause()
		}

		pictureLink = link

		// update event with new banner link
		if err = u.service.RefreshEventPicture(ctx, eventID, pictureLink); err != nil {
			return "", model.ErrEvent.WithError(err).WithMessage("Failed to update event picture").Cause()
		}

		return pictureLink, nil
	}

	return pictureLink, nil
}

func parsePictureURL(pictureLink string) (uuid.UUID, error) {
	parsedURL, err := url.Parse(pictureLink)
	if err != nil {
		return uuid.Nil, model.ErrEvent.WithError(err).WithMessage("Failed to parse picture url").Cause()
	}

	splitURL := strings.Split(parsedURL.Path, "/")

	return uuid.FromStringOrNil(splitURL[len(splitURL)-1]), nil
}
