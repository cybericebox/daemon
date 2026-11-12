package event

import (
	"bytes"
	"context"
	"encoding/json"
	"maps"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRetentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// teamStandMove is what moving one team's stand to a new exercise version touches. Only a Lab whose content the new
// version changes is recreated; a Lab that never reached the agent is reset in place (nothing runs, so nothing is
// lost); a Lab the version does not change is kept as it is.
type teamStandMove struct {
	team eventStandRepo.Team
	// labs are the bindings of the Labs to recreate, assignmentsIDs the team assignments waiting on them.
	labs           []labBindingModel.Binding
	assignmentsIDs []uuid.UUID
	// unborn are the Labs that never reached the agent: their bindings go back to pending without a canonical Lab.
	unborn []unbornStandLab
	// kept counts the Labs the new version does not change.
	kept int
}

type unbornStandLab struct {
	labID    uuid.NullUUID
	bindings []labBindingModel.Binding
}

// standRecreation is the plan of moving the Labs of every prepared team of one exercise to its new version.
type standRecreation struct {
	eventID   uuid.UUID
	now       time.Time
	moves     []teamStandMove
	retainOld bool
}

// StandsToRecreate is how many Labs of how many teams the plan recreates.
func (p *standRecreation) standsToRecreate() (labs, teams int) {
	if p == nil {
		return 0, 0
	}
	for _, move := range p.moves {
		if n := distinctLabs(move.labs); n > 0 {
			labs += n
			teams++
		}
	}
	return labs, teams
}

func distinctLabs(bindings []labBindingModel.Binding) int {
	seen := map[string]struct{}{}
	for _, b := range bindings {
		seen[b.LabGroupName+"/"+b.LabName] = struct{}{}
	}
	return len(seen)
}

// planStandRecreation runs after the transaction owns team and canonical Lab locks (or, for a preview, with no
// locks at all). It finds the Labs of the exercise's prepared teams that the new version really changes: the
// topology of the team's variant or the flag links of its tasks. A solved generation never enters it. Labs that
// never reached the agent are reset in place without asking; recreating Labs that exist needs the organizer's
// confirmation (recreateStands), whenever it happens: the call fails with the affected teams and nothing is
// changed. A finished event keeps its Labs. It returns nil when there is nothing to move.
func (u *EventUseCase) planStandRecreation(ctx context.Context, repo IRepository, terminalLabs map[uuid.UUID]bool, link eventExerciseModel.EventExercise, version exerciseModel.ExerciseVersion, recreateStands bool, now time.Time) (*standRecreation, error) {
	plan, err := u.standRecreationPlan(ctx, repo, terminalLabs, link, version, now)
	if err != nil || plan == nil {
		return nil, err
	}
	if _, teams := plan.standsToRecreate(); teams > 0 && !recreateStands {
		return nil, eventExerciseModel.ErrEventExerciseStandsRunning.WithPublicContext(eventExerciseModel.ContextTeams, plan.affectedTeams()).Err()
	}
	return plan, nil
}

func (p *standRecreation) affectedTeams() []map[string]string {
	named := make([]map[string]string, 0, len(p.moves))
	for _, move := range p.moves {
		if len(move.labs) > 0 {
			named = append(named, map[string]string{"ID": move.team.TeamID.String(), "Name": move.team.PublicName})
		}
	}
	return named
}

// standRecreationPlan is planStandRecreation without the confirmation rule.
func (u *EventUseCase) standRecreationPlan(ctx context.Context, repo IRepository, terminalLabs map[uuid.UUID]bool, link eventExerciseModel.EventExercise, version exerciseModel.ExerciseVersion, now time.Time) (*standRecreation, error) {
	if u.infra == nil || !exerciseModel.HasInfrastructure(version.Variants) {
		return nil, nil
	}
	e, err := eventRepo.New(repo).GetByID(ctx, link.EventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if !e.InfrastructureAllowed {
		return nil, nil
	}
	if finish := e.Lifecycle.EffectiveFinishAt(); finish != nil && !now.Before(*finish) {
		return nil, nil
	}
	previous, err := exerciseRepo.New(repo).GetVersion(ctx, link.ExerciseVersionID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get pinned version").Err()
	}
	board, err := eventChallengeRepo.New(repo).List(ctx, link.ID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	ids := make([]uuid.UUID, 0, len(board))
	for _, challenge := range board {
		ids = append(ids, challenge.ID)
	}
	assignments, err := teamChallengeRepo.New(repo).ForRefresh(ctx, ids)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list team challenges").Err()
	}
	type labKey struct{ team, group, lab string }
	byTeam := make(map[uuid.UUID]*teamStandMove)
	order := make([]uuid.UUID, 0)
	decided := map[labKey]string{} // the verdict on a Lab, shared by the bindings of its tasks
	counted := map[labKey]bool{}
	unbornAt := map[labKey]int{}
	for _, assignment := range assignments {
		binding, getErr := labBindingRepo.New(repo).Get(ctx, assignment.EventTeamID, assignment.EventChallengeID)
		if getErr != nil {
			if repositoryTools.IsObjectNotFoundError(getErr) {
				continue
			}
			return nil, model.ErrPlatform.WithError(getErr).WithMessage("Failed to get lab binding").Err()
		}
		if binding.Readiness == labBindingModel.ReadinessDestroyed || (binding.LabID.Valid && terminalLabs[binding.LabID.UUID]) {
			continue
		}
		key := labKey{assignment.EventTeamID.String(), binding.LabGroupName, binding.LabName}
		verdict, known := decided[key]
		if !known {
			if verdict, err = u.standLabVerdict(ctx, repo, binding, previous, version, assignment.VariantIndex); err != nil {
				return nil, err
			}
			decided[key] = verdict
		}
		if verdict == "solved" {
			continue
		}
		move := byTeam[assignment.EventTeamID]
		if move == nil {
			move = &teamStandMove{}
			byTeam[assignment.EventTeamID] = move
			order = append(order, assignment.EventTeamID)
		}
		switch verdict {
		case "keep":
			if !counted[key] {
				counted[key] = true
				move.kept++
			}
		case "unborn":
			at, seen := unbornAt[key]
			if !seen {
				move.unborn = append(move.unborn, unbornStandLab{labID: binding.LabID})
				at = len(move.unborn) - 1
				unbornAt[key] = at
			}
			move.unborn[at].bindings = append(move.unborn[at].bindings, binding)
		default: // recreate
			move.labs = append(move.labs, binding)
			move.assignmentsIDs = append(move.assignmentsIDs, assignment.ID)
		}
	}
	if len(byTeam) == 0 {
		return nil, nil
	}
	teams, err := eventStandRepo.New(repo).ListTeams(ctx, link.EventID, nil, now.Add(-eventStandModel.WaitAlarmAfter))
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list stand teams").Err()
	}
	plan := &standRecreation{retainOld: u.lifecycleControls, eventID: link.EventID, now: now}
	for _, teamID := range order {
		move := byTeam[teamID]
		for _, team := range teams {
			if team.TeamID == teamID {
				move.team = team
			}
		}
		move.team.TeamID = teamID
		plan.moves = append(plan.moves, *move)
	}
	return plan, nil
}

// standLabVerdict decides what the new version means for one Lab of a team: "solved" (never touched), "unborn"
// (never reached the agent: reset in place), "keep" (the version changes nothing the Lab runs) or "recreate".
func (u *EventUseCase) standLabVerdict(ctx context.Context, repo IRepository, binding labBindingModel.Binding, previous, next exerciseModel.ExerciseVersion, variant int32) (string, error) {
	var canonical *eventLabModel.Lab
	if binding.LabID.Valid {
		l, err := eventLabRepo.New(repo).Get(ctx, binding.LabID.UUID)
		if err != nil {
			return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get canonical lab").Err()
		}
		if l.CloseReason == "solved" {
			return "solved", nil
		}
		canonical = &l
	}
	started := binding.Readiness == labBindingModel.ReadinessReady || binding.DeployedAt != nil || (canonical != nil && !canonical.Unborn())
	if !started {
		return "unborn", nil
	}
	if standContentChanged(previous, next, variant) {
		return "recreate", nil
	}
	return "keep", nil
}

// standContentChanged tells whether the new version changes what a team's Lab runs: the topology of the team's
// variant, or which tasks get a flag into which device (compared by task name, so re-issued task ids alone change
// nothing). A text, hint or attachment change does not touch the Lab.
func standContentChanged(previous, next exerciseModel.ExerciseVersion, variant int32) bool {
	if variant < 0 || int(variant) >= len(previous.Variants) || int(variant) >= len(next.Variants) {
		return true
	}
	before, after := previous.Variants[variant], next.Variants[variant]
	left, leftErr := json.Marshal(before.Topology)
	right, rightErr := json.Marshal(after.Topology)
	if leftErr != nil || rightErr != nil || !bytes.Equal(left, right) {
		return true
	}
	return !maps.Equal(taskKeys(before), taskKeys(after))
}

// taskKeys maps each task (by normalized name) to its key: a bootstrap places the value of a task by that key, so a
// changed key changes what the Lab is given. The bootstraps themselves are part of the topology compared above.
func taskKeys(variant exerciseModel.Variant) map[string]string {
	keys := map[string]string{}
	for _, task := range variant.Tasks {
		keys[normalizedTaskName(task.Name)] = task.Key
	}
	return keys
}

// rebind runs in the switch transaction. A Lab that never reached the agent is reset in place: its bindings forget
// the canonical Lab (the engine prepares a new one from the new version, same name and generation) and the empty
// Lab row goes. Every other Lab to recreate moves to its next free generation under a new name (the engine
// deploys it from the new version), the team assignments waiting on it return to preparing (a published one stays
// on the board; its access returns with the ready Lab) and the stand is creating again.
func (p *standRecreation) rebind(ctx context.Context, repo IRepository) error {
	if p == nil {
		return nil
	}
	bindings, assignments, stands, labs := labBindingRepo.New(repo), teamChallengeRepo.New(repo), eventStandRepo.New(repo), eventLabRepo.New(repo)
	for _, move := range p.moves {
		for _, item := range move.unborn {
			if !item.labID.Valid {
				continue
			}
			dropped, err := labs.DropUnborn(ctx, item.labID.UUID)
			if err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to reset an unborn stand lab").Err()
			}
			if !dropped {
				// Something still holds the empty Lab (an archive, a pin): it moves to a free generation instead.
				move.labs = append(move.labs, item.bindings...)
			}
		}
		for _, id := range move.assignmentsIDs {
			if _, err := assignments.UpdateReadiness(ctx, id, teamChallengeModel.ReadinessReady, teamChallengeModel.ReadinessPreparing); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to reset team challenge").Err()
			}
		}
		if p.retainOld {
			seen := map[uuid.UUID]bool{}
			for _, binding := range move.labs {
				if !binding.LabID.Valid || seen[binding.LabID.UUID] {
					continue
				}
				seen[binding.LabID.UUID] = true
				l, e := labs.Lock(ctx, binding.LabID.UUID)
				if e != nil {
					return e
				}
				if l.CloseReason == "solved" {
					continue
				}
				expected := l.Revision
				if l.DesiredState == "Running" {
					if e = l.Close("manual", uuid.Must(uuid.NewV7()), p.now); e != nil {
						return e
					}
					l.SetRetentionDeadline(p.now.Add(time.Duration(l.RetentionMinutes)*time.Minute), p.now)
					if _, e = labs.Update(ctx, l, expected); e != nil {
						return e
					}
				}
				if e = eventLabRetentionRepo.New(repo).Archive(ctx, l.ID); e != nil {
					return e
				}
			}
		}
		generation := move.team.LabGeneration
		for _, lab := range move.labs {
			next, err := labs.NextGeneration(ctx, lab.EventTeamID, lab.EventChallengeID, lab.Generation)
			if err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to find the next lab generation").Err()
			}
			if _, err = bindings.Recreate(ctx, lab, labBindingModel.NextLabName(lab.LabName, next), next); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to recreate team stand lab").Err()
			}
			generation = max(generation, next)
		}
		if len(move.labs) > 0 && move.team.HasStand && move.team.Status != eventStandModel.StatusRemoved {
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
			if u.lifecycleControls {
				continue
			}
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
