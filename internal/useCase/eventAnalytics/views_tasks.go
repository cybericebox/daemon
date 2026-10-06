package eventAnalytics

import (
	"time"

	"github.com/gofrs/uuid"
)

// Read models of «Завдання» (§6.3) and «Прогрес» (§6.4).

type (
	// TasksView is the per-task table with the difficulty calibration and the
	// group summary.
	TasksView struct {
		Tasks  []TaskRowView
		Groups []GroupRowView
		Period PeriodView
	}

	// TaskRowView is one task over the period. Times are seconds, nil while
	// nobody solved the task. SolveRate is solvers ÷ teams that tried.
	TaskRowView struct {
		ChallengeID uuid.UUID
		Name        string
		Difficulty  string
		Points      int32
		// GroupID is uuid.Nil for a task outside every group.
		GroupID          uuid.UUID
		GroupName        string
		Attempts         int64
		Correct          int64
		TeamsTried       int64
		TeamsOpened      int64
		Solves           int64
		SolveRate        float64
		MedianSinceStart *int64
		MedianSinceOpen  *int64
		FirstBloodTeam   string
		FirstBloodAt     *time.Time
		HintsOpened      int64
		HintPoints       int64
		Calibration      CalibrationView
	}

	// CalibrationView: Verdict is one of the eventAnalyticsModel.Calibration*
	// constants, Expected the solve-rate band of the declared difficulty.
	CalibrationView struct {
		Verdict     string
		ExpectedMin float64
		ExpectedMax float64
	}

	// GroupRowView sums the tasks of a group (uuid.Nil: no group). TeamsTried
	// counts team × task pairs, so SolveRate is the group's average.
	GroupRowView struct {
		GroupID    uuid.UUID
		GroupName  string
		Tasks      int64
		Attempts   int64
		TeamsTried int64
		Solves     int64
		SolveRate  float64
	}

	// TaskDetailView is the task drawer.
	TaskDetailView struct {
		Task        TaskRowView
		Series      []SeriesPointView
		FailedTeams []FailedTeamView
		HintEffect  HintEffectView
		Period      PeriodView
		RefreshedAt *time.Time
		Final       bool
	}

	FailedTeamView struct {
		TeamID        uuid.UUID
		TeamName      string
		Attempts      int64
		LastAttemptAt time.Time
		HintsOpened   int64
	}

	// HintEffectView compares the teams that unlocked a hint before solving
	// (or never solved) with the ones that did not. With's time is measured
	// from the first unlock to the solve, Without's from the team's first
	// open or attempt.
	HintEffectView struct {
		With    HintGroupView
		Without HintGroupView
	}

	HintGroupView struct {
		Teams         int64
		Solved        int64
		SolveRate     float64
		MedianSeconds *int64
	}

	// WrongAnswersView holds the answer texts: only for the sensitive access.
	WrongAnswersView struct {
		Answers []WrongAnswerView
		Period  PeriodView
	}

	WrongAnswerView struct {
		Answer   string
		Attempts int64
		Teams    int64
		LastAt   time.Time
	}

	// ScoresView is the score over time of the selected teams.
	ScoresView struct {
		// Teams lists every ranked team for the picker.
		Teams  []ScoreTeamView
		Series []ScoreSeriesView
		Period PeriodView
	}

	ScoreTeamView struct {
		TeamID uuid.UUID
		Name   string
		Points int64
		Solved int64
		// Rank is the place among the ranked teams (0: hidden or not admitted).
		Rank     int64
		Hidden   bool
		Admitted bool
		Selected bool
	}

	// ScoreSeriesView is a team's running score: a point at the period start,
	// one per score change, one at the period end.
	ScoreSeriesView struct {
		TeamID uuid.UUID
		Name   string
		Points []ScorePointView
	}

	ScorePointView struct {
		At    time.Time
		Score int64
	}

	// MatrixView is the team × task matrix. Only touched cells are listed;
	// the rest are untouched.
	MatrixView struct {
		Tasks  []MatrixTaskView
		Teams  []MatrixTeamView
		Cells  []MatrixCellView
		Period PeriodView
	}

	MatrixTaskView struct {
		ChallengeID uuid.UUID
		Name        string
		GroupName   string
	}

	MatrixTeamView struct {
		TeamID uuid.UUID
		Name   string
		Points int64
		Solved int64
	}

	// MatrixCellView is solved when SolvedAt is set, otherwise tried.
	MatrixCellView struct {
		TeamID      uuid.UUID
		ChallengeID uuid.UUID
		Attempts    int64
		SolvedAt    *time.Time
	}

	// HeatmapView is the hourly activity of the teams. Hours is every hour
	// of the period; Cells are the non-empty team × hour pairs.
	HeatmapView struct {
		Hours       []time.Time
		Teams       []MatrixTeamView
		Cells       []HeatCellView
		MaxActivity int64
		Period      PeriodView
		RefreshedAt *time.Time
		Final       bool
	}

	// HeatCellView: Activity is attempts + opens + solves.
	HeatCellView struct {
		TeamID   uuid.UUID
		HourAt   time.Time
		Attempts int64
		Opens    int64
		Solves   int64
		Activity int64
	}

	// InactiveView lists the ranked teams idle for longer than Minutes as of
	// AsOf (the moment, or the finish once the event is over).
	InactiveView struct {
		Minutes int64
		AsOf    time.Time
		// Running: the event has started.
		Running bool
		Teams   []InactiveTeamView
	}

	InactiveTeamView struct {
		TeamID uuid.UUID
		Name   string
		// LastActivityAt is nil while the team has done nothing.
		LastActivityAt *time.Time
		IdleMinutes    int64
		Points         int64
	}
)
