package eventService

import (
	challengeService "github.com/cybericebox/daemon/internal/service/event/challenge"
	challengeCategoryService "github.com/cybericebox/daemon/internal/service/event/challengeCategory"
	challengeSolutionService "github.com/cybericebox/daemon/internal/service/event/challengeSolution"
	eventService "github.com/cybericebox/daemon/internal/service/event/event"
	participantService "github.com/cybericebox/daemon/internal/service/event/participant"
	scoreService "github.com/cybericebox/daemon/internal/service/event/score"
	teamService "github.com/cybericebox/daemon/internal/service/event/team"
	teamChallengeService "github.com/cybericebox/daemon/internal/service/event/teamChallenge"
)

type (
	EventService struct {
		*eventService.EventService
		*challengeService.ChallengeService
		*challengeCategoryService.ChallengeCategoryService
		*challengeSolutionService.ChallengeSolutionService
		*participantService.ParticipantService
		*scoreService.ScoreService
		*teamService.TeamService
		*teamChallengeService.TeamChallengeService
	}

	IRepository interface {
		eventService.IRepository
		challengeService.IRepository
		challengeCategoryService.IRepository
		challengeSolutionService.IRepository
		participantService.IRepository
		scoreService.IRepository
		teamService.IRepository
		teamChallengeService.IRepository
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *EventService {
	return &EventService{
		EventService:             eventService.NewService(eventService.Dependencies{Repository: deps.Repository}),
		ChallengeService:         challengeService.NewService(challengeService.Dependencies{Repository: deps.Repository}),
		ChallengeCategoryService: challengeCategoryService.NewService(challengeCategoryService.Dependencies{Repository: deps.Repository}),
		ChallengeSolutionService: challengeSolutionService.NewService(challengeSolutionService.Dependencies{Repository: deps.Repository}),
		ParticipantService:       participantService.NewService(participantService.Dependencies{Repository: deps.Repository}),
		ScoreService:             scoreService.NewService(scoreService.Dependencies{Repository: deps.Repository}),
		TeamService:              teamService.NewService(teamService.Dependencies{Repository: deps.Repository}),
		TeamChallengeService:     teamChallengeService.NewService(teamChallengeService.Dependencies{Repository: deps.Repository}),
	}
}
