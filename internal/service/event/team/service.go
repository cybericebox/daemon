package teamService

import (
	"context"
	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"github.com/hashicorp/go-multierror"
	"strings"
)

type (
	TeamService struct {
		repository IRepository
	}

	IRepository interface {
		CreateTeamInEvent(ctx context.Context, arg postgres.CreateTeamInEventParams) error

		GetEventTeams(ctx context.Context, eventID uuid.UUID) ([]postgres.GetEventTeamsRow, error)
		GetEventTeamsPaged(ctx context.Context, arg postgres.GetEventTeamsPagedParams) ([]postgres.GetEventTeamsPagedRow, error)

		GetEventTeamByName(ctx context.Context, arg postgres.GetEventTeamByNameParams) (postgres.GetEventTeamByNameRow, error)
		GetEventTeamByID(ctx context.Context, teamID uuid.UUID) (postgres.GetEventTeamByIDRow, error)

		UpdateEventTeamsLaboratories(ctx context.Context, arg []postgres.UpdateEventTeamsLaboratoriesParams) *postgres.UpdateEventTeamsLaboratoriesBatchResults
		UpdateEventTeamName(ctx context.Context, arg postgres.UpdateEventTeamNameParams) (int64, error)
		DeleteEventTeam(ctx context.Context, teamID uuid.UUID) (int64, error)

		UpdateEventParticipantTeam(ctx context.Context, arg postgres.UpdateEventParticipantTeamParams) (int64, error)
	}

	Dependencies struct {
		Repository IRepository
	}
)

func NewService(deps Dependencies) *TeamService {
	return &TeamService{
		repository: deps.Repository,
	}
}

func (s *TeamService) GetEventTeams(ctx context.Context, eventID uuid.UUID, page int) ([]*eventModel.Team, error) {
	if page == config.AllPages {
		teams, err := s.repository.GetEventTeams(ctx, eventID)
		if err != nil {
			return nil, eventModel.ErrEventTeam.WithError(err).WithMessage("Failed to get teams from repository").Err()
		}

		result := make([]*eventModel.Team, 0, len(teams))
		for _, team := range teams {
			result = append(result, &eventModel.Team{
				ID:                team.ID,
				EventID:           team.EventID,
				Name:              team.Name,
				JoinCode:          "",
				Hidden:            team.Hidden,
				ParticipantsCount: team.ParticipantsCount,
				ApprovalStatus:    team.ApprovalStatus,
				LaboratoryID:      team.LaboratoryID,
				UpdatedAt:         team.UpdatedAt.Time,
				UpdatedBy:         team.UpdatedBy,
				CreatedAt:         team.CreatedAt,
			})
		}

		return result, nil
	}

	// one page
	teams, err := s.repository.GetEventTeamsPaged(ctx, postgres.GetEventTeamsPagedParams{
		EventID: eventID,
		Limit:   config.DefaultOnePageLimit,
		Offset:  int32(page * config.DefaultOnePageLimit),
	})
	if err != nil {
		return nil, eventModel.ErrEventTeam.WithError(err).WithMessage("Failed to get teams from repository").Err()
	}

	result := make([]*eventModel.Team, 0, len(teams))
	for _, team := range teams {
		result = append(result, &eventModel.Team{
			ID:                team.ID,
			EventID:           team.EventID,
			Name:              team.Name,
			JoinCode:          "",
			Hidden:            team.Hidden,
			ApprovalStatus:    team.ApprovalStatus,
			ParticipantsCount: team.ParticipantsCount,
			LaboratoryID:      team.LaboratoryID,
			UpdatedAt:         team.UpdatedAt.Time,
			UpdatedBy:         team.UpdatedBy,
			CreatedAt:         team.CreatedAt,
		})
	}

	return result, nil
}

func (s *TeamService) GetEventTeam(ctx context.Context, teamID uuid.UUID) (*eventModel.Team, error) {
	team, err := s.repository.GetEventTeamByID(ctx, teamID)
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventTeamTeamNotFound.WithContext("teamID", teamID).Err()
		}
		return nil, eventModel.ErrEventTeam.WithError(err).WithMessage("Failed to get team by id").Err()
	}

	return &eventModel.Team{
		ID:             team.ID,
		EventID:        team.EventID,
		Name:           team.Name,
		LaboratoryID:   team.LaboratoryID,
		Hidden:         team.Hidden,
		ApprovalStatus: team.ApprovalStatus,
		UpdatedAt:      team.UpdatedAt.Time,
		UpdatedBy:      team.UpdatedBy,
		CreatedAt:      team.CreatedAt,
	}, nil
}

