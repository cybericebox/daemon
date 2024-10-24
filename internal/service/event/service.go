package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
)

type (
	EventService struct {
		repository IRepository
	}

	IRepository interface {
		IChallengeCategoryRepository
		IChallengeSolutionRepository
		ITeamRepository
		IChallengeRepository
		ITeamChallengeRepository
		IScoreRepository
		IParticipantRepository

		GetEvents(ctx context.Context) ([]postgres.Event, error)
		GetEventByID(ctx context.Context, id uuid.UUID) (postgres.Event, error)
		GetEventByTag(ctx context.Context, tag string) (postgres.Event, error)

		CreateEvent(ctx context.Context, arg postgres.CreateEventParams) error
		DeleteEvent(ctx context.Context, id uuid.UUID) (int64, error)

		UpdateEvent(ctx context.Context, arg postgres.UpdateEventParams) (int64, error)
		UpdateEventPicture(ctx context.Context, arg postgres.UpdateEventPictureParams) (int64, error)

		CountChallengesInEvents(ctx context.Context) ([]postgres.CountChallengesInEventsRow, error)
		CountTeamsInEvents(ctx context.Context) ([]postgres.CountTeamsInEventsRow, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewEventService(deps Dependencies) *EventService {
	return &EventService{
		repository: deps.Repository,
	}
}

func (s *EventService) GetEvents(ctx context.Context) ([]*model.Event, error) {
	events, err := s.repository.GetEvents(ctx)
	if err != nil {
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to get events from repository").Cause()
	}

	challenges, err := s.repository.CountChallengesInEvents(ctx)
	if err != nil {
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to count challenges in events").Cause()
	}

	teams, err := s.repository.CountTeamsInEvents(ctx)
	if err != nil {
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to count teams in events").Cause()
	}

	chaCounts := make(map[uuid.UUID]int64)
	for _, challenge := range challenges {
		chaCounts[challenge.EventID] = challenge.Count
	}

	teamCounts := make(map[uuid.UUID]int64)
	for _, team := range teams {
		teamCounts[team.EventID] = team.Count
	}

	result := make([]*model.Event, 0, len(events))
	for _, event := range events {
		result = append(result, &model.Event{
			ID:                     event.ID,
			Type:                   event.Type,
			Availability:           event.Availability,
			Participation:          event.Participation,
			Tag:                    event.Tag,
			Name:                   event.Name,
			Description:            event.Description,
			Rules:                  event.Rules,
			Picture:                event.Picture,
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
			ChallengesCount:        chaCounts[event.ID],
			TeamsCount:             teamCounts[event.ID],
		})
	}

	return result, nil
}

func (s *EventService) GetEventByID(ctx context.Context, eventID uuid.UUID) (*model.Event, error) {
	event, err := s.repository.GetEventByID(ctx, eventID)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, model.ErrEventEventNotFound.WithContext("eventID", eventID).Cause()
		}
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to get event by id from repository").Cause()
	}
	return &model.Event{
		ID:                     event.ID,
		Type:                   event.Type,
		Availability:           event.Availability,
		Participation:          event.Participation,
		Tag:                    event.Tag,
		Name:                   event.Name,
		Description:            event.Description,
		Rules:                  event.Rules,
		Picture:                event.Picture,
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
	}, nil
}

func (s *EventService) GetEventByTag(ctx context.Context, eventTag string) (*model.Event, error) {
	event, err := s.repository.GetEventByTag(ctx, eventTag)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, model.ErrEventEventNotFound.WithContext("eventTag", eventTag).Cause()
		}
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to get event by tag from repository").Cause()
	}
	return &model.Event{
		ID:                     event.ID,
		Type:                   event.Type,
		Availability:           event.Availability,
		Participation:          event.Participation,
		Tag:                    event.Tag,
		Name:                   event.Name,
		Description:            event.Description,
		Rules:                  event.Rules,
		Picture:                event.Picture,
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
	}, nil
}

func (s *EventService) CreateEvent(ctx context.Context, event model.Event) (*model.Event, error) {
	event.ID = uuid.Must(uuid.NewV7())
	if err := s.repository.CreateEvent(ctx, postgres.CreateEventParams{
		ID:                     event.ID,
		Type:                   event.Type,
		Availability:           event.Availability,
		Participation:          event.Participation,
		Tag:                    event.Tag,
		Name:                   event.Name,
		Description:            event.Description,
		Rules:                  event.Rules,
		Picture:                event.Picture,
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
	}); err != nil {
		if tools.IsUniqueViolationError(err) {
			return nil, model.ErrEventEventExists.Cause()
		}
		return nil, model.ErrEvent.WithError(err).WithMessage("Failed to create event").Cause()
	}
	return &event, nil
}

func (s *EventService) UpdateEvent(ctx context.Context, event model.Event) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Cause()
	}

	affected, err := s.repository.UpdateEvent(ctx, postgres.UpdateEventParams{
		ID:                     event.ID,
		Type:                   event.Type,
		Availability:           event.Availability,
		Name:                   event.Name,
		Description:            event.Description,
		Rules:                  event.Rules,
		Picture:                event.Picture,
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
		if tools.IsUniqueViolationError(err) {
			return model.ErrEventEventExists.Cause()
		}
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Cause()
		}
		return model.ErrEvent.WithError(err).WithMessage("Failed to update event").Cause()
	}

	if affected == 0 {
		return model.ErrEventEventNotFound.WithMessage("Event not found").WithContext("eventID", event.ID).Cause()
	}

	return nil
}

func (s *EventService) RefreshEventPicture(ctx context.Context, eventID uuid.UUID, picture string) error {
	affected, err := s.repository.UpdateEventPicture(ctx, postgres.UpdateEventPictureParams{
		ID:      eventID,
		Picture: picture,
	})
	if err != nil {
		return model.ErrEvent.WithError(err).WithMessage("Failed to update event picture").Cause()
	}

	if affected == 0 {
		return model.ErrEventEventNotFound.WithMessage("Event not found").WithContext("eventID", eventID).Cause()
	}

	return nil
}

func (s *EventService) DeleteEvent(ctx context.Context, eventID uuid.UUID) error {
	affected, err := s.repository.DeleteEvent(ctx, eventID)
	if err != nil {
		return model.ErrEvent.WithError(err).WithMessage("Failed to delete event").Cause()
	}

	if affected == 0 {
		return model.ErrEventEventNotFound.WithMessage("Event not found").WithContext("eventID", eventID).Cause()
	}
	return nil
}
