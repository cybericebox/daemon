package event

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRetentionRepo"
	"github.com/cybericebox/daemon/internal/model"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// DefaultLabSweepGrace is how old a lab group must be before the orphan sweep judges it: a stand or
// test deploy that is still creating its group must not be raced.
const DefaultLabSweepGrace = 15 * time.Minute

// maxSweepDeletes bounds the groups one pass deletes, so a wrong premise cannot empty a cluster in one go;
// the next pass continues.
const maxSweepDeletes = 100

func labSweepGrace(d time.Duration) time.Duration {
	if d <= 0 {
		return DefaultLabSweepGrace
	}
	return d
}

// SweepOrphanLabGroups deletes the stand and test lab groups of this platform instance whose owner is
// definitively gone. It is the safety net behind the event-delete cleanup queue, which cannot cover a
// deploy that creates its group after the cleanup, a team created at the instant of the delete, test
// labs, or groups that are older than the queue.
//
// A group is judged only when the agent lists it (the agent answers for the tenant of the platform's
// own certificate, and the listing is limited to this instance's label) and it is older than the grace
// period. It is deleted only on a positive fact: the event row is gone, the team row of a stand is
// gone, or no test deploy row names a test group. Any read error keeps the group.
func (u *EventUseCase) SweepOrphanLabGroups(ctx context.Context) error {
	return u.SweepOrphanLabGroupsAt(ctx, time.Now())
}

// SweepOrphanLabGroupsAt is SweepOrphanLabGroups with the clock given.
func (u *EventUseCase) SweepOrphanLabGroupsAt(ctx context.Context, now time.Time) error {
	sweeper, ok := u.infra.(infraModel.LabGroupSweeper)
	if !ok {
		return nil
	}
	if u.infrastructureCapability != nil {
		if err := u.infrastructureCapability.RequireLaboratories(ctx); err != nil {
			return err
		}
	}
	groups, failedAgents, err := sweeper.ListSweepableGroups(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list lab groups for the orphan sweep").Err()
	}

	var stands, tests []infraModel.LabGroupInfo
	kept := 0
	for _, g := range groups {
		if u.lifecycleControls {
			owned, e := eventLabRetentionRepo.New(u.repo).OwnedGroup(ctx, g.Name)
			if e != nil {
				return e
			}
			if owned {
				kept++
				continue
			}
		}
		switch {
		case g.CreatedAt.IsZero() || now.Sub(g.CreatedAt) < u.labSweepGrace:
			kept++
		case g.Labels[infraModel.LabelKind] == infraModel.KindStand:
			stands = append(stands, g)
		case g.Labels[infraModel.LabelKind] == infraModel.KindTest:
			tests = append(tests, g)
		default:
			kept++
		}
	}

	doomed, undecided := u.judgeStands(ctx, stands)
	testDoomed, testUndecided := u.judgeTests(ctx, tests)
	doomed = append(doomed, testDoomed...)
	undecided += testUndecided

	deleted, failed := 0, 0
	for _, d := range doomed {
		if deleted >= maxSweepDeletes {
			log.Warn().Int("limit", maxSweepDeletes).Msg("Lab group sweep: deletion limit reached, the rest waits for the next pass")
			break
		}
		if err := sweeper.DestroyLabGroupOn(ctx, d.group.Agent, d.group.Name); err != nil {
			failed++
			log.Warn().Err(err).Str("group", d.group.Name).Str("reason", d.reason).Msg("Lab group sweep: failed to delete an orphan group")
			continue
		}
		deleted++
		log.Info().Str("group", d.group.Name).Str("reason", d.reason).Time("created", d.group.CreatedAt).Msg("Lab group sweep: deleted an orphan group")
	}
	// An idle pass (the usual one at a short interval) is silent; kept groups alone are a debug fact.
	summary := log.Debug()
	if deleted > 0 || failed > 0 || failedAgents > 0 {
		summary = log.Info()
	}
	if deleted > 0 || failed > 0 || undecided > 0 || failedAgents > 0 {
		summary.Int("listed", len(groups)).Int("deleted", deleted).Int("delete_failed", failed).
			Int("kept_young", kept).Int("kept_unknown", undecided).Int("agents_unreadable", failedAgents).Msg("Lab group sweep finished")
	}
	return nil
}

type sweepVerdict struct {
	group  infraModel.LabGroupInfo
	reason string
}

func parseLabelID(labels map[string]string, key string) (uuid.UUID, bool) {
	id, err := uuid.FromString(labels[key])
	return id, err == nil && id != uuid.Nil
}

