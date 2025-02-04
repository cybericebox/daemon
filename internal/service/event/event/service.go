package eventService

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	EventService struct {
		repository IRepository
	}

	IRepository interface {
		GetEvents(ctx context.Context) ([]postgres.Event, error)
		GetEventsPaged(ctx context.Context, arg postgres.GetEventsPagedParams) ([]postgres.Event, error)
		GetEventsWithMetadata(ctx context.Context) ([]postgres.GetEventsWithMetadataRow, error)
		GetEventsWithMetadataPaged(ctx context.Context, arg postgres.GetEventsWithMetadataPagedParams) ([]postgres.GetEventsWithMetadataPagedRow, error)

		GetEventByID(ctx context.Context, id uuid.UUID) (postgres.Event, error)
		GetEventByTag(ctx context.Context, tag string) (postgres.Event, error)
		GetEventWithMetadataByID(ctx context.Context, eventID uuid.UUID) (postgres.GetEventWithMetadataByIDRow, error)

		CreateEvent(ctx context.Context, arg postgres.CreateEventParams) error
		CreateEventMetadata(ctx context.Context, arg postgres.CreateEventMetadataParams) error

		DeleteEvent(ctx context.Context, id uuid.UUID) (int64, error)

		UpdateEvent(ctx context.Context, arg postgres.UpdateEventParams) (int64, error)
		UpdateEventMetadata(ctx context.Context, arg postgres.UpdateEventMetadataParams) (int64, error)

		GetEventsMetadata(ctx context.Context) ([]postgres.EventsMetadatum, error)
		UpdateEventPicture(ctx context.Context, arg postgres.UpdateEventPictureParams) (int64, error)

		CountChallengesInEvents(ctx context.Context) ([]postgres.CountChallengesInEventsRow, error)
		CountTeamsInEvents(ctx context.Context) ([]postgres.CountTeamsInEventsRow, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *EventService {
	return &EventService{
		repository: deps.Repository,
	}
}

func (s *EventService) GetEvents(ctx context.Context, page int) ([]*eventModel.Event, error) {
	var events []postgres.Event
	var err error

	if page == config.AllPages {
		events, err = s.repository.GetEvents(ctx)
	} else {
		events, err = s.repository.GetEventsPaged(ctx, postgres.GetEventsPagedParams{
			Limit:  config.DefaultOnePageLimit,
			Offset: int32(page * config.DefaultOnePageLimit),
		})
	}

	if err != nil {
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to get events from repository").Err()
	}

	challenges, err := s.repository.CountChallengesInEvents(ctx)
	if err != nil {
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to count challenges in events").Err()
	}

	teams, err := s.repository.CountTeamsInEvents(ctx)
	if err != nil {
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to count teams in events").Err()
	}

	chaCounts := make(map[uuid.UUID]int64)
	for _, challenge := range challenges {
		chaCounts[challenge.EventID] = challenge.Count
	}

	teamCounts := make(map[uuid.UUID]int64)
	for _, team := range teams {
		// subtract 1 because the team count includes the admin team
		teamCounts[team.EventID] = team.Count - 1
	}

	result := make([]*eventModel.Event, 0, len(events))
	for _, event := range events {
		result = append(result, &eventModel.Event{
			ID:                     event.ID,
			Type:                   event.Type,
			Availability:           event.Availability,
			Participation:          event.Participation,
			Tag:                    event.Tag,
			Name:                   event.Name,
			DynamicScoring:         event.DynamicScoring,
			DynamicMaxScore:        event.DynamicMax,
			DynamicMinScore:        event.DynamicMin,
			DynamicSolveThreshold:  event.DynamicSolveThreshold,
			Registration:           event.Registration,
			ScoreboardAvailability: event.ScoreboardAvailability,
			ParticipantsVisibility: event.ParticipantsVisibility,
			PublishTime:            event.PublishTime,
			StartTime:              event.StartTime,
			FinishTime:             event.FinishTime,
			WithdrawTime:           event.WithdrawTime,
			CreatedAt:              event.CreatedAt,
			UpdatedAt:              event.UpdatedAt.Time,
			UpdatedBy:              event.UpdatedBy,
			ChallengesCount:        chaCounts[event.ID],
			TeamsCount:             teamCounts[event.ID],
		})
	}

	return result, nil
}

func (s *EventService) GetEventsWithMetadata(ctx context.Context, page int) ([]*eventModel.Event, error) {
	challenges, err := s.repository.CountChallengesInEvents(ctx)
	if err != nil {
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to count challenges in events").Err()
	}

	teams, err := s.repository.CountTeamsInEvents(ctx)
	if err != nil {
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to count teams in events").Err()
	}

	chaCounts := make(map[uuid.UUID]int64)
	for _, challenge := range challenges {
		chaCounts[challenge.EventID] = challenge.Count
	}

	teamCounts := make(map[uuid.UUID]int64)
	for _, team := range teams {
		// subtract 1 because the team count includes the admin team
		teamCounts[team.EventID] = team.Count - 1
	}

	if page == config.AllPages {
		events, err := s.repository.GetEventsWithMetadata(ctx)
		if err != nil {
			return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to get events from repository").Err()
		}

		result := make([]*eventModel.Event, 0, len(events))
		for _, event := range events {
			result = append(result, &eventModel.Event{
				ID:                     event.ID,
				Type:                   event.Type,
				Availability:           event.Availability,
				Participation:          event.Participation,
				Tag:                    event.Tag,
				Name:                   event.Name,
				Description:            event.Data.Description,
				Rules:                  event.Data.Rules,
				Picture:                event.Data.Picture,
				DynamicScoring:         event.DynamicScoring,
				DynamicMaxScore:        event.DynamicMax,
				DynamicMinScore:        event.DynamicMin,
				DynamicSolveThreshold:  event.DynamicSolveThreshold,
				Registration:           event.Registration,
				ScoreboardAvailability: event.ScoreboardAvailability,
				ParticipantsVisibility: event.ParticipantsVisibility,
				PublishTime:            event.PublishTime,
				StartTime:              event.StartTime,
				FinishTime:             event.FinishTime,
				WithdrawTime:           event.WithdrawTime,
				CreatedAt:              event.CreatedAt,
				UpdatedAt:              event.UpdatedAt.Time,
				UpdatedBy:              event.UpdatedBy,
				ChallengesCount:        chaCounts[event.ID],
				TeamsCount:             teamCounts[event.ID],
			})
		}

		return result, nil

	} else {
		events, err := s.repository.GetEventsWithMetadataPaged(ctx, postgres.GetEventsWithMetadataPagedParams{
			Limit:  config.DefaultOnePageLimit,
			Offset: int32(page * config.DefaultOnePageLimit),
		})
		if err != nil {
			return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to get events from repository").Err()
		}
		result := make([]*eventModel.Event, 0, len(events))
		for _, event := range events {
			result = append(result, &eventModel.Event{
				ID:                     event.ID,
				Type:                   event.Type,
				Availability:           event.Availability,
				Participation:          event.Participation,
				Tag:                    event.Tag,
				Name:                   event.Name,
				Description:            event.Data.Description,
				Rules:                  event.Data.Rules,
				Picture:                event.Data.Picture,
				DynamicScoring:         event.DynamicScoring,
				DynamicMaxScore:        event.DynamicMax,
				DynamicMinScore:        event.DynamicMin,
				DynamicSolveThreshold:  event.DynamicSolveThreshold,
				Registration:           event.Registration,
				ScoreboardAvailability: event.ScoreboardAvailability,
				ParticipantsVisibility: event.ParticipantsVisibility,
				PublishTime:            event.PublishTime,
				StartTime:              event.StartTime,
				FinishTime:             event.FinishTime,
				WithdrawTime:           event.WithdrawTime,
				CreatedAt:              event.CreatedAt,
				UpdatedAt:              event.UpdatedAt.Time,
				UpdatedBy:              event.UpdatedBy,
				ChallengesCount:        chaCounts[event.ID],
				TeamsCount:             teamCounts[event.ID],
			})
		}

		return result, nil
	}
}

func (s *EventService) GetEventByID(ctx context.Context, eventID uuid.UUID) (*eventModel.Event, error) {
	event, err := s.repository.GetEventByID(ctx, eventID)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventEventNotFound.WithContext("eventID", eventID).Err()
		}
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to get event by id from repository").Err()
	}
	return &eventModel.Event{
		ID:                     event.ID,
		Type:                   event.Type,
		Availability:           event.Availability,
		Participation:          event.Participation,
		Tag:                    event.Tag,
		Name:                   event.Name,
		DynamicScoring:         event.DynamicScoring,
		DynamicMaxScore:        event.DynamicMax,
		DynamicMinScore:        event.DynamicMin,
		DynamicSolveThreshold:  event.DynamicSolveThreshold,
		Registration:           event.Registration,
		ScoreboardAvailability: event.ScoreboardAvailability,
		ParticipantsVisibility: event.ParticipantsVisibility,
		PublishTime:            event.PublishTime,
		StartTime:              event.StartTime,
		FinishTime:             event.FinishTime,
		WithdrawTime:           event.WithdrawTime,
		UpdatedAt:              event.UpdatedAt.Time,
		UpdatedBy:              event.UpdatedBy,
		CreatedAt:              event.CreatedAt,
	}, nil
}

