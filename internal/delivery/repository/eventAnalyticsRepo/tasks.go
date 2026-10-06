package eventAnalyticsRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
)

// TaskQueries are the statements of «Завдання» and «Прогрес» (§6.3, §6.4);
// Queries embeds it.
type TaskQueries interface {
	ListEventAnalyticsChallenges(context.Context, postgres.ListEventAnalyticsChallengesParams) ([]postgres.ListEventAnalyticsChallengesRow, error)
	ListEventAnalyticsTaskStats(context.Context, postgres.ListEventAnalyticsTaskStatsParams) ([]postgres.ListEventAnalyticsTaskStatsRow, error)
	ListEventAnalyticsTaskSeries(context.Context, postgres.ListEventAnalyticsTaskSeriesParams) ([]postgres.ListEventAnalyticsTaskSeriesRow, error)
	ListEventAnalyticsTaskFailedTeams(context.Context, postgres.ListEventAnalyticsTaskFailedTeamsParams) ([]postgres.ListEventAnalyticsTaskFailedTeamsRow, error)
	ListEventAnalyticsTaskWrongAnswers(context.Context, postgres.ListEventAnalyticsTaskWrongAnswersParams) ([]postgres.ListEventAnalyticsTaskWrongAnswersRow, error)
	ListEventAnalyticsTaskHintEffect(context.Context, postgres.ListEventAnalyticsTaskHintEffectParams) ([]postgres.ListEventAnalyticsTaskHintEffectRow, error)
	ListEventAnalyticsScoreEvents(context.Context, postgres.ListEventAnalyticsScoreEventsParams) ([]postgres.ListEventAnalyticsScoreEventsRow, error)
	ListEventAnalyticsMatrix(context.Context, postgres.ListEventAnalyticsMatrixParams) ([]postgres.ListEventAnalyticsMatrixRow, error)
	ListEventAnalyticsHeatmap(context.Context, postgres.ListEventAnalyticsHeatmapParams) ([]postgres.ListEventAnalyticsHeatmapRow, error)
	ListEventAnalyticsTeamActivity(context.Context, postgres.ListEventAnalyticsTeamActivityParams) ([]postgres.ListEventAnalyticsTeamActivityRow, error)
	ListManageScoreboard(context.Context, uuid.UUID) ([]postgres.ListManageScoreboardRow, error)
}

type (
	// Challenge is a published task of the event with its declared data.
	Challenge struct {
		ID         uuid.UUID
		Name       string
		Difficulty string
		Points     int32
		// GroupID is uuid.Nil for a task outside every group.
		GroupID   uuid.UUID
		GroupName string
	}

	// TaskStats are the figures of one task over a period. The medians are in
	// seconds, nil while nobody solved it.
	TaskStats struct {
		ChallengeID                                                uuid.UUID
		Attempts, AttemptsCorrect, TeamsTried, TeamsOpened, Solves int64
		MedianSinceStart, MedianSinceOpen                          *int64
		FirstBloodTeam                                             string
		FirstBloodAt                                               *time.Time
		HintsOpened, HintPoints                                    int64
	}

	FailedTeam struct {
		TeamID        uuid.UUID
		TeamName      string
		Attempts      int64
		LastAttemptAt time.Time
		HintsOpened   int64
	}

	WrongAnswer struct {
		Answer   string
		Attempts int64
		Teams    int64
		LastAt   time.Time
	}

	// HintEffectRow is one team that tried a task. The durations are seconds,
	// nil when they do not apply.
	HintEffectRow struct {
		TeamID         uuid.UUID
		Hinted, Solved bool
		SinceHint      *int64
		SinceStart     *int64
	}

	// ScoreEvent is one change of a team's score (a solve, or a hint penalty).
	ScoreEvent struct {
		TeamID uuid.UUID
		At     time.Time
		Points int32
	}

	// RankedTeam is a team of the manage scoreboard.
	RankedTeam struct {
		ID       uuid.UUID
		Name     string
		Points   int64
		Solved   int64
		Hidden   bool
		Admitted bool
	}

	// MatrixCell is a touched team × task cell.
	MatrixCell struct {
		TeamID, ChallengeID uuid.UUID
		Attempts            int64
		SolvedAt            *time.Time
	}

	// HeatCell is one team's activity within an hour.
	HeatCell struct {
		HourAt                  time.Time
		TeamID                  uuid.UUID
		Attempts, Opens, Solves int64
	}

	// TeamActivity is a team's last sign of life (nil: none yet).
	TeamActivity struct {
		TeamID         uuid.UUID
		TeamName       string
		LastActivityAt *time.Time
	}
)

// Challenges lists the published tasks in board order.
func (r *Repository) Challenges(ctx context.Context, eventID uuid.UUID) ([]Challenge, error) {
	return r.challenges(ctx, eventID, uuid.NullUUID{})
}

