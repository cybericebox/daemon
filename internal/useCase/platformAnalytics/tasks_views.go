package platformAnalytics

import (
	"github.com/gofrs/uuid"

	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

type (
	// TasksView is the «Каталог завдань» report. Categories and Levels are
	// the options of the two filters.
	TasksView struct {
		Period     platformAnalyticsModel.Period
		Totals     TasksTotals
		Categories []string
		Levels     []string
		// Tasks are the most used tasks (at most TasksLimit); TasksTotal is
		// how many tasks match the filters in all.
		Tasks      []TaskRowView
		TasksTotal int64
		TasksLimit int
		// Unsolved are the used tasks nobody solved, most used first (at most
		// TasksLimit); UnsolvedTotal is how many there are.
		Unsolved      []TaskRowView
		UnsolvedTotal int64
		ByCategory    []CategoryUsageView
		ByLevel       []LevelSolveView
	}

	// TasksTotals sum the matching tasks: Uses is the number of (task, event)
	// uses, SolveRate is solves ÷ team-task pairs that tried (0..1).
	TasksTotals struct {
		TasksUsed, Uses, Attempts, Solves, NeverSolved int64
		SolveRate                                      float64
	}

	// TaskRowView is one catalog task across the events of the period.
	// SolveRate is solves ÷ teams that tried it (0..1); HintRate is the teams
	// that unlocked a hint ÷ the teams that opened or tried it (0..1);
	// MedianSolveSeconds is nil while nobody solved it; Calibration is the
	// verdict of the event calibration (ok, too_easy, too_hard,
	// insufficient, unknown) of the pooled numbers.
	TaskRowView struct {
		ExerciseID, TaskID                uuid.UUID
		Exercise, Task, Level             string
		Categories                        []string
		EventsUsed, Attempts, TeamsTried  int64
		TeamsEngaged, Solves, TeamsHinted int64
		SolveRate, HintRate               float64
		MedianSolveSeconds                *int64
		Calibration                       string
	}

	// CategoryUsageView: Tasks are the tasks with this category, Uses the
	// (task, event) uses.
	CategoryUsageView struct {
		Category    string
		Tasks, Uses int64
	}

	LevelSolveView struct {
		Level                     string
		Tasks, TeamsTried, Solves int64
		SolveRate                 float64
	}
)