func (s *EventService) GetEventByTag(ctx context.Context, eventTag string) (*eventModel.Event, error) {
	event, err := s.repository.GetEventByTag(ctx, eventTag)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventEventNotFound.WithContext("eventTag", eventTag).Err()
		}
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to get event by tag from repository").Err()
	}
	return &eventModel.Event{
		ID:                     event.ID,
		Type:                   event.Type,
		Availability:           event.Availability,
		Participation:          event.Participation,
		Tag:                    event.Tag,
		Name:                   event.Name,
		DynamicScoring:         event.DynamicScoring,
		DynamicMaxScore:        event.DynamicMax,
		DynamicMinScore:        event.DynamicMin,
		DynamicSolveThreshold:  event.DynamicSolveThreshold,
		Registration:           event.Registration,
		ScoreboardAvailability: event.ScoreboardAvailability,
		ParticipantsVisibility: event.ParticipantsVisibility,
		PublishTime:            event.PublishTime,
		StartTime:              event.StartTime,
		FinishTime:             event.FinishTime,
		WithdrawTime:           event.WithdrawTime,
		UpdatedAt:              event.UpdatedAt.Time,
		UpdatedBy:              event.UpdatedBy,
		CreatedAt:              event.CreatedAt,
	}, nil
}

func (s *EventService) GetEventWithMetadataByID(ctx context.Context, eventID uuid.UUID) (*eventModel.Event, error) {
	event, err := s.repository.GetEventWithMetadataByID(ctx, eventID)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventEventNotFound.WithContext("eventID", eventID).Err()
		}
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to get event by id from repository").Err()
	}
	return &eventModel.Event{
		ID:                     event.ID,
		Type:                   event.Type,
		Availability:           event.Availability,
		Participation:          event.Participation,
		Tag:                    event.Tag,
		Name:                   event.Name,
		Description:            event.Data.Description,
		Rules:                  event.Data.Rules,
		Picture:                event.Data.Picture,
		DynamicScoring:         event.DynamicScoring,
		DynamicMaxScore:        event.DynamicMax,
		DynamicMinScore:        event.DynamicMin,
		DynamicSolveThreshold:  event.DynamicSolveThreshold,
		Registration:           event.Registration,
		ScoreboardAvailability: event.ScoreboardAvailability,
		ParticipantsVisibility: event.ParticipantsVisibility,
		PublishTime:            event.PublishTime,
		StartTime:              event.StartTime,
		FinishTime:             event.FinishTime,
		WithdrawTime:           event.WithdrawTime,
		UpdatedAt:              event.UpdatedAt.Time,
		UpdatedBy:              event.UpdatedBy,
		CreatedAt:              event.CreatedAt,
	}, nil
}