// Challenge reads one published task; ok is false when the event has none
// with this ID.
func (r *Repository) Challenge(ctx context.Context, eventID, challengeID uuid.UUID) (c Challenge, ok bool, err error) {
	list, err := r.challenges(ctx, eventID, uuid.NullUUID{UUID: challengeID, Valid: true})
	if err != nil || len(list) == 0 {
		return Challenge{}, false, err
	}
	return list[0], true, nil
}

func (r *Repository) challenges(ctx context.Context, eventID uuid.UUID, challengeID uuid.NullUUID) ([]Challenge, error) {
	rows, err := r.q.ListEventAnalyticsChallenges(ctx, postgres.ListEventAnalyticsChallengesParams{EventID: eventID, ChallengeID: challengeID})
	if err != nil {
		return nil, err
	}
	out := make([]Challenge, 0, len(rows))
	for _, row := range rows {
		out = append(out, Challenge{
			ID: row.ChallengeID, Name: row.Name, Difficulty: row.Difficulty, Points: row.Points,
			GroupID: row.GroupID, GroupName: row.GroupName,
		})
	}
	return out, nil
}

// TaskStats reads the per-task figures of the period for every task.
func (r *Repository) TaskStats(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]TaskStats, error) {
	rows, err := r.q.ListEventAnalyticsTaskStats(ctx, postgres.ListEventAnalyticsTaskStatsParams{EventID: eventID, FromAt: period.From, ToAt: period.To})
	if err != nil {
		return nil, err
	}
	out := make([]TaskStats, 0, len(rows))
	for _, row := range rows {
		out = append(out, TaskStats{
			ChallengeID: row.ChallengeID, Attempts: row.Attempts, AttemptsCorrect: row.AttemptsCorrect,
			TeamsTried: row.TeamsTried, TeamsOpened: row.TeamsOpened, Solves: row.Solves,
			MedianSinceStart: secs(row.MedianSinceStart), MedianSinceOpen: secs(row.MedianSinceOpen),
			FirstBloodTeam: row.FirstBloodTeam, FirstBloodAt: timePtr(row.FirstBloodAt),
			HintsOpened: row.HintsOpened, HintPoints: row.HintPoints,
		})
	}
	return out, nil
}

// TaskSeries reads one task's 5-minute buckets with activity.
func (r *Repository) TaskSeries(ctx context.Context, eventID, challengeID uuid.UUID, period eventAnalyticsModel.Period) ([]SeriesPoint, error) {
	rows, err := r.q.ListEventAnalyticsTaskSeries(ctx, postgres.ListEventAnalyticsTaskSeriesParams{
		EventID: eventID, ChallengeID: challengeID, FromAt: period.From, ToAt: period.To,
	})
	if err != nil {
		return nil, err
	}
	out := make([]SeriesPoint, 0, len(rows))
	for _, row := range rows {
		out = append(out, SeriesPoint{At: row.BucketAt, Attempts: row.Attempts, Correct: row.Correct, Solves: row.Solves, Opens: row.Opens})
	}
	return out, nil
}

// TaskFailedTeams lists the teams that tried a task and never solved it.
func (r *Repository) TaskFailedTeams(ctx context.Context, eventID, challengeID uuid.UUID, period eventAnalyticsModel.Period) ([]FailedTeam, error) {
	rows, err := r.q.ListEventAnalyticsTaskFailedTeams(ctx, postgres.ListEventAnalyticsTaskFailedTeamsParams{
		EventID: eventID, ChallengeID: challengeID, FromAt: period.From, ToAt: period.To,
	})
	if err != nil {
		return nil, err
	}
	out := make([]FailedTeam, 0, len(rows))
	for _, row := range rows {
		out = append(out, FailedTeam{TeamID: row.TeamID, TeamName: row.TeamName, Attempts: row.Attempts, LastAttemptAt: row.LastAttemptAt, HintsOpened: row.HintsOpened})
	}
	return out, nil
}

// TaskWrongAnswers lists the most common wrong answer texts of a task.
func (r *Repository) TaskWrongAnswers(ctx context.Context, eventID, challengeID uuid.UUID, period eventAnalyticsModel.Period, limit int32) ([]WrongAnswer, error) {
	rows, err := r.q.ListEventAnalyticsTaskWrongAnswers(ctx, postgres.ListEventAnalyticsTaskWrongAnswersParams{
		EventID: eventID, ChallengeID: challengeID, FromAt: period.From, ToAt: period.To, RowLimit: limit,
	})
	if err != nil {
		return nil, err
	}
	out := make([]WrongAnswer, 0, len(rows))
	for _, row := range rows {
		out = append(out, WrongAnswer{Answer: row.Answer, Attempts: row.Attempts, Teams: row.Teams, LastAt: row.LastAt})
	}
	return out, nil
}

