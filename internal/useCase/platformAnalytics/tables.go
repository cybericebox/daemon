package platformAnalytics

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
	"github.com/cybericebox/daemon/pkg/pagination"
)

// MaxTableRows is the page size meaning "everything that matches" for the exports (a table the admin reads is
// paged; the CSV of the same filters is not cut).
const MaxTableRows = 1<<31 - 1

// TableQuery is the search, the sorting and the page of an analytics table. Page 0 means everything (the export).
type TableQuery struct {
	Search          string
	SortBy, SortDir string
	Page, PageSize  int
}

func (q TableQuery) limitOffset() (int32, int32) {
	if q.Page <= 0 {
		return MaxTableRows, 0
	}
	size := q.PageSize
	if size <= 0 || size > pagination.MaxPageSize {
		size = pagination.DefaultPageSize
	}
	return int32(size), int32((q.Page - 1) * size)
}

func (q TableQuery) repo() platformAnalyticsRepo.TableQuery {
	limit, offset := q.limitOffset()
	dir := q.SortDir
	if dir != "asc" {
		dir = "desc"
	}
	return platformAnalyticsRepo.TableQuery{Search: strings.TrimSpace(q.Search), SortBy: q.SortBy, SortDir: dir, Limit: limit, Offset: offset}
}

// Sortable columns of the tables.
var (
	PeopleSortColumns = []string{"name", "email", "role", "eventsJoined", "solves", "lastSeen"}
	EventSortColumns  = []string{"start", "name", "tag", "status", "participants", "teams", "solves", "teamsSolved"}
	TaskSortColumns   = []string{"exercise", "task", "level", "eventsUsed", "attempts", "teamsTried", "solves", "solveRate", "medianSolveSeconds", "hintRate"}
)

// pagedEventsStore / pagedPeopleStore are the stores that can filter, sort and page (the repository).
type pagedEventsStore interface {
	EventsPage(ctx context.Context, from, to, asOf time.Time, q platformAnalyticsRepo.TableQuery) ([]platformAnalyticsRepo.EventRow, int64, error)
}

type pagedPeopleStore interface {
	UsersPeoplePage(ctx context.Context, q platformAnalyticsRepo.TableQuery) ([]platformAnalyticsRepo.UsersPerson, int64, error)
}

// EventsTable is one page of the events of the period.
type EventsTable struct {
	Period platformAnalyticsModel.Period
	Rows   []EventRowView
	Total  int64
}

// GetEventsTable is the per-event table of the «Заходи» report: searched, sorted and paged on the server, without a
// cut at the newest N.
func (u *PlatformAnalyticsUseCase) GetEventsTable(ctx context.Context, from, to *time.Time, q TableQuery) (EventsTable, error) {
	period, err := u.period(from, to)
	if err != nil {
		return EventsTable{}, err
	}
	store, ok := u.store.(pagedEventsStore)
	if !ok {
		return EventsTable{}, fail(nil, "The events table is not available")
	}
	rows, total, err := store.EventsPage(ctx, period.From, period.To, u.now(), q.repo())
	if err != nil {
		return EventsTable{}, fail(err, "Failed to read the events")
	}
	out := EventsTable{Period: period, Rows: make([]EventRowView, 0, len(rows)), Total: total}
	for _, row := range rows {
		out.Rows = append(out.Rows, eventRowView(row))
	}
	return out, nil
}

// PeopleTable is one page of the accounts that joined an event.
type PeopleTable struct {
	Rows  []UsersPersonView
	Total int64
}

// GetUsersPeoplePage is the «Люди» table: every account that joined an event, searched, sorted and paged on the
// server. It holds personal data: the caller must hold analytics.users.read.
func (u *PlatformAnalyticsUseCase) GetUsersPeoplePage(ctx context.Context, q TableQuery) (PeopleTable, error) {
	store, ok := u.store.(pagedPeopleStore)
	if !ok {
		return PeopleTable{}, fail(nil, "The people table is not available")
	}
	rows, total, err := store.UsersPeoplePage(ctx, q.repo())
	if err != nil {
		return PeopleTable{}, fail(err, "Failed to read the users")
	}
	out := PeopleTable{Rows: make([]UsersPersonView, 0, len(rows)), Total: total}
	for _, r := range rows {
		out.Rows = append(out.Rows, UsersPersonView{
			ID: r.ID, Name: strings.TrimSpace(r.FirstName + " " + r.LastName), Email: r.Email, Role: r.Role,
			EventsJoined: r.EventsJoined, Solves: r.Solves, LastSeenAt: r.LastSeenAt,
		})
	}
	return out, nil
}

