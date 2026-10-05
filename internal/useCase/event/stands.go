package event

import (
	"context"
	"crypto/rand"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventChallengeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventExerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/teamChallengeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/flagpattern"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// DefaultStandDeployBudget bounds agent deploy calls per event and pass (a pass
// runs every 10 seconds), so one large event cannot monopolize a pass; the
// remaining Labs deploy on the next one. It only protects the agent API from a
// burst: the launch pacing is the Laboratory operator's queue, so this is high.
const DefaultStandDeployBudget = 200

// standLabDeleter is the optional agent capability a recreate needs. It is
// separate from Infrastructure so narrow test doubles stay unaffected.
type standLabDeleter interface {
	DeleteLab(ctx context.Context, group, lab string) error
}

// ReconcileEventStands is the schedule-driven stand engine (River periodic
// job). Every pass re-derives the complete desired state from the database:
// the schedule follows lifecycle and setting changes without rescheduling,
// every write is conditional, and a restart loses nothing. One failing event
// does not stop the others.
func (u *EventUseCase) ReconcileEventStands(ctx context.Context) error {
	now := time.Now()
	u.prewarmEventImages(ctx, now)
	eventIDs, err := u.stands.ListEvents(ctx, now)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list stand events").Err()
	}
	var errs []error
	for _, eventID := range eventIDs {
		if err = u.reconcileEventStands(ctx, eventID, now); err != nil {
			log.Error().Err(err).Str("event_id", eventID.String()).Msg("Stand reconcile failed")
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (u *EventUseCase) reconcileEventStands(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get stand event").Err()
	}
	config, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get stand timing").Err()
	}
	rollout, err := u.standRollout(ctx, eventID)
	if err != nil {
		return err
	}
	// The deploy lead is computed from the workload (the stand window only brackets it): nothing happens before
	// the first labs are due. The sets of a later stage wait for their own lead (schedule.NotDue).
	schedule := u.standSchedule(ctx, e, now)
	if now.Before(schedule.InitialDeployAt) {
		return nil
	}
	if teardownAt := config.StandTiming.TeardownAt(e.Lifecycle.EffectiveFinishAt()); teardownAt != nil && !now.Before(*teardownAt) {
		return u.tearDownEventStands(ctx, e, now)
	}
	// Every event in the window gets the hidden moderators team (the
	// moderators board); only infrastructure events sync its access.
	if err = u.ensureModeratorsTeam(ctx, eventID, now, e.InfrastructureAllowed); err != nil {
		return err
	}
	var errs []error
	if e.InfrastructureAllowed && u.laboratoriesUsable(ctx) {
		if err = u.moveLegacyStandLabs(ctx, e); err != nil {
			errs = append(errs, err)
		}
	}
	if err = u.prepareMissingAssignments(ctx, e, now); err != nil {
		errs = append(errs, err)
	}
	readyTeams := map[uuid.UUID]struct{}{}
	allReady := false
	if e.InfrastructureAllowed {
		if u.laboratoriesUsable(ctx) {
			if readyTeams, err = u.deployAndObserveStandLabs(ctx, e, now, schedule.NotDue); err != nil {
				errs = append(errs, err)
			}
		}
		if allReady, err = u.assessStands(ctx, e, now, schedule.NotDue); err != nil {
			errs = append(errs, err)
		}
	}
	open, openNow := eventStandModel.InfrastructureOpen(e.Lifecycle.HasStarted(now), rollout.OpenedAt, allReady)
	if openNow {
		if err = u.stands.OpenRollout(ctx, eventID, now); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to open infrastructure challenges").Err()
		}
	}
	published, err := u.teamChallenges.PublishAvailable(ctx, eventID, open && e.InfrastructureAllowed)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to publish available team challenges").Err()
	}
	for _, teamID := range published {
		readyTeams[teamID] = struct{}{}
	}
	if u.supportsLabAccessPolicy() {
		for teamID := range readyTeams {
			if err = u.RequestLabAccessSync(ctx, teamID); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (u *EventUseCase) standRollout(ctx context.Context, eventID uuid.UUID) (eventStandRepo.Rollout, error) {
	rollout, err := u.stands.GetRollout(ctx, eventID)
	if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
		return eventStandRepo.Rollout{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get stand rollout").Err()
	}
	return rollout, nil
}

// laboratoriesConfigured is false only when the capability says no agent exists at all; periodic
// jobs then do nothing instead of failing every tick.
func (u *EventUseCase) laboratoriesConfigured() bool {
	if reporter, ok := u.infrastructureCapability.(interface{ LaboratoriesConfigured() bool }); ok {
		return reporter.LaboratoriesConfigured()
	}
	return true
}

func (u *EventUseCase) laboratoriesUsable(ctx context.Context) bool {
	if u.infra == nil {
		return false
	}
	return u.infrastructureCapability == nil || u.infrastructureCapability.RequireLaboratories(ctx) == nil
}

// ensureModeratorsTeam creates the hidden moderators team once. On an
// infrastructure event (syncAccess) its ACL is requested immediately so the
// managers' VPN clients follow the first sync; otherwise it has no VPN/Labs.
func (u *EventUseCase) ensureModeratorsTeam(ctx context.Context, eventID uuid.UUID, now time.Time, syncAccess bool) error {
	if _, err := u.stands.GetModeratorsTeam(ctx, eventID); err == nil {
		return nil
	} else if !repositoryTools.IsObjectNotFoundError(err) {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get moderators team").Err()
	}
	if err := u.stands.EnsureModeratorsTeam(ctx, eventID, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV4()).String(), now); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to create moderators team").Err()
	}
	team, err := u.stands.GetModeratorsTeam(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			// No owner yet: nothing to create the team for.
			return nil
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get moderators team").Err()
	}
	if syncAccess && u.supportsLabAccessPolicy() {
		return u.RequestLabAccessSync(ctx, team.ID)
	}
	return nil
}

