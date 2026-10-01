package eventAnalytics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

const (
	// DefaultScoreTop is how many leading teams the score chart shows when
	// the caller does not say.
	DefaultScoreTop = 10
	// MaxScoreTop and MaxScoreTeams bound the chart's lines.
	MaxScoreTop   = 50
	MaxScoreTeams = 20
	// DefaultInactiveMinutes is the idle threshold when the caller gives none.
	DefaultInactiveMinutes = 30
	MinInactiveMinutes     = 5
	MaxInactiveMinutes     = 24 * 60
)

// ranked reports whether a team counts on the scoreboard.
func ranked(t eventAnalyticsRepo.RankedTeam) bool { return !t.Hidden && t.Admitted }

// GetEventAnalyticsProgressScores is the score over time of the top N ranked
// teams plus the chosen ones (§6.4). It reads the scoreboard's own score
// function, so a running total matches the scoreboard, hint costs included.
func (u *EventAnalyticsUseCase) GetEventAnalyticsProgressScores(ctx context.Context, eventID uuid.UUID, from, to *time.Time, top int, teamIDs []uuid.UUID) (ScoresView, error) {
	_, _, period, err := u.reportPeriod(ctx, eventID, from, to)
	if err != nil {
		return ScoresView{}, err
	}
	top = min(max(top, 0), MaxScoreTop)
	if len(teamIDs) > MaxScoreTeams {
		teamIDs = teamIDs[:MaxScoreTeams]
	}
	chosen := make([]string, 0, len(teamIDs))
	for _, id := range teamIDs {
		chosen = append(chosen, id.String())
	}
	sort.Strings(chosen)
	key := fmt.Sprintf("scores:%s:%s:%d:%s", eventID, periodKey(period), top, strings.Join(chosen, ","))
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (ScoresView, error) {
		teams, err := u.store.RankedTeams(ctx, eventID)
		if err != nil {
			return ScoresView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the ranked teams").Err()
		}
		selected := map[uuid.UUID]bool{}
		view := ScoresView{Teams: make([]ScoreTeamView, 0, len(teams)), Period: periodView(period)}
		place := int64(0)
		for _, t := range teams {
			tv := ScoreTeamView{TeamID: t.ID, Name: t.Name, Points: t.Points, Solved: t.Solved, Hidden: t.Hidden, Admitted: t.Admitted}
			if ranked(t) {
				place++
				tv.Rank = place
				if place <= int64(top) {
					selected[t.ID] = true
				}
			}
			view.Teams = append(view.Teams, tv)
		}
		known := map[uuid.UUID]bool{}
		for _, t := range teams {
			known[t.ID] = true
		}
		for _, id := range teamIDs {
			if known[id] {
				selected[id] = true
			}
		}
		ids := make([]uuid.UUID, 0, len(selected))
		for i := range view.Teams {
			if selected[view.Teams[i].TeamID] {
				view.Teams[i].Selected = true
				ids = append(ids, view.Teams[i].TeamID)
			}
		}
		events, err := u.store.ScoreEvents(ctx, eventID, ids)
		if err != nil {
			return ScoresView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read score changes").Err()
		}
		view.Series = scoreSeries(view.Teams, events, period, u.now())
		return view, nil
	})
}

// scoreSeries builds the running score of every selected team over the
// period, in the order of the teams (their ranking). The end point is the
// period end, or now when the period is still running.
func scoreSeries(teams []ScoreTeamView, events []eventAnalyticsRepo.ScoreEvent, period eventAnalyticsModel.Period, now time.Time) []ScoreSeriesView {
	end := period.To
	if now.Before(end) && now.After(period.From) {
		end = now
	}
	byTeam := map[uuid.UUID][]eventAnalyticsRepo.ScoreEvent{}
	for _, e := range events {
		byTeam[e.TeamID] = append(byTeam[e.TeamID], e)
	}
	out := []ScoreSeriesView{}
	for _, t := range teams {
		if !t.Selected {
			continue
		}
		var score int64
		var inside []eventAnalyticsRepo.ScoreEvent
		for _, e := range byTeam[t.TeamID] {
			switch {
			case e.At.Before(period.From):
				score += int64(e.Points)
			case e.At.Before(period.To):
				inside = append(inside, e)
			}
		}
		points := []ScorePointView{{At: period.From, Score: score}}
		for _, e := range inside {
			score += int64(e.Points)
			points = append(points, ScorePointView{At: e.At, Score: score})
		}
		if last := points[len(points)-1]; last.At.Before(end) {
			points = append(points, ScorePointView{At: end, Score: score})
		}
		out = append(out, ScoreSeriesView{TeamID: t.TeamID, Name: t.Name, Points: points})
	}
	return out
}

