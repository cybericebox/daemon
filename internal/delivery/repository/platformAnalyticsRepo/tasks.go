package platformAnalyticsRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

// TasksQueries are the sqlc statements of the tasks section.
type TasksQueries interface {
	ListPlatformAnalyticsTaskCatalog(context.Context, postgres.ListPlatformAnalyticsTaskCatalogParams) ([]postgres.ListPlatformAnalyticsTaskCatalogRow, error)
	ListPlatformAnalyticsTaskCategories(context.Context, postgres.ListPlatformAnalyticsTaskCategoriesParams) ([]string, error)
}

// CatalogTask is one catalog task with its use across the events of the
// period. MedianSolveSeconds is nil while nobody solved it.
type CatalogTask struct {
	ExerciseID, TaskID                             uuid.UUID
	ExerciseName, TaskName, Difficulty             string
	Tags                                           []string
	EventsUsed, Attempts, TeamsTried, TeamsEngaged int64
	Solves, TeamsHinted                            int64
	MedianSolveSeconds                             *int64
}

// TaskCatalog reads the catalog tasks used by the events that start in
// [from, to), most used first (at most limit) and the size of the whole
// result. An empty category or level means no filter.
func (r *Repository) TaskCatalog(ctx context.Context, from, to time.Time, category, level string, limit int32) (rows []CatalogTask, total int64, err error) {
	list, err := r.q.ListPlatformAnalyticsTaskCatalog(ctx, postgres.ListPlatformAnalyticsTaskCatalogParams{
		FromAt: from, ToAt: to, Category: taskFilterText(category), Level: taskFilterText(level), RowLimit: limit,
	})
	if err != nil {
		return nil, 0, err
	}
	rows = make([]CatalogTask, 0, len(list))
	for _, row := range list {
		total = row.Total
		task := CatalogTask{
			ExerciseID: row.ExerciseID, TaskID: row.TaskID, ExerciseName: row.ExerciseName, TaskName: row.TaskName,
			Difficulty: row.Difficulty, Tags: row.Tags, EventsUsed: row.EventsUsed, Attempts: row.Attempts,
			TeamsTried: row.TeamsTried, TeamsEngaged: row.TeamsEngaged, Solves: row.Solves, TeamsHinted: row.TeamsHinted,
		}
		if row.MedianSolveSecs >= 0 {
			secs := row.MedianSolveSecs
			task.MedianSolveSeconds = &secs
		}
		rows = append(rows, task)
	}
	return rows, total, nil
}

// TaskCategories lists the categories (exercise tags) of the tasks used in
// the period.
func (r *Repository) TaskCategories(ctx context.Context, from, to time.Time) ([]string, error) {
	return r.q.ListPlatformAnalyticsTaskCategories(ctx, postgres.ListPlatformAnalyticsTaskCategoriesParams{FromAt: from, ToAt: to})
}

func taskFilterText(value string) pgtype.Text { return pgtype.Text{String: value, Valid: value != ""} }
