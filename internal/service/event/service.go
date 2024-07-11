package event

import (
	"context"
	"database/sql"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/gofrs/uuid"
)

type (
	EventService struct {
		repository      IRepository
		exerciseService IExerciseService
	}

	IRepository interface {
		IChallengeCategoryRepository
		ITeamRepository
		IChallengeRepository
		IJoinRepository
		IScoreRepository
		IParticipantRepository

		CreateEvent(ctx context.Context, arg postgres.CreateEventParams) error
		DeleteEvent(ctx context.Context, id uuid.UUID) error
		GetAllEvents(ctx context.Context) ([]postgres.Event, error)
		GetEventByID(ctx context.Context, id uuid.UUID) (postgres.Event, error)
		GetEventByTag(ctx context.Context, tag string) (postgres.Event, error)

		UpdateEvent(ctx context.Context, arg postgres.UpdateEventParams) error

		CountChallengesInEvents(ctx context.Context) ([]postgres.CountChallengesInEventsRow, error)
		CountTeamsInEvents(ctx context.Context) ([]postgres.CountTeamsInEventsRow, error)
	}

	Dependencies struct {
		Repository      IRepository
		ExerciseService IExerciseService
	}
)

func NewEventService(deps Dependencies) *EventService {
	return &EventService{
		repository:      deps.Repository,
		exerciseService: deps.ExerciseService,
	}
}

func (s *EventService) GetEvents(ctx context.Context) ([]*model.Event, error) {
	events, err := s.repository.GetAllEvents(ctx)
	if err != nil {
		return nil, err
	}

	challenges, err := s.repository.CountChallengesInEvents(ctx)
	if err != nil {
		return nil, err
	}

	teams, err := s.repository.CountTeamsInEvents(ctx)
	if err != nil {
		return nil, err
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrNotFound
		}
		return nil, err
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
		if errors.Is(err, sql.ErrNoRows) {
			return nil, model.ErrNotFound
		}
		return nil, err
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
	//TODO: check if event tag is unique

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
		return nil, err
	}
	return &event, nil
}

func (s *EventService) UpdateEvent(ctx context.Context, event model.Event) error {
	if err := s.repository.UpdateEvent(ctx, postgres.UpdateEventParams{
		ID:                     event.ID,
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
		return err
	}
	return nil
}

func (s *EventService) DeleteEvent(ctx context.Context, eventID uuid.UUID) error {
	if err := s.repository.DeleteEvent(ctx, eventID); err != nil {
		return err
	}
	return nil
}