// GetEventAnalyticsProgressMatrix is the team × task matrix (§6.4): solved
// (with the time), tried (with the attempts) or untouched, over the period.
func (u *EventAnalyticsUseCase) GetEventAnalyticsProgressMatrix(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (MatrixView, error) {
	_, _, period, err := u.reportPeriod(ctx, eventID, from, to)
	if err != nil {
		return MatrixView{}, err
	}
	return cachedReportOf(ctx, u.cache, "matrix:"+eventID.String()+":"+periodKey(period), func(ctx context.Context) (MatrixView, error) {
		challenges, err := u.store.Challenges(ctx, eventID)
		if err != nil {
			return MatrixView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
		}
		teams, err := u.store.RankedTeams(ctx, eventID)
		if err != nil {
			return MatrixView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the ranked teams").Err()
		}
		cells, err := u.store.Matrix(ctx, eventID, period)
		if err != nil {
			return MatrixView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the team task matrix").Err()
		}
		view := MatrixView{Period: periodView(period), Tasks: make([]MatrixTaskView, 0, len(challenges)), Cells: []MatrixCellView{}}
		known := map[uuid.UUID]bool{}
		for _, c := range challenges {
			known[c.ID] = true
			view.Tasks = append(view.Tasks, MatrixTaskView{ChallengeID: c.ID, Name: c.Name, GroupName: c.GroupName})
		}
		view.Teams = matrixTeams(teams)
		shown := map[uuid.UUID]bool{}
		for _, t := range view.Teams {
			shown[t.TeamID] = true
		}
		for _, c := range cells {
			if shown[c.TeamID] && known[c.ChallengeID] {
				view.Cells = append(view.Cells, MatrixCellView{TeamID: c.TeamID, ChallengeID: c.ChallengeID, Attempts: c.Attempts, SolvedAt: c.SolvedAt})
			}
		}
		return view, nil
	})
}

func matrixTeams(teams []eventAnalyticsRepo.RankedTeam) []MatrixTeamView {
	out := make([]MatrixTeamView, 0, len(teams))
	for _, t := range teams {
		if ranked(t) {
			out = append(out, MatrixTeamView{TeamID: t.ID, Name: t.Name, Points: t.Points, Solved: t.Solved})
		}
	}
	return out
}

// GetEventAnalyticsProgressHeatmap is the team × hour activity heatmap
// (§6.4), from the 5-minute rollup.
func (u *EventAnalyticsUseCase) GetEventAnalyticsProgressHeatmap(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (HeatmapView, error) {
	_, _, period, err := u.reportPeriod(ctx, eventID, from, to)
	if err != nil {
		return HeatmapView{}, err
	}
	return cachedReportOf(ctx, u.cache, "heatmap:"+eventID.String()+":"+periodKey(period), func(ctx context.Context) (HeatmapView, error) {
		teams, err := u.store.RankedTeams(ctx, eventID)
		if err != nil {
			return HeatmapView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the ranked teams").Err()
		}
		cells, err := u.store.Heatmap(ctx, eventID, period)
		if err != nil {
			return HeatmapView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the activity heatmap").Err()
		}
		state, err := u.store.RollupState(ctx, eventID)
		if err != nil {
			return HeatmapView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read event analytics state").Err()
		}
		view := HeatmapView{
			Teams: matrixTeams(teams), Cells: []HeatCellView{}, Period: periodView(period),
			RefreshedAt: state.RefreshedAt, Final: state.FinalizedAt != nil,
		}
		for at := period.From.Truncate(time.Hour); at.Before(period.To); at = at.Add(time.Hour) {
			view.Hours = append(view.Hours, at)
		}
		shown := map[uuid.UUID]bool{}
		for _, t := range view.Teams {
			shown[t.TeamID] = true
		}
		for _, c := range cells {
			if !shown[c.TeamID] {
				continue
			}
			activity := c.Attempts + c.Opens + c.Solves
			view.MaxActivity = max(view.MaxActivity, activity)
			view.Cells = append(view.Cells, HeatCellView{TeamID: c.TeamID, HourAt: c.HourAt, Attempts: c.Attempts, Opens: c.Opens, Solves: c.Solves, Activity: activity})
		}
		return view, nil
	})
}

// GetEventAnalyticsProgressInactive lists the ranked teams with no activity
// for longer than the given minutes during the event (§6.4). The moment is
// now while the event runs, its finish afterwards; before the start nobody is
// idle.
func (u *EventAnalyticsUseCase) GetEventAnalyticsProgressInactive(ctx context.Context, eventID uuid.UUID, minutes int) (InactiveView, error) {
	e, err := u.event(ctx, eventID)
	if err != nil {
		return InactiveView{}, err
	}
	minutes = min(max(minutes, MinInactiveMinutes), MaxInactiveMinutes)
	now := u.now()
	start := e.Lifecycle.StartAt
	asOf := now
	if finish := e.Lifecycle.EffectiveFinishAt(); finish != nil && finish.Before(asOf) {
		asOf = *finish
	}
	view := InactiveView{Minutes: int64(minutes), AsOf: asOf, Running: !now.Before(start), Teams: []InactiveTeamView{}}
	if !view.Running || asOf.Before(start) {
		return view, nil
	}
	// The moment moves every second; a minute-grained key keeps the cache useful.
	key := fmt.Sprintf("inactive:%s:%d:%d", eventID, minutes, asOf.Unix()/60)
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (InactiveView, error) {
		activity, err := u.store.TeamActivity(ctx, eventID, asOf)
		if err != nil {
			return InactiveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read team activity").Err()
		}
		teams, err := u.store.RankedTeams(ctx, eventID)
		if err != nil {
			return InactiveView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the ranked teams").Err()
		}
		points := map[uuid.UUID]int64{}
		for _, t := range teams {
			points[t.ID] = t.Points
		}
		limit := time.Duration(minutes) * time.Minute
		for _, a := range activity {
			since := start
			if a.LastActivityAt != nil && a.LastActivityAt.After(since) {
				since = *a.LastActivityAt
			}
			if idle := asOf.Sub(since); idle > limit {
				view.Teams = append(view.Teams, InactiveTeamView{
					TeamID: a.TeamID, Name: a.TeamName, LastActivityAt: a.LastActivityAt,
					IdleMinutes: int64(idle / time.Minute), Points: points[a.TeamID],
				})
			}
		}
		sort.SliceStable(view.Teams, func(i, j int) bool { return view.Teams[i].IdleMinutes > view.Teams[j].IdleMinutes })
		return view, nil
	})
}