func (u *EventUseCase) prepareMissingAssignments(ctx context.Context, e eventModel.Event, now time.Time) error {
	assignments, err := u.stands.ListMissingAssignments(ctx, e.ID, e.InfrastructureAllowed)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list missing team assignments").Err()
	}
	var errs []error
	for _, assignment := range assignments {
		if err = u.prepareTeamAssignment(ctx, e, assignment, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// prepareTeamAssignment pins one team's variant of one active exercise and
// classifies every challenge in the same transaction: a static challenge is
// ready at once; an infrastructure one gets its pending Lab binding (only
// when the event allows infrastructure; otherwise it is never shown). The
// tasks of the exercise attach to one Lab: every binding carries the same Lab
// name, derived from the event exercise and the variant.
func (u *EventUseCase) prepareTeamAssignment(ctx context.Context, e eventModel.Event, assignment eventStandRepo.Assignment, now time.Time) error {
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	team, err := eventStandRepo.New(txRepo).GetTeam(txCtx, e.ID, assignment.TeamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get stand team").Err()
	}
	attachment, err := eventExerciseRepo.New(txRepo).GetByID(txCtx, e.ID, assignment.EventExerciseID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event exercise").Err()
	}
	if attachment.Status != eventExerciseModel.StatusActive {
		return nil
	}
	version, err := exerciseRepo.New(txRepo).GetVersion(txCtx, attachment.ExerciseVersionID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get pinned exercise version").Err()
	}
	variantIndex, err := selectTeamVariant(team.ID, attachment, version)
	if err != nil {
		return err
	}
	variant := version.Variants[variantIndex]
	infrastructure := len(variant.Topology.Devices) > 0
	challenges, err := eventChallengeRepo.New(txRepo).List(txCtx, attachment.ID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	tasks := make(map[uuid.UUID]exerciseModel.Task, len(variant.Tasks))
	for _, task := range variant.Tasks {
		tasks[task.ID] = task
	}
	teamChallenges := teamChallengeRepo.New(txRepo)
	bindings := labBindingRepo.New(txRepo)
	for _, challenge := range challenges {
		value, getErr := teamChallenges.Get(txCtx, team.ID, challenge.ID)
		if getErr != nil {
			if !repositoryTools.IsObjectNotFoundError(getErr) {
				return model.ErrPlatform.WithError(getErr).WithMessage("Failed to get team challenge").Err()
			}
			if value, err = u.newTeamChallenge(txCtx, teamChallenges, e.ID, team.ID, challenge, tasks, variantIndex, now); err != nil {
				return err
			}
		}
		if value.Readiness != teamChallengeModel.ReadinessPreparing {
			continue
		}
		if !infrastructure {
			if _, err = teamChallenges.UpdateReadiness(txCtx, value.ID, teamChallengeModel.ReadinessPreparing, teamChallengeModel.ReadinessReady); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to mark static team challenge ready").Err()
			}
			continue
		}
		if !e.InfrastructureAllowed {
			continue
		}
		if _, getErr = bindings.Get(txCtx, team.ID, challenge.ID); getErr == nil {
			continue
		} else if !repositoryTools.IsObjectNotFoundError(getErr) {
			return model.ErrPlatform.WithError(getErr).WithMessage("Failed to get lab binding").Err()
		}
		group, lab, nameErr := labBindingModel.Names(e.ID, team.ID, attachment.ID, variantIndex)
		if nameErr != nil {
			return nameErr
		}
		binding, newErr := labBindingModel.New(e.ID, team.ID, challenge.ID, group, lab, now)
		if newErr != nil {
			return newErr
		}
		if _, _, err = bindings.Create(txCtx, binding); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to create lab binding").Err()
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to prepare team challenges").Err()
	}
	return nil
}

// newTeamChallenge makes the durable, team-specific copy of one task: the
// snapshot and the resolved flag are pinned once and never recomputed.
func (u *EventUseCase) newTeamChallenge(ctx context.Context, repo *teamChallengeRepo.Repository, eventID, teamID uuid.UUID, challenge eventChallengeModel.EventChallenge, tasks map[uuid.UUID]exerciseModel.Task, variantIndex int32, now time.Time) (teamChallengeModel.TeamChallenge, error) {
	task, found := tasks[challenge.TaskID]
	if !found {
		return teamChallengeModel.TeamChallenge{}, model.ErrPlatform.WithMessage("Pinned exercise version has no task for event challenge").Err()
	}
	snapshot, err := eventChallengeModel.SnapshotForTask(task)
	if err != nil {
		return teamChallengeModel.TeamChallenge{}, model.ErrPlatform.WithError(err).WithMessage("Failed to snapshot team challenge").Err()
	}
	expectedFlag, err := flagpattern.Resolve(task.Flag, u.flagRandomBytes, rand.Reader)
	if err != nil {
		return teamChallengeModel.TeamChallenge{}, model.ErrPlatform.WithError(err).WithMessage("Failed to resolve team challenge flag").Err()
	}
	value, err := teamChallengeModel.New(eventID, teamID, challenge.ID, variantIndex, snapshot, expectedFlag, now)
	if err != nil {
		return teamChallengeModel.TeamChallenge{}, err
	}
	value.Hints = teamHints(task)
	if _, err = repo.Create(ctx, value); err != nil {
		return teamChallengeModel.TeamChallenge{}, model.ErrPlatform.WithError(err).WithMessage("Failed to materialize team challenge").Err()
	}
	return value, nil
}

// moveLegacyStandLabs retires the per-task Labs (c-<challenge>) a stand had before the tasks of an exercise
// shared one Lab: the old Labs of a team are deleted and its bindings of that exercise move, together, to one
// next-generation shared Lab, which the engine then deploys with every task's flag. A pass that fails halfway
// resumes on the next one.
func (u *EventUseCase) moveLegacyStandLabs(ctx context.Context, e eventModel.Event) error {
	legacy, err := u.labBindings.ListLegacy(ctx, e.ID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list per-task stand labs").Err()
	}
	if len(legacy) == 0 {
		return nil
	}
	deleter, ok := u.infra.(standLabDeleter)
	if !ok {
		return nil
	}
	type setKey struct{ team, exercise uuid.UUID }
	sets := map[setKey][]labBindingRepo.LegacyBinding{}
	var order []setKey
	for _, item := range legacy {
		key := setKey{item.Binding.EventTeamID, item.EventExerciseID}
		if _, seen := sets[key]; !seen {
			order = append(order, key)
		}
		sets[key] = append(sets[key], item)
	}
	var errs []error
	for _, key := range order {
		if err = u.moveLegacySet(ctx, deleter, sets[key]); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (u *EventUseCase) moveLegacySet(ctx context.Context, deleter standLabDeleter, items []labBindingRepo.LegacyBinding) error {
	var generation int32
	for _, item := range items {
		generation = max(generation, item.Binding.Generation)
	}
	generation++
	name := labBindingModel.LabName(items[0].EventExerciseID, items[0].VariantIndex, generation)
	deleted := map[string]struct{}{}
	for _, item := range items {
		if _, done := deleted[item.Binding.LabName]; done {
			continue
		}
		deleted[item.Binding.LabName] = struct{}{}
		if err := deleter.DeleteLab(ctx, item.Binding.LabGroupName, item.Binding.LabName); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to delete per-task stand lab").Err()
		}
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	bindings := labBindingRepo.New(txRepo)
	for _, item := range items {
		if _, err = bindings.Recreate(txCtx, item.Binding, name, generation); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to move stand lab binding").Err()
		}
	}
	if err = bindings.ResetUnpublishedForRecreate(txCtx, items[0].Binding.EventTeamID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reset team challenges").Err()
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to move stand lab").Err()
	}
	return nil
}

// FlagLinkResolver is the optional part of the topology resolver that names the
// flag-linked tasks of a pinned variant. Without it no device gets a flag.
type FlagLinkResolver interface {
	ResolveDeployedFlagLinks(context.Context, uuid.UUID, int32) ([]exerciseModel.FlagLink, error)
}

type topologyKey struct {
	versionID uuid.UUID
	variant   int32
}

// deployAndObserveStandLabs deploys each pending Lab once and then observes
// it until the agent reports it ready or failed. It returns the teams whose
// Labs became ready, so their access policy is re-derived.
func (u *EventUseCase) deployAndObserveStandLabs(ctx context.Context, e eventModel.Event, now time.Time, notDue []uuid.UUID) (map[uuid.UUID]struct{}, error) {
	ready := map[uuid.UUID]struct{}{}
	pending, err := u.labBindings.ListPending(ctx, e.ID, notDue)
	if err != nil {
		return ready, model.ErrPlatform.WithError(err).WithMessage("Failed to list pending stand labs").Err()
	}
	topologies := map[topologyKey]exerciseModel.Topology{}
	budget := u.standDeployBudget
	var errs []error
	for _, lab := range pending {
		binding := lab.Binding
		if binding.DeployedAt == nil {
			if budget == 0 {
				continue
			}
			budget--
			if err = u.deployStandLab(ctx, e, lab, topologies, now); err != nil {
				errs = append(errs, err)
			}
			continue
		}
		status, statusErr := u.infra.LabStatus(ctx, binding.LabGroupName, binding.LabName)
		if statusErr != nil {
			// A transient agent error is retried next pass; a Lab that stays
			// unreadable past the deploy timeout fails like a stuck one.
			if now.Sub(*binding.DeployedAt) < eventStandModel.DeployTimeout {
				continue
			}
			if _, err = u.failStandLab(ctx, binding, eventStandModel.Reason("Laboratory status unavailable: "+statusErr.Error())); err != nil {
				errs = append(errs, model.ErrPlatform.WithError(err).WithMessage("Failed to fail stand lab").Err())
			}
			continue
		}
		switch outcome, reason := eventStandModel.Classify(status, *binding.DeployedAt, now); outcome {
		case eventStandModel.OutcomeReady:
			changed, markErr := u.labBindings.MarkReady(ctx, binding)
			if markErr != nil {
				errs = append(errs, model.ErrPlatform.WithError(markErr).WithMessage("Failed to mark stand lab ready").Err())
			} else if changed {
				ready[binding.EventTeamID] = struct{}{}
			}
		case eventStandModel.OutcomeFailed:
			if _, err = u.failStandLab(ctx, binding, reason); err != nil {
				errs = append(errs, model.ErrPlatform.WithError(err).WithMessage("Failed to fail stand lab").Err())
			}
		}
	}
	return ready, errors.Join(errs...)
}

// failStandLab fails the pending Lab of a team's stand and tells the error journal about a failed lab deploy. The
// journal hears about it once per failure (only when this call changed the binding).
func (u *EventUseCase) failStandLab(ctx context.Context, binding labBindingModel.Binding, reason string) (bool, error) {
	changed, err := u.labBindings.MarkFailed(ctx, binding, reason)
	if err == nil && changed {
		errorJournal.Report(errorJournal.Event{
			Kind: errorJournal.KindLabDeploy, Source: "stands", Message: reason,
			Details: map[string]string{
				"event_id": binding.EventID.String(), "lab_group": binding.LabGroupName, "lab": binding.LabName,
			},
		})
	}
	return changed, err
}

func (u *EventUseCase) deployStandLab(ctx context.Context, e eventModel.Event, lab labBindingRepo.PendingLab, topologies map[topologyKey]exerciseModel.Topology, now time.Time) error {
	if u.topologies == nil {
		return model.ErrPlatform.WithMessage("Laboratory deployment is not configured").Err()
	}
	key := topologyKey{versionID: lab.ExerciseVersionID, variant: lab.VariantIndex}
	topology, cached := topologies[key]
	if !cached {
		resolved, err := u.topologies.ResolveDeployedTopology(ctx, lab.ExerciseVersionID, lab.VariantIndex)
		if err != nil {
			if _, markErr := u.failStandLab(ctx, lab.Binding, eventStandModel.Reason("Topology unavailable: "+err.Error())); markErr != nil {
				return model.ErrPlatform.WithError(markErr).WithMessage("Failed to fail stand lab").Err()
			}
			return nil
		}
		topology, topologies[key] = resolved, resolved
	}
	topology, err := u.withTeamFlags(ctx, lab, topology)
	if err != nil {
		if _, markErr := u.failStandLab(ctx, lab.Binding, eventStandModel.Reason("Flag injection failed: "+err.Error())); markErr != nil {
			return model.ErrPlatform.WithError(markErr).WithMessage("Failed to fail stand lab").Err()
		}
		return nil
	}
	// The team's group is placed and sized by what the whole event puts on it (largest device, team size, internet labs).
	if err := u.infra.DeployLab(u.withPlacementNeed(ctx, e.ID), lab.Binding.LabGroupName, lab.Binding.LabName, standLabMeta(e, lab), topology); err != nil {
		// The previous group or Lab of this name is still being deleted: not a
		// failure. The binding stays pending and the next pass deploys again.
		if terminating, ok := infraModel.AsTerminating(err); ok {
			log.Info().Err(terminating).Str("lab_group", lab.Binding.LabGroupName).Str("lab", lab.Binding.LabName).Msg("Stand lab deploy waits for deletion to finish")
			return nil
		}
		if _, markErr := u.failStandLab(ctx, lab.Binding, eventStandModel.Reason("Deploy failed: "+err.Error())); markErr != nil {
			return model.ErrPlatform.WithError(markErr).WithMessage("Failed to fail stand lab").Err()
		}
		return nil
	}
	if _, err := u.labBindings.MarkDeployed(ctx, lab.Binding, now); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record stand lab deploy").Err()
	}
	return nil
}

// standLabMeta tells the infrastructure what a team's Lab is: labels to select it by event, team
// and exercise (the Lab is shared by the tasks of the exercise), and, for an event that locks its rosters at
// the start, the exercise as its deploy group, so the same exercise comes up for every team in one go. A rolling event has no such moment: its
// Labs are independent.
func standLabMeta(e eventModel.Event, lab labBindingRepo.PendingLab) infraModel.LabMeta {
	binding := lab.Binding
	meta := infraModel.LabMeta{
		Labels: map[string]string{
			infraModel.LabelKind:    infraModel.KindStand,
			infraModel.LabelEvent:   e.ID.String(),
			infraModel.LabelTeam:    binding.EventTeamID.String(),
			infraModel.LabelTask:    lab.EventExerciseID.String(),
			infraModel.LabelVersion: lab.ExerciseVersionID.String(),
		},
		GroupLabels: map[string]string{
			infraModel.LabelKind:  infraModel.KindStand,
			infraModel.LabelEvent: e.ID.String(),
			infraModel.LabelTeam:  binding.EventTeamID.String(),
		},
	}
	if e.Lifecycle.JoinPolicy == eventModel.JoinPolicyLockedAtStart {
		meta.DeployGroup = lab.EventExerciseID.String()
	}
	return meta
}

// withTeamFlags sets, for every task of the variant linked to a device, the
// team's stored expected flag as that device's env var. The value is read from
// the team challenge that was pinned at materialization; it is never re-rolled,
// so a recreated Lab shows the same flag as before. A task the event does not
// use has no team challenge and adds nothing.
func (u *EventUseCase) withTeamFlags(ctx context.Context, lab labBindingRepo.PendingLab, topology exerciseModel.Topology) (exerciseModel.Topology, error) {
	resolver, ok := u.topologies.(FlagLinkResolver)
	if !ok {
		return topology, nil
	}
	links, err := resolver.ResolveDeployedFlagLinks(ctx, lab.ExerciseVersionID, lab.VariantIndex)
	if err != nil || len(links) == 0 {
		return topology, err
	}
	teamChallenges, err := u.teamChallenges.List(ctx, lab.Binding.EventTeamID)
	if err != nil {
		return topology, err
	}
	flagByTask := make(map[uuid.UUID]string, len(teamChallenges))
	for _, tc := range teamChallenges {
		challenge, getErr := u.eventChallenges.GetForEvent(ctx, lab.Binding.EventID, tc.EventChallengeID)
		if getErr != nil {
			return topology, getErr
		}
		flagByTask[challenge.TaskID] = tc.ExpectedFlag
	}
	for i := range links {
		links[i].Flag = flagByTask[links[i].TaskID]
	}
	return topology.WithFlagEnv(links)
}

// assessStands persists every candidate's stand status and reports whether
// all admitted teams and the moderators team are ready at once.
func (u *EventUseCase) assessStands(ctx context.Context, e eventModel.Event, now time.Time, notDue []uuid.UUID) (bool, error) {
	teams, err := u.stands.ListTeams(ctx, e.ID, notDue)
	if err != nil {
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to list stand teams").Err()
	}
	allReady := true
	var errs []error
	for _, team := range teams {
		if team.HasStand && team.Status == eventStandModel.StatusRemoved {
			continue
		}
		status, reason := eventStandModel.Assess(team.Counters)
		if (team.Admitted || team.Moderators) && status != eventStandModel.StatusReady {
			allReady = false
		}
		if err = u.recordStandStatus(ctx, e, team, status, reason, now); err != nil {
			errs = append(errs, err)
		}
	}
	return allReady, errors.Join(errs...)
}

// recordStandStatus writes a changed status conditionally on the one read,
// and publishes event.lab.failed to every owner and moderator in the same
// transaction when the stand newly fails.
func (u *EventUseCase) recordStandStatus(ctx context.Context, e eventModel.Event, team eventStandRepo.Team, status eventStandModel.Status, reason string, now time.Time) error {
	generation := team.LabGeneration
	if team.HasStand && team.Status == status && team.Reason == reason && team.Generation == generation {
		return nil
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	stands := eventStandRepo.New(txRepo)
	var changed bool
	if team.HasStand {
		changed, err = stands.UpdateStand(txCtx, team.TeamID, team.Status, status, reason, generation, now)
	} else {
		changed, err = stands.CreateStand(txCtx, e.ID, team.TeamID, status, reason, generation, now)
	}
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save stand status").Err()
	}
	if !changed {
		return nil
	}
	if status == eventStandModel.StatusFailed && (!team.HasStand || team.Status != eventStandModel.StatusFailed) {
		if err = u.publishStandFailure(txCtx, txRepo, e, team, reason); err != nil {
			return err
		}
	}
	if err = unit.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save stand status").Err()
	}
	return nil
}

func (u *EventUseCase) publishStandFailure(ctx context.Context, repo IRepository, e eventModel.Event, team eventStandRepo.Team, reason string) error {
	if u.signalPublishers == nil {
		return nil
	}
	publisher := u.signalPublishers(repo)
	if publisher == nil {
		return model.ErrPlatform.WithMessage("Event signal publisher is not configured").Err()
	}
	recipients, err := eventStandRepo.New(repo).ListRecipients(ctx, e.ID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list stand failure recipients").Err()
	}
	teamName := team.PublicName
	if team.Moderators {
		teamName = "Команда модераторів"
	}
	for _, recipient := range recipients {
		if err = publisher.Publish(ctx, signalModel.TypeEventLabFailed, &signalModel.EventLabFailedPayload{
			ScopeEventID: e.ID, SubjectUserID: recipient, EventTag: e.Tag, EventName: e.Name,
			TeamID: team.TeamID, TeamName: teamName, Reason: reason,
		}); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to publish stand failure signal").Err()
		}
	}
	return nil
}