func (s *TeamService) CreateEventTeam(ctx context.Context, team eventModel.Team) (*uuid.UUID, error) {
	if team.ID.IsNil() {
		team.ID = uuid.Must(uuid.NewV7())
	}

	team.JoinCode = uuid.Must(uuid.NewV4()).String()

	// create team
	if err := s.repository.CreateTeamInEvent(ctx, postgres.CreateTeamInEventParams{
		ID:           team.ID,
		EventID:      team.EventID,
		Name:         team.Name,
		JoinCode:     team.JoinCode,
		LaboratoryID: team.LaboratoryID,
		Hidden:       team.Hidden,
	}); err != nil {
		errCreator, has := tools.UniqueViolationError(err, eventModel.ErrEventTeamTeamExists)
		if has {
			return nil, errCreator.Err()
		}

		errCreator, has = tools.ForeignKeyViolationError(err)
		if has {
			return nil, errCreator.Err()
		}
		return nil, eventModel.ErrEventTeam.WithError(err).WithMessage("Failed to create team").Err()
	}

	return &team.ID, nil
}

func (s *TeamService) UpdateEventTeamName(ctx context.Context, teamID uuid.UUID, name string) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	affected, err := s.repository.UpdateEventTeamName(ctx, postgres.UpdateEventTeamNameParams{
		ID:   teamID,
		Name: name,
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		errCreator, has := tools.UniqueViolationError(err, eventModel.ErrEventTeamTeamExists)
		if has {
			return errCreator.Err()
		}

		errCreator, has = tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return eventModel.ErrEventTeam.WithError(err).WithMessage("Failed to update team name").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventTeamTeamNotFound.WithContext("teamID", teamID).Err()
	}

	return nil
}

func (s *TeamService) UpdateEventTeamsLaboratories(ctx context.Context, teamsLabs map[uuid.UUID]uuid.UUID) error {

	currentUserID, _ := tools.GetCurrentUserIDFromContext(ctx)

	teamsLabsParams := make([]postgres.UpdateEventTeamsLaboratoriesParams, 0, len(teamsLabs))

	for teamID, labID := range teamsLabs {
		teamsLabsParams = append(teamsLabsParams, postgres.UpdateEventTeamsLaboratoriesParams{
			ID: teamID,
			LaboratoryID: uuid.NullUUID{
				UUID:  labID,
				Valid: !labID.IsNil(),
			},
			UpdatedBy: uuid.NullUUID{
				UUID:  currentUserID,
				Valid: !currentUserID.IsNil(),
			},
		})
	}

	var errs error
	batchResult := s.repository.UpdateEventTeamsLaboratories(ctx, teamsLabsParams)

	batchResult.Exec(func(i int, affected int64, err error) {
		if err != nil {
			errs = multierror.Append(errs, eventModel.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to update team laboratory").Err())
		}
	})

	if errs != nil {
		return eventModel.ErrEventTeamChallenge.WithError(errs).WithMessage("Failed to update teams laboratories").Err()
	}

	return nil
}

func (s *TeamService) DeleteEventTeam(ctx context.Context, teamID uuid.UUID) error {
	affected, err := s.repository.DeleteEventTeam(ctx, teamID)
	if err != nil {
		return eventModel.ErrEventTeam.WithError(err).WithMessage("Failed to delete team").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventTeamTeamNotFound.WithContext("teamID", teamID).Err()
	}

	return nil
}

func (s *TeamService) CheckEventTeamCredentials(ctx context.Context, t eventModel.Team) error {
	team, err := s.repository.GetEventTeamByName(ctx, postgres.GetEventTeamByNameParams{
		EventID: t.EventID,
		Name:    t.Name,
	})
	if err != nil {
		if tools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventTeamWrongCredentials.Err()
		}
		return eventModel.ErrEventTeamChallenge.WithError(err).WithMessage("Failed to get team by name").Err()
	}

	if strings.Compare(team.JoinCode, t.JoinCode) != 0 {
		return eventModel.ErrEventTeamWrongCredentials.Err()
	}

	return nil
}

func (s *TeamService) AssignEventTeam(ctx context.Context, eventID, userID, teamID uuid.UUID) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	// update participant team
	affected, err := s.repository.UpdateEventParticipantTeam(ctx, postgres.UpdateEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
		TeamID: uuid.NullUUID{
			UUID:  teamID,
			Valid: true,
		},
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		// if team not found
		errCreator, has := tools.ForeignKeyViolationError(err)
		if has {
			return errCreator.Err()
		}
		return eventModel.ErrEventTeam.WithError(err).WithMessage("Failed to update participant team").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventParticipantNotFound.WithContext("userID", userID).Err()
	}

	return nil
}

func (s *TeamService) UnassignEventTeam(ctx context.Context, eventID, userID uuid.UUID) error {
	currentUserID, err := tools.GetCurrentUserIDFromContext(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get current user id from context").Err()
	}

	affected, err := s.repository.UpdateEventParticipantTeam(ctx, postgres.UpdateEventParticipantTeamParams{
		EventID: eventID,
		UserID:  userID,
		TeamID: uuid.NullUUID{
			UUID:  uuid.Nil,
			Valid: false,
		},
		UpdatedBy: uuid.NullUUID{
			UUID:  currentUserID,
			Valid: true,
		},
	})
	if err != nil {
		return eventModel.ErrEventTeam.WithError(err).WithMessage("Failed to update participant team").Err()
	}

	if affected == 0 {
		return eventModel.ErrEventParticipantNotFound.WithContext("userID", userID).Err()
	}

	return nil
}
