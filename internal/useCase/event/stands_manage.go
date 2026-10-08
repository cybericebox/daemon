package event

import (
	"context"
	"errors"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"sort"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventStandRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	labMonitoringModel "github.com/cybericebox/daemon/internal/model/labMonitoring"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	teamChallengeModel "github.com/cybericebox/daemon/internal/model/teamChallenge"
)

// StandsView is the moderator «Стенди» read model.
type StandsView struct {
	InfrastructureAllowed bool
	LaboratoriesAvailable bool
	Timing                eventStandModel.Timing
	DeployAt              *time.Time
	TeardownAt            *time.Time
	ChallengesOpened      bool
	Summary               StandSummaryView
	// Prewarm is the state of the image cache for the event's images; nil while nothing was prewarmed.
	Prewarm *PrewarmSummary
	Items   []StandTeamView
}

type StandSummaryView struct {
	Total, NotDeployed, Creating, Ready, Failed, Removed int
}

type StandTeamView struct {
	TeamID     uuid.UUID
	TeamName   string
	Moderators bool
	Status     eventStandModel.Status
	Reason     string
	UpdatedAt  *time.Time
	Generation int32
	Labs       []StandLabView
	// Launch is how many of the stand's labs wait in the launch queue (and the place of the best one)
	// and whether an image is pulled by tag; from the current monitoring state, no agent calls.
	Launch labMonitoringModel.Launch
}

type StandLabView struct {
	ChallengeID   uuid.UUID
	ChallengeName string
	Readiness     labBindingModel.Readiness
	Reason        string
}

type ModeratorsChallengeView struct {
	ChallengeID  uuid.UUID
	Name         string
	Readiness    teamChallengeModel.Readiness
	LabReadiness *labBindingModel.Readiness
}

// requireInfrastructureEvent is the single gate of every stand route: stands
// exist only on events the platform administrator allowed infrastructure for.
func (u *EventUseCase) requireInfrastructureEvent(ctx context.Context, eventID uuid.UUID) (eventModel.Event, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.Event{}, eventModel.ErrEventNotFound.Err()
		}
		return eventModel.Event{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	if !e.InfrastructureAllowed {
		return eventModel.Event{}, eventStandModel.ErrStandInfrastructureNotAllowed.Err()
	}
	return e, nil
}

// GetEventStands lists the stand of every admitted team and of the
// moderators team, with the schedule and a status summary.
func (u *EventUseCase) GetEventStands(ctx context.Context, eventID uuid.UUID) (StandsView, error) {
	e, err := u.requireInfrastructureEvent(ctx, eventID)
	if err != nil {
		return StandsView{}, err
	}
	config, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return StandsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get stand timing").Err()
	}
	rollout, err := u.standRollout(ctx, eventID)
	if err != nil {
		return StandsView{}, err
	}
	schedule := u.standSchedule(ctx, e, time.Now())
	teams, err := u.stands.ListTeams(ctx, eventID, schedule.NotDue)
	if err != nil {
		return StandsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list stand teams").Err()
	}
	labs, err := u.stands.ListLabs(ctx, eventID)
	if err != nil {
		return StandsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list stand labs").Err()
	}
	labsByTeam := make(map[uuid.UUID][]StandLabView, len(teams))
	for _, lab := range labs {
		labsByTeam[lab.TeamID] = append(labsByTeam[lab.TeamID], StandLabView{ChallengeID: lab.ChallengeID, ChallengeName: lab.ChallengeName, Readiness: lab.Readiness, Reason: lab.FailureReason})
	}
	view := StandsView{
		InfrastructureAllowed: true,
		LaboratoriesAvailable: u.laboratoriesUsable(ctx),
		Timing:                config.StandTiming,
		ChallengesOpened:      rollout.OpenedAt != nil,
		Prewarm:               u.prewarm.summaryOf(eventID),
		Items:                 make([]StandTeamView, 0, len(teams)),
	}
	if e.Lifecycle.Configured {
		deployAt := schedule.InitialDeployAt
		view.DeployAt = &deployAt
		view.TeardownAt = config.StandTiming.TeardownAt(e.Lifecycle.EffectiveFinishAt())
	}
	// The live queue and image warnings are optional: a failed read leaves them empty, the list still loads.
	launchByTeam := map[uuid.UUID]labMonitoringModel.Launch{}
	if current, currentErr := u.observations.CurrentForEvent(ctx, eventID); currentErr == nil {
		for _, group := range current {
			launch := launchByTeam[group.EventTeamID]
			launch.Add(labMonitoringModel.PayloadLaunch(group.Payload))
			launchByTeam[group.EventTeamID] = launch
		}
	}
	for _, team := range teams {
		item := toStandTeamView(team)
		item.Launch = launchByTeam[team.TeamID]
		item.Labs = labsByTeam[team.TeamID]
		if item.Labs == nil {
			item.Labs = []StandLabView{}
		}
		view.Items = append(view.Items, item)
		view.Summary.add(item.Status)
	}
	return view, nil
}

