package scoreService

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"sort"
	"time"
)

type (
	ScoreService struct {
		repository IRepository
	}

	IRepository interface {
		GetChallengesSolutionsInEvent(ctx context.Context, arg postgres.GetChallengesSolutionsInEventParams) ([]postgres.GetChallengesSolutionsInEventRow, error)

		GetEventByID(ctx context.Context, id uuid.UUID) (postgres.Event, error)
		GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]postgres.GetEventTeamsRow, error)
		GetEventChallenges(ctx context.Context, eventID uuid.UUID) ([]postgres.EventChallenge, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *ScoreService {
	return &ScoreService{
		repository: deps.Repository,
	}
}

func (s *ScoreService) GetEventScore(ctx context.Context, eventID uuid.UUID, fromTime, toTime time.Time) (*eventModel.EventScore, error) {
	event, err := s.repository.GetEventByID(ctx, eventID)
	if err != nil {
		return nil, eventModel.ErrEventScore.WithError(err).WithMessage("Failed to get event by id from repository").Err()
	}

	teams, err := s.repository.GetEventTeams(ctx, eventID)
	if err != nil {
		return nil, eventModel.ErrEventScore.WithError(err).WithMessage("Failed to get teams from repository").Err()
	}

	challenges, err := s.repository.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, eventModel.ErrEventScore.WithError(err).WithMessage("Failed to get challenges from repository").Err()
	}

	solutionsByChallenges, err := s.getSolutionsByChallenges(ctx, eventID, fromTime, toTime)
	if err != nil {
		return nil, eventModel.ErrEventScore.WithError(err).WithMessage("Failed to get solutions by challenges").Err()
	}

	challengePoints := make(map[uuid.UUID]int32)
	if !event.DynamicScoring {
		for _, challenge := range challenges {
			challengePoints[challenge.ID] = challenge.Data.Points
		}
	}
	teamScores := make([]eventModel.TeamScore, 0)
	for _, team := range teams {
		if team.Hidden {
			continue
		}
		teamSolutions := make(map[uuid.UUID]eventModel.TeamSolution)

		var solvesForTimeline []eventModel.SolutionForTimeline
		score := 0
	GlobalLoop:
		for challengeID, solutions := range solutionsByChallenges {
			challengeSolutionCount := len(solutions)
			for index, solution := range solutions {
				if solution.TeamID == team.ID {
					teamSolutions[challengeID] = eventModel.TeamSolution{
						ID:   solution.ChallengeID,
						Rank: index + 1,
					}
					points := challengePoints[challengeID]
					if event.DynamicScoring {
						points = tools.CalculateScore(event.DynamicMin, event.DynamicMax, event.DynamicSolveThreshold, float64(challengeSolutionCount))
					}
					score += int(points)
					solvesForTimeline = append(solvesForTimeline, eventModel.SolutionForTimeline{
						Date:   solution.Timestamp,
						Points: int(points),
					})

					continue GlobalLoop
				}
			}
		}

		teamScoreTimeline := convertToScoreTimeline(solvesForTimeline, event.StartTime)

		latestSolution := teamScoreTimeline[len(teamScoreTimeline)-1][0].(time.Time)

		teamScores = append(teamScores, eventModel.TeamScore{
			TeamID:            team.ID,
			TeamName:          team.Name,
			Score:             score,
			TeamSolutions:     teamSolutions,
			LatestSolution:    latestSolution,
			TeamScoreTimeline: teamScoreTimeline,
		})
	}
	sortTeamScores(teamScores)

	//Inserting their rank
	for i := range teamScores {
		teamScores[i].Rank = i + 1
	}

	return &eventModel.EventScore{
		TeamsScores: teamScores,
		Challenges:  convertToChallengeList(challenges),
	}, nil
}

func (s *ScoreService) getSolutionsByChallenges(ctx context.Context, eventID uuid.UUID, fromTime, toTime time.Time) (map[uuid.UUID][]postgres.GetChallengesSolutionsInEventRow, error) {
	solutions, err := s.repository.GetChallengesSolutionsInEvent(ctx, postgres.GetChallengesSolutionsInEventParams{
		EventID:  eventID,
		FromTime: fromTime,
		ToTime:   toTime,
	})
	if err != nil {
		return nil, eventModel.ErrEventScore.WithError(err).WithMessage("Failed to get all challenges solutions in event").Err()
	}

	result := make(map[uuid.UUID][]postgres.GetChallengesSolutionsInEventRow)
	for _, solution := range solutions {
		result[solution.ChallengeID] = append(result[solution.ChallengeID], solution)
	}

	return result, nil
}

func convertToScoreTimeline(solvesForTimeline []eventModel.SolutionForTimeline, startTime time.Time) [][]interface{} {
	var teamScoreTimeline [][]interface{}
	sortTimeline(solvesForTimeline)
	scoreForTimeline := 0
	var teamScoreTime []interface{}
	teamScoreTime = append(teamScoreTime, startTime)
	teamScoreTime = append(teamScoreTime, 0)
	teamScoreTimeline = append(teamScoreTimeline, teamScoreTime)
	for _, solveForTimeLine := range solvesForTimeline {
		teamScoreTime = []interface{}{}
		teamScoreTime = append(teamScoreTime, solveForTimeLine.Date)
		scoreForTimeline += solveForTimeLine.Points
		teamScoreTime = append(teamScoreTime, scoreForTimeline)
		teamScoreTimeline = append(teamScoreTimeline, teamScoreTime)
	}
	return teamScoreTimeline
}

func sortTeamScores(teamsScore []eventModel.TeamScore) {
	sort.SliceStable(teamsScore, func(p, q int) bool {
		return teamsScore[p].Score > teamsScore[q].Score
	})

	sort.SliceStable(teamsScore, func(p, q int) bool {
		if teamsScore[p].Score == teamsScore[q].Score {

			return teamsScore[p].LatestSolution.Before(teamsScore[q].LatestSolution)
		}
		return false
	})
}

func sortTimeline(solvesForTimeline []eventModel.SolutionForTimeline) {
	sort.SliceStable(solvesForTimeline, func(p, q int) bool {
		return solvesForTimeline[p].Date.Before(solvesForTimeline[q].Date)
	})
}

func convertToChallengeList(challenges []postgres.EventChallenge) []eventModel.ChallengeInfo {
	result := make([]eventModel.ChallengeInfo, 0, len(challenges))
	for _, challenge := range challenges {
		result = append(result, eventModel.ChallengeInfo{
			ID:   challenge.ID,
			Name: challenge.Data.Name,
		})
	}
	return result
}
