package eventAnalytics

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
)

// wrongAnswersLimit is how many distinct wrong answers the drawer lists.
const wrongAnswersLimit = 20

// TaskStore is the «Завдання» and «Прогрес» part of Store.
type TaskStore interface {
	Challenges(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.Challenge, error)
	Challenge(ctx context.Context, eventID, challengeID uuid.UUID) (eventAnalyticsRepo.Challenge, bool, error)
	TaskStats(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.TaskStats, error)
	TaskSeries(ctx context.Context, eventID, challengeID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.SeriesPoint, error)
	TaskFailedTeams(ctx context.Context, eventID, challengeID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.FailedTeam, error)
	TaskWrongAnswers(ctx context.Context, eventID, challengeID uuid.UUID, period eventAnalyticsModel.Period, limit int32) ([]eventAnalyticsRepo.WrongAnswer, error)
	TaskHintEffect(ctx context.Context, eventID, challengeID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.HintEffectRow, error)
	RankedTeams(ctx context.Context, eventID uuid.UUID) ([]eventAnalyticsRepo.RankedTeam, error)
	ScoreEvents(ctx context.Context, eventID uuid.UUID, teamIDs []uuid.UUID) ([]eventAnalyticsRepo.ScoreEvent, error)
	Matrix(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.MatrixCell, error)
	Heatmap(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]eventAnalyticsRepo.HeatCell, error)
	TeamActivity(ctx context.Context, eventID uuid.UUID, asOf time.Time) ([]eventAnalyticsRepo.TeamActivity, error)
}