func toStandTeamView(team eventStandRepo.Team) StandTeamView {
	item := StandTeamView{TeamID: team.TeamID, TeamName: team.PublicName, Moderators: team.Moderators, Status: team.Status, Reason: team.Reason, UpdatedAt: team.UpdatedAt, Generation: team.Generation}
	if team.Moderators {
		// The stored name is technical; clients label the moderators team.
		item.TeamName = ""
	}
	return item
}

func (s *StandSummaryView) add(status eventStandModel.Status) {
	s.Total++
	switch status {
	case eventStandModel.StatusCreating:
		s.Creating++
	case eventStandModel.StatusReady:
		s.Ready++
	case eventStandModel.StatusFailed:
		s.Failed++
	case eventStandModel.StatusRemoved:
		s.Removed++
	default:
		s.NotDeployed++
	}
}

// UpdateEventStandSettings changes only the stand schedule. The engine reads
// it on every pass, so a new lead or delay applies without rescheduling.
func (u *EventUseCase) UpdateEventStandSettings(ctx context.Context, eventID uuid.UUID, timing eventStandModel.Timing, by uuid.UUID) (StandsView, error) {
	if _, err := u.requireInfrastructureEvent(ctx, eventID); err != nil {
		return StandsView{}, err
	}
	now := time.Now()
	if _, err := u.mutateEventConfig(ctx, eventID, func(cfg *eventConfigModel.EventConfig) error {
		return cfg.SetStandTiming(timing, now, by)
	}); err != nil {
		return StandsView{}, err
	}
	return u.GetEventStands(ctx, eventID)
}