func (s *EventService) GetEventsMetadata(ctx context.Context) ([]*eventModel.Event, error) {
	eventsMeta, err := s.repository.GetEventsMetadata(ctx)
	if err != nil {
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to get events metadata from repository").Err()
	}

	result := make([]*eventModel.Event, 0, len(eventsMeta))
	for _, eventMeta := range eventsMeta {
		result = append(result, &eventModel.Event{
			ID:          eventMeta.EventID,
			Picture:     eventMeta.Data.Picture,
			Description: eventMeta.Data.Description,
			Rules:       eventMeta.Data.Rules,
		})
	}

	return result, nil
}

func (s *EventService) CreateEvent(ctx context.Context, event eventModel.Event) (*eventModel.Event, error) {
	event.ID = uuid.Must(uuid.NewV7())
	if err := s.repository.CreateEvent(ctx, postgres.CreateEventParams{
		ID:                     event.ID,
		Type:                   event.Type,
		Availability:           event.Availability,
		Participation:          event.Participation,
		Tag:                    event.Tag,
		Name:                   event.Name,
		DynamicMax:             event.DynamicMaxScore,
		DynamicMin:             event.DynamicMinScore,
		DynamicSolveThreshold:  event.DynamicSolveThreshold,
		Registration:           event.Registration,
		ScoreboardAvailability: event.ScoreboardAvailability,
		ParticipantsVisibility: event.ParticipantsVisibility,
		PublishTime:            event.PublishTime,
		StartTime:              event.StartTime,
		FinishTime:             event.FinishTime,
		WithdrawTime:           event.WithdrawTime,
	}); err != nil {
		errCreator, has := tools.UniqueViolationError(err, eventModel.ErrEventEventExists)
		if has {
			return nil, errCreator.Err()
		}

		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to create event").Err()
	}

	// create event metadata
	if err := s.repository.CreateEventMetadata(ctx, postgres.CreateEventMetadataParams{
		EventID: event.ID,
		Data: eventModel.EventMetadata{
			Description: event.Description,
			Rules:       event.Rules,
			Picture:     event.Picture,
		},
	}); err != nil {
		return nil, eventModel.ErrEvent.WithError(err).WithMessage("Failed to create event metadata").Err()
	}

	return &event, nil
}