// judgeStands returns the stand groups whose event or team row is gone. A group with an unreadable
// label, or any group of a class whose lookup failed, is counted as undecided and kept.
func (u *EventUseCase) judgeStands(ctx context.Context, groups []infraModel.LabGroupInfo) (doomed []sweepVerdict, undecided int) {
	if len(groups) == 0 {
		return nil, 0
	}
	type ref struct {
		g           infraModel.LabGroupInfo
		event, team uuid.UUID
	}
	refs := make([]ref, 0, len(groups))
	var eventIDs, teamIDs []uuid.UUID
	for _, g := range groups {
		ev, okEvent := parseLabelID(g.Labels, infraModel.LabelEvent)
		tm, okTeam := parseLabelID(g.Labels, infraModel.LabelTeam)
		if !okEvent || !okTeam {
			undecided++
			continue
		}
		refs = append(refs, ref{g, ev, tm})
		eventIDs = append(eventIDs, ev)
		teamIDs = append(teamIDs, tm)
	}
	if len(refs) == 0 {
		return nil, undecided
	}
	events, err := u.labBindings.ExistingEventIDs(ctx, eventIDs)
	if err != nil {
		log.Warn().Err(err).Msg("Lab group sweep: could not read the events, keeping every stand group")
		return nil, undecided + len(refs)
	}
	teams, err := u.labBindings.ExistingTeamIDs(ctx, teamIDs)
	if err != nil {
		log.Warn().Err(err).Msg("Lab group sweep: could not read the teams, keeping every stand group")
		return nil, undecided + len(refs)
	}
	haveEvent, haveTeam := idSet(events), idSet(teams)
	for _, r := range refs {
		switch {
		case !haveEvent[r.event]:
			doomed = append(doomed, sweepVerdict{r.g, "event is gone"})
		case !haveTeam[r.team]:
			doomed = append(doomed, sweepVerdict{r.g, "team is gone"})
		}
	}
	return doomed, undecided
}

// judgeTests returns the test groups that no test deploy row names any more. A test group that also
// carries an event label is judged by that event: gone means orphan, present means kept (nothing
// else says whether such a group is still wanted).
func (u *EventUseCase) judgeTests(ctx context.Context, groups []infraModel.LabGroupInfo) (doomed []sweepVerdict, undecided int) {
	if len(groups) == 0 {
		return nil, 0
	}
	var names []string
	var eventIDs []uuid.UUID
	for _, g := range groups {
		if _, linked := g.Labels[infraModel.LabelEvent]; linked {
			ev, ok := parseLabelID(g.Labels, infraModel.LabelEvent)
			if !ok {
				undecided++
				continue
			}
			eventIDs = append(eventIDs, ev)
			continue
		}
		names = append(names, g.Name)
	}
	var haveEvent map[uuid.UUID]bool
	eventsOK := true
	if len(eventIDs) > 0 {
		events, err := u.labBindings.ExistingEventIDs(ctx, eventIDs)
		if err != nil {
			log.Warn().Err(err).Msg("Lab group sweep: could not read the events, keeping the event-linked test groups")
			eventsOK = false
		}
		haveEvent = idSet(events)
	}
	var haveDeploy map[string]bool
	deploysOK := true
	if len(names) > 0 {
		held, err := u.labBindings.TestDeployGroups(ctx, names)
		if err != nil {
			log.Warn().Err(err).Msg("Lab group sweep: could not read the test deploys, keeping the test groups")
			deploysOK = false
		}
		haveDeploy = make(map[string]bool, len(held))
		for _, n := range held {
			haveDeploy[n] = true
		}
	}
	for _, g := range groups {
		if ev, ok := parseLabelID(g.Labels, infraModel.LabelEvent); ok {
			switch {
			case !eventsOK:
				undecided++
			case !haveEvent[ev]:
				doomed = append(doomed, sweepVerdict{g, "event of the test lab is gone"})
			}
			continue
		}
		if _, linked := g.Labels[infraModel.LabelEvent]; linked {
			continue // counted above
		}
		switch {
		case !deploysOK:
			undecided++
		case !haveDeploy[g.Name]:
			doomed = append(doomed, sweepVerdict{g, "no test deploy holds the group"})
		}
	}
	return doomed, undecided
}

func idSet(ids []uuid.UUID) map[uuid.UUID]bool {
	set := make(map[uuid.UUID]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return set
}