// reportPeriod reads the event and resolves the report window (nil bounds:
// the event's own window).
func (u *EventAnalyticsUseCase) reportPeriod(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (eventModel.Event, *time.Time, eventAnalyticsModel.Period, error) {
	e, err := u.event(ctx, eventID)
	if err != nil {
		return eventModel.Event{}, nil, eventAnalyticsModel.Period{}, err
	}
	finish := e.Lifecycle.EffectiveFinishAt()
	period, err := eventAnalyticsModel.NewPeriod(from, to, e.Lifecycle.StartAt, finish, u.now())
	if err != nil {
		return eventModel.Event{}, nil, eventAnalyticsModel.Period{}, err
	}
	return e, finish, period, nil
}

func periodKey(period eventAnalyticsModel.Period) string {
	return fmt.Sprintf("%d:%d", period.From.Unix(), period.To.Unix())
}

func periodView(period eventAnalyticsModel.Period) PeriodView {
	return PeriodView{From: period.From, To: period.To}
}

// GetEventAnalyticsTasks is the «Завдання» report (§6.3): the per-task
// table, the difficulty calibration and the group summary.
func (u *EventAnalyticsUseCase) GetEventAnalyticsTasks(ctx context.Context, eventID uuid.UUID, from, to *time.Time) (TasksView, error) {
	_, _, period, err := u.reportPeriod(ctx, eventID, from, to)
	if err != nil {
		return TasksView{}, err
	}
	return cachedReportOf(ctx, u.cache, "tasks:"+eventID.String()+":"+periodKey(period), func(ctx context.Context) (TasksView, error) {
		challenges, err := u.store.Challenges(ctx, eventID)
		if err != nil {
			return TasksView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event challenges").Err()
		}
		stats, err := u.store.TaskStats(ctx, eventID, period)
		if err != nil {
			return TasksView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read task statistics").Err()
		}
		byID := make(map[uuid.UUID]eventAnalyticsRepo.TaskStats, len(stats))
		for _, s := range stats {
			byID[s.ChallengeID] = s
		}
		rows := make([]TaskRowView, 0, len(challenges))
		for _, c := range challenges {
			rows = append(rows, taskRow(c, byID[c.ID]))
		}
		return TasksView{Tasks: rows, Groups: groupRows(rows), Period: periodView(period)}, nil
	})
}

func taskRow(c eventAnalyticsRepo.Challenge, s eventAnalyticsRepo.TaskStats) TaskRowView {
	cal := eventAnalyticsModel.Calibrate(c.Difficulty, s.TeamsTried, s.Solves)
	return TaskRowView{
		ChallengeID: c.ID, Name: c.Name, Difficulty: c.Difficulty, Points: c.Points, GroupID: c.GroupID, GroupName: c.GroupName,
		Attempts: s.Attempts, Correct: s.AttemptsCorrect, TeamsTried: s.TeamsTried, TeamsOpened: s.TeamsOpened, Solves: s.Solves,
		SolveRate:        eventAnalyticsModel.SolveRate(s.TeamsTried, s.Solves),
		MedianSinceStart: s.MedianSinceStart, MedianSinceOpen: s.MedianSinceOpen,
		FirstBloodTeam: s.FirstBloodTeam, FirstBloodAt: s.FirstBloodAt,
		HintsOpened: s.HintsOpened, HintPoints: s.HintPoints,
		Calibration: CalibrationView{Verdict: cal.Verdict, ExpectedMin: cal.Expected.Min, ExpectedMax: cal.Expected.Max},
	}
}

// groupRows sums the task rows per group, in the order the groups first
// appear (board order).
func groupRows(rows []TaskRowView) []GroupRowView {
	index := map[uuid.UUID]int{}
	out := []GroupRowView{}
	for _, r := range rows {
		i, ok := index[r.GroupID]
		if !ok {
			i = len(out)
			index[r.GroupID] = i
			out = append(out, GroupRowView{GroupID: r.GroupID, GroupName: r.GroupName})
		}
		g := &out[i]
		g.Tasks++
		g.Attempts += r.Attempts
		g.TeamsTried += r.TeamsTried
		g.Solves += r.Solves
	}
	for i := range out {
		out[i].SolveRate = eventAnalyticsModel.SolveRate(out[i].TeamsTried, out[i].Solves)
	}
	return out
}

// GetEventAnalyticsTaskDetail is the task drawer: solves over time, the teams
// that tried and failed, and the effect of hints. Wrong answer texts are a
// separate call for the sensitive access.
func (u *EventAnalyticsUseCase) GetEventAnalyticsTaskDetail(ctx context.Context, eventID, challengeID uuid.UUID, from, to *time.Time) (TaskDetailView, error) {
	_, _, period, err := u.reportPeriod(ctx, eventID, from, to)
	if err != nil {
		return TaskDetailView{}, err
	}
	key := "task:" + eventID.String() + ":" + challengeID.String() + ":" + periodKey(period)
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (TaskDetailView, error) {
		challenge, err := u.taskOf(ctx, eventID, challengeID)
		if err != nil {
			return TaskDetailView{}, err
		}
		stats, err := u.store.TaskStats(ctx, eventID, period)
		if err != nil {
			return TaskDetailView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read task statistics").Err()
		}
		var own eventAnalyticsRepo.TaskStats
		for _, s := range stats {
			if s.ChallengeID == challengeID {
				own = s
			}
		}
		points, err := u.store.TaskSeries(ctx, eventID, challengeID, period)
		if err != nil {
			return TaskDetailView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read task activity series").Err()
		}
		failed, err := u.store.TaskFailedTeams(ctx, eventID, challengeID, period)
		if err != nil {
			return TaskDetailView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read teams that failed the task").Err()
		}
		hints, err := u.store.TaskHintEffect(ctx, eventID, challengeID, period)
		if err != nil {
			return TaskDetailView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the hint effect").Err()
		}
		state, err := u.store.RollupState(ctx, eventID)
		if err != nil {
			return TaskDetailView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read event analytics state").Err()
		}
		view := TaskDetailView{
			Task:       taskRow(challenge, own),
			Series:     denseSeries(points, period),
			HintEffect: hintEffect(hints),
			Period:     periodView(period), RefreshedAt: state.RefreshedAt, Final: state.FinalizedAt != nil,
			FailedTeams: make([]FailedTeamView, 0, len(failed)),
		}
		for _, f := range failed {
			view.FailedTeams = append(view.FailedTeams, FailedTeamView{TeamID: f.TeamID, TeamName: f.TeamName, Attempts: f.Attempts, LastAttemptAt: f.LastAttemptAt, HintsOpened: f.HintsOpened})
		}
		return view, nil
	})
}

