package eventAnalytics

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// leadersLimit is how many teams the overview's mini leaderboard shows.
const leadersLimit = 5

// commsWindow is how far back the overview's mail counters look.
const commsWindow = 24 * time.Hour

// leadersOf takes the top of the scoreboard (ranked teams only, in the
// scoreboard order) with each team's gap to the first place.
func leadersOf(teams []eventAnalyticsRepo.RankedTeam, limit int) ([]LeaderView, int64) {
	out := []LeaderView{}
	var place int64
	var top int64
	for _, t := range teams {
		if !ranked(t) {
			continue
		}
		place++
		if place == 1 {
			top = t.Points
		}
		if len(out) < limit {
			out = append(out, LeaderView{TeamID: t.ID, Name: t.Name, Rank: place, Points: t.Points, Solved: t.Solved, Gap: top - t.Points})
		}
	}
	return out, place
}

// engagementOf counts how many admitted teams solved anything and the mean
// solves per admitted team. The active counters come from the overview counts.
func engagementOf(teams []eventAnalyticsRepo.RankedTeam) EngagementView {
	var v EngagementView
	var solves int64
	for _, t := range teams {
		if !t.Admitted {
			continue
		}
		v.Teams++
		solves += t.Solved
		if t.Solved > 0 {
			v.TeamsSolving++
		}
	}
	if v.Teams > 0 {
		v.AvgSolves = float64(solves) / float64(v.Teams)
	}
	return v
}

// tasksSnapshotOf sums up the task table: unsolved tasks, the most and least
// solved of the solved ones (ties keep the board order) and the first bloods.
func tasksSnapshotOf(challenges []eventAnalyticsRepo.Challenge, stats []eventAnalyticsRepo.TaskStats) TasksSnapshotView {
	byID := make(map[uuid.UUID]eventAnalyticsRepo.TaskStats, len(stats))
	for _, s := range stats {
		byID[s.ChallengeID] = s
	}
	v := TasksSnapshotView{Total: int64(len(challenges))}
	for _, c := range challenges {
		s := byID[c.ID]
		if s.FirstBloodAt != nil {
			v.FirstBloods++
		}
		if s.Solves == 0 {
			v.Unsolved++
			continue
		}
		ref := &TaskSolvesView{ChallengeID: c.ID, Name: c.Name, Solves: s.Solves}
		if v.MostSolved == nil || s.Solves > v.MostSolved.Solves {
			v.MostSolved = ref
		}
		if v.LeastSolved == nil || s.Solves < v.LeastSolved.Solves {
			v.LeastSolved = ref
		}
	}
	return v
}

// commsSnapshot totals the mail handed to the transport and failed over the
// dispatches read.
func commsSnapshot(dispatches []eventAnalyticsRepo.DispatchCount, since time.Time) CommsSnapshotView {
	v := CommsSnapshotView{Since: since}
	for _, d := range dispatches {
		if d.Channel != channelEmail {
			continue
		}
		switch d.Status {
		case dispatchDone:
			v.EmailSent += d.Targets
		case dispatchError:
			v.EmailFailed += d.Targets
		}
	}
	return v
}

// loadSnapshots reads the leaderboard, task and engagement parts of the
// overview, and the mail counters of the last 24 hours.
func (u *EventAnalyticsUseCase) loadSnapshots(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period, now time.Time) (OverviewSnapshots, error) {
	teams, err := u.store.RankedTeams(ctx, eventID)
	if err != nil {
		return OverviewSnapshots{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the ranked teams").Err()
	}
	challenges, err := u.store.Challenges(ctx, eventID)
	if err != nil {
		return OverviewSnapshots{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
	}
	stats, err := u.store.TaskStats(ctx, eventID, period)
	if err != nil {
		return OverviewSnapshots{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read task statistics").Err()
	}
	since := now.Add(-commsWindow)
	dispatches, err := u.store.DispatchStats(ctx, eventID, &since, &now)
	if err != nil {
		return OverviewSnapshots{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read notification dispatches").Err()
	}
	leaders, rankedTeams := leadersOf(teams, leadersLimit)
	return OverviewSnapshots{
		Leaders:     leaders,
		RankedTeams: rankedTeams,
		Tasks:       tasksSnapshotOf(challenges, stats),
		Engagement:  engagementOf(teams),
		Comms:       commsSnapshot(dispatches, since),
	}, nil
}