// TaskHintEffect reads one row per team that tried a task.
func (r *Repository) TaskHintEffect(ctx context.Context, eventID, challengeID uuid.UUID, period eventAnalyticsModel.Period) ([]HintEffectRow, error) {
	rows, err := r.q.ListEventAnalyticsTaskHintEffect(ctx, postgres.ListEventAnalyticsTaskHintEffectParams{
		EventID: eventID, ChallengeID: challengeID, FromAt: period.From, ToAt: period.To,
	})
	if err != nil {
		return nil, err
	}
	out := make([]HintEffectRow, 0, len(rows))
	for _, row := range rows {
		out = append(out, HintEffectRow{TeamID: row.TeamID, Hinted: row.Hinted, Solved: row.Solved, SinceHint: secs(row.SinceHintSecs), SinceStart: secs(row.SinceStartSecs)})
	}
	return out, nil
}

// RankedTeams lists every team but the moderators team, ranked as on the
// scoreboard (live).
func (r *Repository) RankedTeams(ctx context.Context, eventID uuid.UUID) ([]RankedTeam, error) {
	rows, err := r.q.ListManageScoreboard(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]RankedTeam, 0, len(rows))
	for _, row := range rows {
		// Analytics never counts the moderators team.
		if row.Moderators {
			continue
		}
		out = append(out, RankedTeam{ID: row.TeamID, Name: row.PublicName, Points: row.Points, Solved: row.Solved, Hidden: row.Hidden, Admitted: row.Admitted})
	}
	return out, nil
}

// ScoreEvents reads every score change of the teams, oldest first.
func (r *Repository) ScoreEvents(ctx context.Context, eventID uuid.UUID, teamIDs []uuid.UUID) ([]ScoreEvent, error) {
	if len(teamIDs) == 0 {
		return nil, nil
	}
	rows, err := r.q.ListEventAnalyticsScoreEvents(ctx, postgres.ListEventAnalyticsScoreEventsParams{EventID: eventID, TeamIds: teamIDs})
	if err != nil {
		return nil, err
	}
	out := make([]ScoreEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, ScoreEvent{TeamID: row.TeamID, At: row.At, Points: row.Points})
	}
	return out, nil
}

// Matrix reads the team × task cells touched in the period.
func (r *Repository) Matrix(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]MatrixCell, error) {
	rows, err := r.q.ListEventAnalyticsMatrix(ctx, postgres.ListEventAnalyticsMatrixParams{EventID: eventID, FromAt: period.From, ToAt: period.To})
	if err != nil {
		return nil, err
	}
	out := make([]MatrixCell, 0, len(rows))
	for _, row := range rows {
		out = append(out, MatrixCell{TeamID: row.TeamID, ChallengeID: row.ChallengeID, Attempts: row.Attempts, SolvedAt: timePtr(row.SolvedAt)})
	}
	return out, nil
}

// Heatmap reads the hourly team activity of the period from the rollup.
func (r *Repository) Heatmap(ctx context.Context, eventID uuid.UUID, period eventAnalyticsModel.Period) ([]HeatCell, error) {
	rows, err := r.q.ListEventAnalyticsHeatmap(ctx, postgres.ListEventAnalyticsHeatmapParams{EventID: eventID, FromAt: period.From, ToAt: period.To})
	if err != nil {
		return nil, err
	}
	out := make([]HeatCell, 0, len(rows))
	for _, row := range rows {
		out = append(out, HeatCell{HourAt: row.HourAt, TeamID: row.TeamID, Attempts: row.Attempts, Opens: row.Opens, Solves: row.Solves})
	}
	return out, nil
}

// TeamActivity reads the last sign of life of every ranked team up to asOf.
func (r *Repository) TeamActivity(ctx context.Context, eventID uuid.UUID, asOf time.Time) ([]TeamActivity, error) {
	rows, err := r.q.ListEventAnalyticsTeamActivity(ctx, postgres.ListEventAnalyticsTeamActivityParams{EventID: eventID, AsOf: asOf})
	if err != nil {
		return nil, err
	}
	out := make([]TeamActivity, 0, len(rows))
	for _, row := range rows {
		a := TeamActivity{TeamID: row.TeamID, TeamName: row.TeamName}
		if row.LastActivityAt.Unix() > 0 {
			at := row.LastActivityAt
			a.LastActivityAt = &at
		}
		out = append(out, a)
	}
	return out, nil
}

// secs maps the query sentinel -1 ("nothing to measure") to nil.
func secs(v int64) *int64 {
	if v < 0 {
		return nil
	}
	return &v
}