// GetEventAnalyticsTaskWrongAnswers lists the most common wrong answer texts
// of a task (the sensitive access, §7; the caller enforces it).
func (u *EventAnalyticsUseCase) GetEventAnalyticsTaskWrongAnswers(ctx context.Context, eventID, challengeID uuid.UUID, from, to *time.Time) (WrongAnswersView, error) {
	_, _, period, err := u.reportPeriod(ctx, eventID, from, to)
	if err != nil {
		return WrongAnswersView{}, err
	}
	key := "wrong:" + eventID.String() + ":" + challengeID.String() + ":" + periodKey(period)
	return cachedReportOf(ctx, u.cache, key, func(ctx context.Context) (WrongAnswersView, error) {
		if _, err := u.taskOf(ctx, eventID, challengeID); err != nil {
			return WrongAnswersView{}, err
		}
		answers, err := u.store.TaskWrongAnswers(ctx, eventID, challengeID, period, wrongAnswersLimit)
		if err != nil {
			return WrongAnswersView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read wrong answers").Err()
		}
		view := WrongAnswersView{Answers: make([]WrongAnswerView, 0, len(answers)), Period: periodView(period)}
		for _, a := range answers {
			view.Answers = append(view.Answers, WrongAnswerView{Answer: a.Answer, Attempts: a.Attempts, Teams: a.Teams, LastAt: a.LastAt})
		}
		return view, nil
	})
}

// taskOf reads a published task of the event; a missing one is not found.
func (u *EventAnalyticsUseCase) taskOf(ctx context.Context, eventID, challengeID uuid.UUID) (eventAnalyticsRepo.Challenge, error) {
	c, ok, err := u.store.Challenge(ctx, eventID, challengeID)
	if err != nil {
		return eventAnalyticsRepo.Challenge{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event challenge").Err()
	}
	if !ok {
		return eventAnalyticsRepo.Challenge{}, eventChallengeModel.ErrEventChallengeNotFound.Err()
	}
	return c, nil
}

func hintEffect(rows []eventAnalyticsRepo.HintEffectRow) HintEffectView {
	var with, without []eventAnalyticsRepo.HintEffectRow
	for _, r := range rows {
		if r.Hinted {
			with = append(with, r)
		} else {
			without = append(without, r)
		}
	}
	return HintEffectView{With: hintGroup(with, func(r eventAnalyticsRepo.HintEffectRow) *int64 { return r.SinceHint }),
		Without: hintGroup(without, func(r eventAnalyticsRepo.HintEffectRow) *int64 { return r.SinceStart })}
}

func hintGroup(rows []eventAnalyticsRepo.HintEffectRow, seconds func(eventAnalyticsRepo.HintEffectRow) *int64) HintGroupView {
	g := HintGroupView{Teams: int64(len(rows))}
	var times []int64
	for _, r := range rows {
		if !r.Solved {
			continue
		}
		g.Solved++
		if s := seconds(r); s != nil {
			times = append(times, *s)
		}
	}
	g.SolveRate = eventAnalyticsModel.SolveRate(g.Teams, g.Solved)
	g.MedianSeconds = median(times)
	return g
}

// median is the middle value (the mean of the two middle ones, rounded), nil
// for no values.
func median(values []int64) *int64 {
	if len(values) == 0 {
		return nil
	}
	sorted := append([]int64(nil), values...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	mid := len(sorted) / 2
	m := sorted[mid]
	if len(sorted)%2 == 0 {
		m = (sorted[mid-1] + sorted[mid] + 1) / 2
	}
	return &m
}