// TasksTable is one page of the catalog tasks used in the period.
type TasksTable struct {
	Period platformAnalyticsModel.Period
	Rows   []TaskRowView
	Total  int64
}

// GetTasksTable is the task usage table: every used task (not the first 200), filtered by category and level, searched
// over the exercise and task names, sorted and paged.
func (u *PlatformAnalyticsUseCase) GetTasksTable(ctx context.Context, from, to *time.Time, category, level string, q TableQuery) (TasksTable, error) {
	period, err := u.period(from, to)
	if err != nil {
		return TasksTable{}, err
	}
	category, level = strings.TrimSpace(category), strings.TrimSpace(level)
	if len(category) > filterMaxLen {
		return TasksTable{Period: period, Rows: []TaskRowView{}}, nil
	}
	rows, _, err := u.store.TaskCatalog(ctx, period.From, period.To, category, level, taskCatalogScan)
	if err != nil {
		return TasksTable{}, fail(err, "Failed to read the task catalog usage")
	}
	needle := strings.ToLower(strings.TrimSpace(q.Search))
	views := make([]TaskRowView, 0, len(rows))
	for _, row := range rows {
		view := taskRowView(row)
		if needle == "" || strings.Contains(strings.ToLower(view.Exercise), needle) || strings.Contains(strings.ToLower(view.Task), needle) {
			views = append(views, view)
		}
	}
	sortTasks(views, q.SortBy, q.SortDir)
	total := int64(len(views))
	if q.Page > 0 {
		limit, offset := q.limitOffset()
		start := min(int(offset), len(views))
		views = views[start:min(start+int(limit), len(views))]
	}
	return TasksTable{Period: period, Rows: views, Total: total}, nil
}

func sortTasks(rows []TaskRowView, by, dir string) {
	less := func(a, b TaskRowView) bool { return a.EventsUsed > b.EventsUsed }
	switch by {
	case "exercise":
		less = func(a, b TaskRowView) bool { return strings.ToLower(a.Exercise) < strings.ToLower(b.Exercise) }
	case "task":
		less = func(a, b TaskRowView) bool { return strings.ToLower(a.Task) < strings.ToLower(b.Task) }
	case "level":
		less = func(a, b TaskRowView) bool { return a.Level < b.Level }
	case "eventsUsed":
		less = func(a, b TaskRowView) bool { return a.EventsUsed < b.EventsUsed }
	case "attempts":
		less = func(a, b TaskRowView) bool { return a.Attempts < b.Attempts }
	case "teamsTried":
		less = func(a, b TaskRowView) bool { return a.TeamsTried < b.TeamsTried }
	case "solves":
		less = func(a, b TaskRowView) bool { return a.Solves < b.Solves }
	case "solveRate":
		less = func(a, b TaskRowView) bool { return a.SolveRate < b.SolveRate }
	case "medianSolveSeconds":
		less = func(a, b TaskRowView) bool { return medianOf(a) < medianOf(b) }
	case "hintRate":
		less = func(a, b TaskRowView) bool { return a.HintRate < b.HintRate }
	default:
		by = ""
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if by == "" {
			return less(rows[i], rows[j]) // the default: the most used first
		}
		if dir == "asc" {
			return less(rows[i], rows[j])
		}
		return less(rows[j], rows[i])
	})
}

// medianOf is the median solve time, -1 while nobody solved the task.
func medianOf(row TaskRowView) int64 {
	if row.MedianSolveSeconds == nil {
		return -1
	}
	return *row.MedianSolveSeconds
}
