package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"sort"
	"time"
)

type (
	IScoreRepository interface {
		GetChallengesSolutionsInEvent(ctx context.Context, eventID uuid.UUID) ([]postgres.GetChallengesSolutionsInEventRow, error)
	}
)

func (s *EventService) GetScore(ctx context.Context, eventID uuid.UUID) (*model.EventScore, error) {
	event, err := s.repository.GetEventByID(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventScore.WithError(err).WithMessage("Failed to get event by id from repository").Cause()
	}

	teams, err := s.repository.GetEventTeams(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventScore.WithError(err).WithMessage("Failed to get teams from repository").Cause()
	}

	challenges, err := s.GetEventChallenges(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventScore.WithError(err).WithMessage("Failed to get challenges from repository").Cause()
	}

	solutionsByChallenges, err := s.getSolutionsByChallenges(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventScore.WithError(err).WithMessage("Failed to get solutions by challenges").Cause()
	}

	challengePoints := make(map[uuid.UUID]int32)
	if !event.DynamicScoring {
		for _, challenge := range challenges {
			challengePoints[challenge.ID] = challenge.Data.Points
		}
	}
	var teamScores []model.TeamScore
	for _, team := range teams {
		teamSolutions := make(map[uuid.UUID]model.TeamSolution)

		var solvesForTimeline []model.SolutionForTimeline
		score := 0
	GlobalLoop:
		for challengeID, solutions := range solutionsByChallenges {
			challengeSolutionCount := len(solutions)
			for index, solution := range solutions {
				if solution.TeamID == team.ID {
					teamSolutions[challengeID] = model.TeamSolution{
						ID:   solution.ChallengeID,
						Rank: index + 1,
					}
					points := challengePoints[challengeID]
					if event.DynamicScoring {
						points = tools.CalculateScore(event.DynamicMin, event.DynamicMax, event.DynamicSolveThreshold, float64(challengeSolutionCount))
					}
					score += int(points)
					solvesForTimeline = append(solvesForTimeline, model.SolutionForTimeline{
						Date:   solution.Timestamp,
						Points: int(points),
					})

					continue GlobalLoop
				}
			}
		}

		teamScoreTimeline := convertToScoreTimeline(solvesForTimeline, event.StartTime)

		latestSolution := teamScoreTimeline[len(teamScoreTimeline)-1][0].(time.Time)

		teamScores = append(teamScores, model.TeamScore{
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

	return &model.EventScore{
		TeamsScores: teamScores,
		Challenges:  convertToChallengeList(challenges),
	}, nil
}

func (s *EventService) getSolutionsByChallenges(ctx context.Context, eventID uuid.UUID) (map[uuid.UUID][]postgres.GetChallengesSolutionsInEventRow, error) {
	solutions, err := s.repository.GetChallengesSolutionsInEvent(ctx, eventID)
	if err != nil {
		return nil, model.ErrEventScore.WithError(err).WithMessage("Failed to get all challenges solutions in event").Cause()
	}

	result := make(map[uuid.UUID][]postgres.GetChallengesSolutionsInEventRow)
	for _, solution := range solutions {
		result[solution.ChallengeID] = append(result[solution.ChallengeID], solution)
	}

	return result, nil
}

func convertToScoreTimeline(solvesForTimeline []model.SolutionForTimeline, startTime time.Time) [][]interface{} {
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

func sortTeamScores(teamsScore []model.TeamScore) {
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

func sortTimeline(solvesForTimeline []model.SolutionForTimeline) {
	sort.SliceStable(solvesForTimeline, func(p, q int) bool {
		return solvesForTimeline[p].Date.Before(solvesForTimeline[q].Date)
	})
}

func convertToChallengeList(challenges []*model.Challenge) []model.ChallengeInfo {
	var result []model.ChallengeInfo
	for _, challenge := range challenges {
		result = append(result, model.ChallengeInfo{
			ID:   challenge.ID,
			Name: challenge.Data.Name,
		})
	}
	return result
}
