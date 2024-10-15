package event

import (
	"context"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
)

type (
	ITeamChallengeRepository interface {
		CreateEventTeamChallenge(ctx context.Context, arg []postgres.CreateEventTeamChallengeParams) *postgres.CreateEventTeamChallengeBatchResults
	}
)

func (s *EventService) CreateTeamChallenges(ctx context.Context, teamChallenges []model.TeamChallenge) error {
	var errs error

	teamChallengesParams := make([]postgres.CreateEventTeamChallengeParams, 0, len(teamChallenges))
	for _, teamChallenge := range teamChallenges {
		teamChallengesParams = append(teamChallengesParams, postgres.CreateEventTeamChallengeParams{
			ID:          uuid.Must(uuid.NewV7()),
			EventID:     teamChallenge.EventID,
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
				errs = multierror.Append(errs, errCreator.Cause())
				return
			}
			errs = multierror.Append(errs, model.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to create team challenge").Cause())
		}
	})

	if errs != nil {
		return model.ErrEventTeamChallenge.WithError(errs).WithMessage("Failed to create team challenges").Cause()
	}

	return nil
}
