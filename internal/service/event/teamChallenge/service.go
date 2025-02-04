package teamChallengeService

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/hashicorp/go-multierror"
)

type (
	TeamChallengeService struct {
		repository IRepository
	}

	IRepository interface {
		CreateEventTeamChallenge(ctx context.Context, arg []postgres.CreateEventTeamChallengeParams) *postgres.CreateEventTeamChallengeBatchResults
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *TeamChallengeService {
	return &TeamChallengeService{
		repository: deps.Repository,
	}
}

func (s *TeamChallengeService) CreateEventTeamChallenges(ctx context.Context, teamChallenges []eventModel.TeamChallenge) error {
	var errs error

	teamChallengesParams := make([]postgres.CreateEventTeamChallengeParams, 0, len(teamChallenges))
	for _, teamChallenge := range teamChallenges {
		teamChallengesParams = append(teamChallengesParams, postgres.CreateEventTeamChallengeParams{
			TeamID:      teamChallenge.TeamID,
			ChallengeID: teamChallenge.ChallengeID,
			Flag:        teamChallenge.Flag,
		})
	}

	batchResult := s.repository.CreateEventTeamChallenge(ctx, teamChallengesParams)

	batchResult.Exec(func(i int, err error) {
		if err != nil {
			errCreator, has := tools.ForeignKeyViolationError(err)
			if has {
				errs = multierror.Append(errs, errCreator.Err())
				return
			}
			errs = multierror.Append(errs, eventModel.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to create team challenge").Err())
		}
	})

	if errs != nil {
		return eventModel.ErrEventTeamChallenge.WithError(errs).WithMessage("Failed to create team challenges").Err()
	}

	return nil
}