func (s *EventService) UpdateEvent(ctx context.Context, event eventModel.Event) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	affected, err := s.repository.UpdateEvent(ctx, postgres.UpdateEventParams{
		ID:                     event.ID,
		Type:                   event.Type,
		Availability:           event.Availability,
		Name:                   event.Name,
		DynamicScoring:         event.DynamicScoring,
		DynamicMax:             event.DynamicMaxScore,
		DynamicMin:             event.DynamicMinScore,
		DynamicSolveThreshold:  event.DynamicSolveThreshold,
		Registration:           event.Registration,
		ScoreboardAvailability: event.ScoreboardAvailability,
		ParticipantsVisibility: event.ParticipantsVisibility,
		PublishTime:            event.PublishTime,
		StartTime:              event.StartTime,
		FinishTime:             event.FinishTime,
		WithdrawTime:           event.WithdrawTime,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.UniqueViolationError(err, eventModel.ErrEventEventExists)
		if has {
			return errCreator.Err()
		}

		errCreator, has = tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return eventModel.ErrEvent.WithError(err).WithMessage("Failed to update event").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventEventNotFound.WithMessage("Event not found").WithContext("eventID", event.ID).Err()
	}

	// update event metadata
	affected, err = s.repository.UpdateEventMetadata(ctx, postgres.UpdateEventMetadataParams{
		EventID: event.ID,
		Data: eventModel.EventMetadata{
			Description: event.Description,
			Rules:       event.Rules,
			Picture:     event.Picture,
		},
	})
	if err != nil {
		return eventModel.ErrEvent.WithError(err).WithMessage("Failed to update event metadata").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventEventNotFound.WithMessage("Event metadata not found").WithContext("eventID", event.ID).Err()
	}

	return nil
}

func (s *EventService) RefreshEventPicture(ctx context.Context, eventID uuid.UUID, picture string) error {
	affected, err := s.repository.UpdateEventPicture(ctx, postgres.UpdateEventPictureParams{
		EventID: eventID,
		Picture: picture,
	})
	if err != nil {
		return eventModel.ErrEvent.WithError(err).WithMessage("Failed to update event picture").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventEventNotFound.WithMessage("Event not found").WithContext("eventID", eventID).Err()
	}

	return nil
}

func (s *EventService) DeleteEvent(ctx context.Context, eventID uuid.UUID) error {
	affected, err := s.repository.DeleteEvent(ctx, eventID)
	if err != nil {
		return eventModel.ErrEvent.WithError(err).WithMessage("Failed to delete event").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventEventNotFound.WithMessage("Event not found").WithContext("eventID", eventID).Err()
	}

	return nil
}
