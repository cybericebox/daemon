package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// teamStandMove is what moving one team's stand to a new exercise version
// touches: the Labs of that exercise and the team assignments waiting on them.
type teamStandMove struct {
	team           eventStandRepo.Team
	labs           []labBindingModel.Binding
	assignmentsIDs []uuid.UUID
}

// standRecreation is the plan of recreating the Labs of every prepared team
// of one exercise from its new version.
type standRecreation struct {
	eventID uuid.UUID
	now     time.Time
	moves   []teamStandMove
}

// planStandRecreation finds the live Labs of the exercise's prepared teams. A
// stage that is running (the event started and the set's stage is not
// upcoming) is not touched without recreateStands: the call fails with the
// affected teams and nothing is changed. A finished event keeps its Labs.
// It returns nil when there is nothing to move.
func (u *EventUseCase) planStandRecreation(ctx context.Context, link eventExerciseModel.EventExercise, version exerciseModel.ExerciseVersion, recreateStands bool, now time.Time) (*standRecreation, error) {
	if u.infra == nil || !exerciseModel.HasInfrastructure(version.Variants) {
		return nil, nil
	}
	e, err := u.events.GetByID(ctx, link.EventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if !e.InfrastructureAllowed {
		return nil, nil
	}
	if finish := e.Lifecycle.EffectiveFinishAt(); finish != nil && !now.Before(*finish) {
		return nil, nil
	}
	board, err := u.eventChallenges.List(ctx, link.ID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	ids := make([]uuid.UUID, 0, len(board))
	for _, challenge := range board {
		ids = append(ids, challenge.ID)
	}
	assignments, err := u.teamChallenges.ForRefresh(ctx, ids)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list team challenges").Err()
	}
	byTeam := make(map[uuid.UUID]*teamStandMove)
	order := make([]uuid.UUID, 0)
	for _, assignment := range assignments {
		binding, getErr := u.labBindings.Get(ctx, assignment.EventTeamID, assignment.EventChallengeID)
		if getErr != nil {
			if repositoryTools.IsObjectNotFoundError(getErr) {
				continue
			}
			return nil, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get lab binding").Err()
		}
		if binding.Readiness == labBindingModel.ReadinessDestroyed {
			continue
		}
		move, found := byTeam[assignment.EventTeamID]
		if !found {
			move = &teamStandMove{}
			byTeam[assignment.EventTeamID] = move
			order = append(order, assignment.EventTeamID)
		}
		move.labs = append(move.labs, binding)
		move.assignmentsIDs = append(move.assignmentsIDs, assignment.ID)
	}
	if len(byTeam) == 0 {
		return nil, nil
	}
	teams, err := u.stands.ListTeams(ctx, link.EventID, nil)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list stand teams").Err()
	}
	plan := &standRecreation{eventID: link.EventID, now: now}
	named := make([]map[string]string, 0, len(order))
	for _, teamID := range order {
		move := byTeam[teamID]
		for _, team := range teams {
			if team.TeamID == teamID {
				move.team = team
			}
		}
		move.team.TeamID = teamID
		plan.moves = append(plan.moves, *move)
		named = append(named, map[string]string{"ID": teamID.String(), "Name": move.team.PublicName})
	}
	stage, err := u.stageOfSet(ctx, u.stages, link)
	if err != nil {
		return nil, err
	}
	running := e.Lifecycle.RuntimeOpen(now) && !eventModel.StagePhaseAt(stage, now).Hidden()
	if running && !recreateStands {
		return nil, eventExerciseModel.ErrEventExerciseStandsRunning.WithPublicContext(eventExerciseModel.ContextTeams, named).Err()
	}
	return plan, nil
}

// rebind runs in the switch transaction: every Lab of the exercise moves to
// its next generation under a new name (the engine deploys it from the new
// version), the team assignments waiting on it return to preparing (a
// published one stays on the board; its access returns with the ready Lab) and the
// stand is creating again.
func (p *standRecreation) rebind(ctx context.Context, repo IRepository) error {
	if p == nil {
		return nil
	}
	bindings, assignments, stands := labBindingRepo.New(repo), teamChallengeRepo.New(repo), eventStandRepo.New(repo)
	for _, move := range p.moves {
		for _, id := range move.assignmentsIDs {
			if _, err := assignments.UpdateReadiness(ctx, id, teamChallengeModel.ReadinessReady, teamChallengeModel.ReadinessPreparing); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to reset team challenge").Err()
			}
		}
		generation := move.team.LabGeneration
		for _, lab := range move.labs {
			next := lab.Generation + 1
			if _, err := bindings.Recreate(ctx, lab, labBindingModel.NextLabName(lab.LabName, next), next); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to recreate team stand lab").Err()
			}
			generation = max(generation, next)
		}
		if move.team.HasStand && move.team.Status != eventStandModel.StatusRemoved {
			// A concurrent engine pass may have changed the status first; the next pass assesses the Labs as creating either way.
			if _, err := stands.UpdateStand(ctx, move.team.TeamID, move.team.Status, eventStandModel.StatusCreating, "", generation, p.now); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to save stand status").Err()
			}
		}
	}
	return nil
}

// finishStandRecreation runs after the switch committed: the replaced Labs are
// deleted in the agent (each shared Lab once) and the teams' lab access and
// failed-stand requests follow. The bindings already name the new Labs, so a
// failure here only leaves an old Lab to the cleanup sweeps and is logged.
func (u *EventUseCase) finishStandRecreation(ctx context.Context, plan *standRecreation, by uuid.UUID) {
	if plan == nil {
		return
	}
	deleter, canDelete := u.infra.(standLabDeleter)
	for _, move := range plan.moves {
		deleted := make(map[[2]string]struct{}, len(move.labs))
		for _, lab := range move.labs {
			key := [2]string{lab.LabGroupName, lab.LabName}
			if _, done := deleted[key]; done || !canDelete {
				continue
			}
			deleted[key] = struct{}{}
			if err := deleter.DeleteLab(ctx, lab.LabGroupName, lab.LabName); err != nil {
				log.Error().Err(err).Str("lab_group", lab.LabGroupName).Str("lab", lab.LabName).Msg("Failed to delete replaced stand lab")
			}
		}
		if u.standInbox != nil {
			if err := u.standInbox.StandRecreated(ctx, plan.eventID, move.team.TeamID, by); err != nil {
				log.Error().Err(err).Str("team_id", move.team.TeamID.String()).Msg("Failed to resolve stand failure requests")
			}
		}
		if u.supportsLabAccessPolicy() {
			if err := u.RequestLabAccessSync(ctx, move.team.TeamID); err != nil {
				log.Error().Err(err).Str("team_id", move.team.TeamID.String()).Msg("Failed to request lab access sync")
			}
		}
	}
}