// RecreateTeamStand replaces every Lab of one team stand (one per exercise, shared by its tasks). The LabGroup and so
// every issued VPN configuration survive; the team loses Lab routes until the
// new Labs are ready, and not yet published infrastructure challenges wait.
// The team's failed-laboratory request is closed as fixed by the caller.
func (u *EventUseCase) RecreateTeamStand(ctx context.Context, eventID, teamID, by uuid.UUID) (StandTeamView, error) {
	e, err := u.requireInfrastructureEvent(ctx, eventID)
	if err != nil {
		return StandTeamView{}, err
	}
	now := time.Now()
	if finish := e.Lifecycle.EffectiveFinishAt(); finish != nil && !now.Before(*finish) {
		return StandTeamView{}, eventStandModel.ErrStandEventFinished.Err()
	}
	current, err := u.standTeam(ctx, eventID, teamID)
	if err != nil {
		return StandTeamView{}, err
	}
	if !current.HasStand || current.Status == eventStandModel.StatusRemoved {
		return StandTeamView{}, eventStandModel.ErrStandNotDeployed.Err()
	}
	if !u.laboratoriesUsable(ctx) {
		return StandTeamView{}, infraUnavailable()
	}
	if u.lifecycleControls {
		return u.recreateRetainedTeamStand(ctx, eventID, teamID, by, now)
	}
	deleter, ok := u.infra.(standLabDeleter)
	if !ok {
		return StandTeamView{}, infraUnavailable()
	}
	labs, err := u.labBindings.ListTeamLive(ctx, teamID)
	if err != nil {
		return StandTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list team stand labs").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return StandTeamView{}, err
	}
	defer unit.Restore()

	// Admission/team locks precede every shared Lab lock. Keep locks through the
	// recreation decision so a concurrent final answer cannot lose its generation.
	if err = eventLabRepo.New(txRepo).LockAdmission(txCtx, teamID); err != nil {
		return StandTeamView{}, err
	}
	labs, err = labBindingRepo.New(txRepo).ListTeamLive(txCtx, teamID)
	if err != nil {
		return StandTeamView{}, err
	}
	ids := map[uuid.UUID]bool{}
	var ordered []uuid.UUID
	for _, binding := range labs {
		if binding.LabID.Valid && !ids[binding.LabID.UUID] {
			ids[binding.LabID.UUID] = true
			ordered = append(ordered, binding.LabID.UUID)
		}
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].String() < ordered[j].String() })
	terminal := map[uuid.UUID]bool{}
	for _, id := range ordered {
		lab, lockErr := eventLabRepo.New(txRepo).Lock(txCtx, id)
		if lockErr != nil {
			return StandTeamView{}, lockErr
		}
		terminal[id] = lab.CloseReason == "solved"
	}
	originalLabs := labs
	labs = nil
	preserved := map[[2]string]struct{}{}
	for _, binding := range originalLabs {
		if binding.LabID.Valid && terminal[binding.LabID.UUID] {
			preserved[[2]string{binding.LabGroupName, binding.LabName}] = struct{}{}
			continue
		}
		labs = append(labs, binding)
	}
	// The tasks of an exercise share one Lab: each Lab is deleted once.
	deleted := preserved
	for _, lab := range labs {
		key := [2]string{lab.LabGroupName, lab.LabName}
		if _, done := deleted[key]; done {
			continue
		}
		deleted[key] = struct{}{}
		if err = deleter.DeleteLab(ctx, lab.LabGroupName, lab.LabName); err != nil {
			return StandTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to delete team stand lab").Err()
		}
	}
	if err = u.deleteStaleTeamLabs(ctx, teamID, originalLabs, deleted); err != nil {
		return StandTeamView{}, err
	}
	bindings := labBindingRepo.New(txRepo)
	if err = bindings.ResetUnpublishedForRecreate(txCtx, teamID); err != nil {
		return StandTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to reset team challenges").Err()
	}
	generation := current.LabGeneration
	for _, lab := range labs {
		next := lab.Generation + 1
		if _, err = bindings.Recreate(txCtx, lab, labBindingModel.NextLabName(lab.LabName, next), next); err != nil {
			return StandTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to recreate team stand lab").Err()
		}
		if next > generation {
			generation = next
		}
	}
	// A concurrent engine pass may have changed the status first; the next
	// pass assesses the recreated Labs as creating either way.
	if _, err = eventStandRepo.New(txRepo).UpdateStand(txCtx, teamID, current.Status, eventStandModel.StatusCreating, "", generation, now); err != nil {
		return StandTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save stand status").Err()
	}
	if err = unit.Save(); err != nil {
		return StandTeamView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to recreate team stand").Err()
	}
	if u.standInbox != nil {
		// Best-effort: the stand is already being re-created.
		if inboxErr := u.standInbox.StandRecreated(ctx, eventID, teamID, by); inboxErr != nil {
			log.Error().Err(inboxErr).Str("team_id", teamID.String()).Msg("Failed to resolve stand failure requests")
		}
	}
	if u.supportsLabAccessPolicy() {
		if err = u.RequestLabAccessSync(ctx, teamID); err != nil {
			return StandTeamView{}, err
		}
	}
	updated, err := u.standTeam(ctx, eventID, teamID)
	if err != nil {
		return StandTeamView{}, err
	}
	return toStandTeamView(updated), nil
}

