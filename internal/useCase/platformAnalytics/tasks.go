package platformAnalytics

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

const (
	// taskCatalogScan bounds the rows read for one report; the tables show the
	// first taskTableLimit of them.
	taskCatalogScan = 5000
	taskTableLimit  = 200
	// filterMaxLen bounds a category filter; longer values match nothing.
	filterMaxLen = 64
)

// TasksStore is the read port of the tasks section.
type TasksStore interface {
	TaskCatalog(ctx context.Context, from, to time.Time, category, level string, limit int32) ([]platformAnalyticsRepo.CatalogTask, int64, error)
	TaskCategories(ctx context.Context, from, to time.Time) ([]string, error)
}

// taskLevels are the difficulties of a task, easiest first: the options of
// the level filter.
var taskLevels = []exerciseModel.Difficulty{
	exerciseModel.DifficultyElementary, exerciseModel.DifficultyTrivial, exerciseModel.DifficultyEasy, exerciseModel.DifficultyMedium,
	exerciseModel.DifficultyHard, exerciseModel.DifficultyInsane,
}

// GetTasks is the «Каталог завдань» report: how catalog tasks are used and
// how they behave across the events that started in the period. category is an
// exercise tag, level a task difficulty; empty means no filter.
func (u *PlatformAnalyticsUseCase) GetTasks(ctx context.Context, from, to *time.Time, category, level string) (TasksView, error) {
	period, err := u.period(from, to)
	if err != nil {
		return TasksView{}, err
	}
	category, level = strings.TrimSpace(category), strings.TrimSpace(level)
	if len(category) > filterMaxLen {
		return emptyTasksView(period), nil
	}
	key := fmt.Sprintf("tasks:%s:%q:%q", period.Key(), category, level)
	return cached(ctx, u, key, func(ctx context.Context) (TasksView, error) {
		return u.loadTasks(ctx, period, category, level)
	})
}

func (u *PlatformAnalyticsUseCase) loadTasks(ctx context.Context, period platformAnalyticsModel.Period, category, level string) (TasksView, error) {
	categories, err := u.store.TaskCategories(ctx, period.From, period.To)
	if err != nil {
		return TasksView{}, fail(err, "Failed to read the task categories")
	}
	rows, total, err := u.store.TaskCatalog(ctx, period.From, period.To, category, level, taskCatalogScan)
	if err != nil {
		return TasksView{}, fail(err, "Failed to read the task catalog usage")
	}
	view := buildTasksView(period, rows, total)
	view.Categories = categories
	if view.Categories == nil {
		view.Categories = []string{}
	}
	return view, nil
}

func emptyTasksView(period platformAnalyticsModel.Period) TasksView {
	view := buildTasksView(period, nil, 0)
	view.Categories = []string{}
	return view
}

func buildTasksView(period platformAnalyticsModel.Period, rows []platformAnalyticsRepo.CatalogTask, total int64) TasksView {
	view := TasksView{
		Period:     period,
		Levels:     make([]string, 0, len(taskLevels)),
		Tasks:      make([]TaskRowView, 0, min(len(rows), taskTableLimit)),
		TasksTotal: total,
		TasksLimit: taskTableLimit,
		Unsolved:   []TaskRowView{},
		ByCategory: []CategoryUsageView{},
		ByLevel:    make([]LevelSolveView, 0, len(taskLevels)),
		Categories: []string{},
	}
	for _, level := range taskLevels {
		view.Levels = append(view.Levels, string(level))
	}
	perCategory := map[string]*CategoryUsageView{}
	perLevel := map[string]*LevelSolveView{}
	var tried, solved int64
	for _, row := range rows {
		task := taskRowView(row)
		if len(view.Tasks) < taskTableLimit {
			view.Tasks = append(view.Tasks, task)
		}
		if row.Solves == 0 {
			view.UnsolvedTotal++
			if len(view.Unsolved) < taskTableLimit {
				view.Unsolved = append(view.Unsolved, task)
			}
		}
		view.Totals.TasksUsed++
		view.Totals.Uses += row.EventsUsed
		view.Totals.Attempts += row.Attempts
		view.Totals.Solves += row.Solves
		tried += row.TeamsTried
		solved += row.Solves
		for _, tag := range row.Tags {
			c := perCategory[tag]
			if c == nil {
				c = &CategoryUsageView{Category: tag}
				perCategory[tag] = c
			}
			c.Tasks++
			c.Uses += row.EventsUsed
		}
		l := perLevel[row.Difficulty]
		if l == nil {
			l = &LevelSolveView{Level: row.Difficulty}
			perLevel[row.Difficulty] = l
		}
		l.Tasks++
		l.TeamsTried += row.TeamsTried
		l.Solves += row.Solves
	}
	view.Totals.NeverSolved = view.UnsolvedTotal
	view.Totals.SolveRate = eventAnalyticsModel.SolveRate(tried, solved)
	for _, c := range perCategory {
		view.ByCategory = append(view.ByCategory, *c)
	}
	sort.Slice(view.ByCategory, func(i, j int) bool {
		a, b := view.ByCategory[i], view.ByCategory[j]
		if a.Uses != b.Uses {
			return a.Uses > b.Uses
		}
		return a.Category < b.Category
	})
	if len(view.ByCategory) > taskTableLimit {
		view.ByCategory = view.ByCategory[:taskTableLimit]
	}
	for _, level := range taskLevels {
		l, ok := perLevel[string(level)]
		if !ok {
			l = &LevelSolveView{Level: string(level)}
		}
		l.SolveRate = eventAnalyticsModel.SolveRate(l.TeamsTried, l.Solves)
		view.ByLevel = append(view.ByLevel, *l)
	}
	return view
}

func taskRowView(row platformAnalyticsRepo.CatalogTask) TaskRowView {
	tags := row.Tags
	if tags == nil {
		tags = []string{}
	}
	v := TaskRowView{
		ExerciseID: row.ExerciseID, TaskID: row.TaskID, Exercise: row.ExerciseName, Task: row.TaskName,
		Level: row.Difficulty, Categories: tags, EventsUsed: row.EventsUsed, Attempts: row.Attempts,
		TeamsTried: row.TeamsTried, TeamsEngaged: row.TeamsEngaged, Solves: row.Solves, TeamsHinted: row.TeamsHinted,
		SolveRate: eventAnalyticsModel.SolveRate(row.TeamsTried, row.Solves), MedianSolveSeconds: row.MedianSolveSeconds,
		Calibration: eventAnalyticsModel.Calibrate(row.Difficulty, row.TeamsTried, row.Solves).Verdict,
	}
	if row.TeamsEngaged > 0 {
		v.HintRate = float64(row.TeamsHinted) / float64(row.TeamsEngaged)
	}
	return v
}