// tearDownEventStands is the final stand step after the effective finish
// plus the teardown delay: every team LabGroup (and so every Lab and VPN
// client) is removed. A failed agent call leaves the event for the next pass.
func (u *EventUseCase) tearDownEventStands(ctx context.Context, e eventModel.Event, now time.Time) error {
	if e.InfrastructureAllowed {
		if u.infra == nil {
			return infraUnavailable()
		}
		if u.infrastructureCapability != nil {
			if err := u.infrastructureCapability.RequireLaboratories(ctx); err != nil {
				return err
			}
		}
		groups, err := u.stands.ListLabGroups(ctx, e.ID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to list event laboratory groups").Err()
		}
		for _, group := range groups {
			if err = u.infra.DestroyLabGroup(ctx, group); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to destroy team stand").Err()
			}
		}
		if err = u.labBindings.MarkEventDestroyed(ctx, e.ID); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to mark stand labs destroyed").Err()
		}
		if err = u.stands.RemoveStands(ctx, e.ID, now); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to mark stands removed").Err()
		}
	}
	if err := u.stands.TearDownRollout(ctx, e.ID, now); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to finish stand rollout").Err()
	}
	return nil
}

// publishAvailableChallenges applies the same publication rule the engine
// uses, for a moderator's board-publish action between passes.
func (u *EventUseCase) publishAvailableChallenges(ctx context.Context, eventID uuid.UUID) error {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	rollout, err := u.standRollout(ctx, eventID)
	if err != nil {
		return err
	}
	labsOpen := e.InfrastructureAllowed && rollout.OpenedAt != nil
	if _, err = u.teamChallenges.PublishAvailable(ctx, eventID, labsOpen); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to publish available team challenges").Err()
	}
	return nil
}