// deleteStaleTeamLabs removes the Labs of the team's group that no binding is going to use: the
// recreate deletes the Labs it knows, and a Lab left from an earlier generation or a failed
// deploy would otherwise stay in the group for good. The names the recreate moves the bindings
// to and the ones just deleted are left alone, so a Lab deployed for them in the meantime is not touched.
func (u *EventUseCase) deleteStaleTeamLabs(ctx context.Context, teamID uuid.UUID, labs []labBindingModel.Binding, deleted map[[2]string]struct{}) error {
	lister, ok := u.infra.(standLabLister)
	deleter, canDelete := u.infra.(standLabDeleter)
	if !ok || !canDelete || len(labs) == 0 {
		return nil
	}
	wanted := make(map[string]struct{}, len(labs))
	for _, lab := range labs {
		wanted[labBindingModel.NextLabName(lab.LabName, lab.Generation+1)] = struct{}{}
	}
	group := labs[0].LabGroupName
	existing, err := lister.ListGroupLabs(ctx, group)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list team stand labs").Err()
	}
	for _, name := range existing {
		if u.lifecycleControls {
			owned, e := u.retainsLabReference(ctx, group, name)
			if e != nil {
				return e
			}
			if owned {
				continue
			}
		}
		if _, keep := wanted[name]; keep {
			continue
		}
		if _, done := deleted[[2]string{group, name}]; done {
			continue
		}
		if err = deleter.DeleteLab(ctx, group, name); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to delete stale team stand lab").Err()
		}
		log.Info().Str("team_id", teamID.String()).Str("lab_group", group).Str("lab", name).Msg("Deleted a stale stand lab on recreate")
	}
	return nil
}

func (u *EventUseCase) standTeam(ctx context.Context, eventID, teamID uuid.UUID) (eventStandRepo.Team, error) {
	teams, err := u.stands.ListTeams(ctx, eventID, nil)
	if err != nil {
		return eventStandRepo.Team{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list stand teams").Err()
	}
	for _, team := range teams {
		if team.TeamID == teamID {
			return team, nil
		}
	}
	return eventStandRepo.Team{}, eventStandModel.ErrStandTeamNotFound.Err()
}

// ListModeratorsChallenges lets managers check what the moderators team got:
// the readiness of every assignment and the state of its Lab.
func (u *EventUseCase) ListModeratorsChallenges(ctx context.Context, eventID uuid.UUID) ([]ModeratorsChallengeView, error) {
	if _, err := u.requireInfrastructureEvent(ctx, eventID); err != nil {
		return nil, err
	}
	rows, err := u.stands.ListModeratorsChallenges(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list moderators team challenges").Err()
	}
	out := make([]ModeratorsChallengeView, 0, len(rows))
	for _, row := range rows {
		out = append(out, ModeratorsChallengeView{ChallengeID: row.ChallengeID, Name: row.Name, Readiness: row.Readiness, LabReadiness: row.LabReadiness})
	}
	return out, nil
}

// GetModeratorsChallengeLabStatus reads the live agent status (including
// access links) of one moderators team Lab.
func (u *EventUseCase) GetModeratorsChallengeLabStatus(ctx context.Context, eventID, challengeID uuid.UUID) (exerciseModel.LabDeployStatus, error) {
	team, err := u.moderatorsTeam(ctx, eventID)
	if err != nil {
		return exerciseModel.LabDeployStatus{}, err
	}
	binding, err := u.labBindings.Get(ctx, team, challengeID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseModel.LabDeployStatus{}, eventStandModel.ErrStandLabNotFound.Err()
		}
		return exerciseModel.LabDeployStatus{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get lab binding").Err()
	}
	if binding.LabID.Valid {
		canonical, getErr := u.labs.Get(ctx, binding.LabID.UUID)
		if getErr != nil {
			return exerciseModel.LabDeployStatus{}, getErr
		}
		if canonical.ClosedAt != nil || canonical.DesiredState != "Running" {
			return exerciseModel.LabDeployStatus{Phase: "Closed", Access: []exerciseModel.LabAccess{}}, nil
		}
	}
	if u.infra == nil {
		return exerciseModel.LabDeployStatus{}, infraUnavailable()
	}
	status, err := u.infra.LabStatus(ctx, binding.LabGroupName, binding.LabName)
	if err != nil {
		return exerciseModel.LabDeployStatus{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get lab status").Err()
	}
	if binding.LabID.Valid {
		canonical, getErr := u.labs.Get(ctx, binding.LabID.UUID)
		if getErr != nil {
			return exerciseModel.LabDeployStatus{}, getErr
		}
		if canonical.ClosedAt != nil || canonical.DesiredState != "Running" {
			return exerciseModel.LabDeployStatus{Phase: "Closed", Access: []exerciseModel.LabAccess{}}, nil
		}
	}
	return status, nil
}

// GetModeratorsVPNConfig issues the caller's own credential in the moderators
// team LabGroup (one per manager and event, stored like participant ones).
// The route gate admits owners and moderators only.
func (u *EventUseCase) GetModeratorsVPNConfig(ctx context.Context, eventID, userID uuid.UUID) (string, error) {
	if u.vpn == nil {
		return "", model.ErrPlatform.WithMessage("VPN storage is not configured").Err()
	}
	teamID, err := u.moderatorsTeam(ctx, eventID)
	if err != nil {
		return "", err
	}
	if u.infra == nil {
		return "", infraUnavailable()
	}
	group, err := labBindingModel.GroupName(eventID, teamID)
	if err != nil {
		return "", err
	}
	if err = u.admitGroupAllocation(ctx, eventID, teamID); err != nil {
		return "", err
	}
	if err = u.infra.EnsureVPNGroup(u.withPlacementNeed(ctx, eventID), group); err != nil {
		if errors.Is(err, infraModel.ErrNoAgentFitsTask.Err()) {
			return "", err
		}
		if _, terminating := infraModel.AsTerminating(err); terminating {
			return "", infraModel.ErrLabAccessRetry.WithError(err).Err()
		}
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to ensure moderators VPN group").Err()
	}
	config, err := ensureParticipantVPNConfig(ctx, u.vpn, u.infra, group, eventID, userID)
	if err != nil {
		return "", err
	}
	if u.supportsLabAccessPolicy() {
		if err = u.RequestLabAccessSync(ctx, teamID); err != nil {
			return "", err
		}
	}
	return config, nil
}

// moderatorsTeam returns the hidden team of an infrastructure event, creating
// it on demand so managers can prepare VPN before the deploy window opens.
func (u *EventUseCase) moderatorsTeam(ctx context.Context, eventID uuid.UUID) (uuid.UUID, error) {
	e, err := u.requireInfrastructureEvent(ctx, eventID)
	if err != nil {
		return uuid.Nil, err
	}
	return u.resolveModeratorsTeam(ctx, e)
}

// GetOwnStandStatus is the participant's read-only view of the team stand
// (no failure reason); participants never manage stands.
func (u *EventUseCase) GetOwnStandStatus(ctx context.Context, eventID, userID uuid.UUID) (eventStandModel.Status, error) {
	p, err := u.participants.Get(ctx, eventID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventStandModel.StatusNotDeployed, participantModel.ErrParticipantNotApproved.Err()
		}
		return eventStandModel.StatusNotDeployed, model.ErrPlatform.WithError(err).WithMessage("Failed to get event participant").Err()
	}
	if p.Status != participantModel.StatusApproved || p.TeamID == nil {
		return eventStandModel.StatusNotDeployed, participantModel.ErrParticipantNotApproved.Err()
	}
	if err = requireTeamAdmitted(ctx, u.teams, eventID, *p.TeamID); err != nil {
		return eventStandModel.StatusNotDeployed, err
	}
	if _, err = u.requireInfrastructureEvent(ctx, eventID); err != nil {
		return eventStandModel.StatusNotDeployed, err
	}
	status, err := u.stands.GetStatus(ctx, *p.TeamID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventStandModel.StatusNotDeployed, nil
		}
		return eventStandModel.StatusNotDeployed, model.ErrPlatform.WithError(err).WithMessage("Failed to get team stand").Err()
	}
	return status, nil
}
