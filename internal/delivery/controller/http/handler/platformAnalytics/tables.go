package platformAnalytics

import (
	"errors"
	"slices"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	utils "github.com/cybericebox/daemon/internal/delivery/controller/http/utils"
	platformAnalyticsUseCase "github.com/cybericebox/daemon/internal/useCase/platformAnalytics"
	"github.com/cybericebox/daemon/pkg/pagination"
)

var errInvalidSort = errors.New("sortBy or sortDir is not valid")

// parseTableQuery reads the search, the sorting and, for a screen table (paged), the page of an analytics table. The
// exports pass paged=false and get everything that matches. It answers 400 itself and reports false then.
func parseTableQuery(ctx *gin.Context, columns []string, paged bool) (platformAnalyticsUseCase.TableQuery, bool) {
	q := platformAnalyticsUseCase.TableQuery{Search: ctx.Query("search"), SortBy: ctx.Query("sortBy"), SortDir: ctx.Query("sortDir")}
	if q.SortBy != "" && !slices.Contains(columns, q.SortBy) || q.SortDir != "" && q.SortDir != "asc" && q.SortDir != "desc" {
		response.AbortWithBadRequest(ctx, errInvalidSort)
		return q, false
	}
	if paged {
		page, err := utils.GetOffsetPaginationParams(ctx)
		if err != nil {
			response.AbortWithBadRequest(ctx, err)
			return q, false
		}
		if err = page.Validate(); err != nil {
			response.AbortWithBadRequest(ctx, err)
			return q, false
		}
		q.Page, q.PageSize = page.Page, page.PageSize
	}
	return q, true
}

// eventsTable godoc
// @Summary Platform analytics: the per-event table (searched, sorted, paged)
// @Description Every event of the period with participants, teams, solves and completion; search over the tag and the name; sortBy start, name, tag, status, participants, teams, solves or teamsSolved. Not cut at the newest N. from/to are RFC 3339.
// @Tags platform-analytics
// @Produce json
// @Param from query string false "period start (RFC 3339); absent = all time"
// @Param to query string false "period end, exclusive (RFC 3339); absent = now"
// @Param search query string false "tag or name"
// @Param sortBy query string false "start | name | tag | status | participants | teams | solves | teamsSolved"
// @Param sortDir query string false "asc | desc"
// @Param page query int true "1-based page"
// @Param pageSize query int false "page size"
// @Success 200 {object} response.Response{data=pagination.OffsetPage[eventRowResponse]}
// @Router /analytics/events/table [get]
func (h *Handler) eventsTable(ctx *gin.Context) {
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	q, ok := parseTableQuery(ctx, platformAnalyticsUseCase.EventSortColumns, true)
	if !ok {
		return
	}
	answer(ctx, from, to, func(from, to *time.Time) (pagination.OffsetPage[eventRowResponse], error) {
		table, err := h.useCase.GetEventsTable(ctx, from, to, q)
		items := make([]eventRowResponse, 0, len(table.Rows))
		for _, row := range table.Rows {
			items = append(items, toEventRowResponse(row))
		}
		return pagination.NewOffsetPage(items, table.Total, q.Page, q.PageSize), err
	})
}

// tasksTable godoc
// @Summary Platform analytics: the task usage table (searched, sorted, paged)
// @Description Every catalog task used in the period (not the first 200), filtered by category and level, searched over the exercise and task names; sortBy exercise, task, level, eventsUsed, attempts, teamsTried, solves, solveRate, medianSolveSeconds or hintRate.
// @Tags platform-analytics
// @Produce json
// @Param from query string false "period start (RFC 3339); absent = all time"
// @Param to query string false "period end, exclusive (RFC 3339); absent = now"
// @Param category query string false "exercise tag"
// @Param level query string false "difficulty"
// @Param search query string false "exercise or task name"
// @Param sortBy query string false "column"
// @Param sortDir query string false "asc | desc"
// @Param page query int true "1-based page"
// @Param pageSize query int false "page size"
// @Success 200 {object} response.Response{data=pagination.OffsetPage[taskRowResponse]}
// @Router /analytics/tasks/table [get]
func (h *Handler) tasksTable(ctx *gin.Context) {
	from, to, ok := parsePeriod(ctx)
	if !ok {
		return
	}
	q, ok := parseTableQuery(ctx, platformAnalyticsUseCase.TaskSortColumns, true)
	if !ok {
		return
	}
	answer(ctx, from, to, func(from, to *time.Time) (pagination.OffsetPage[taskRowResponse], error) {
		table, err := h.useCase.GetTasksTable(ctx, from, to, ctx.Query("category"), ctx.Query("level"), q)
		return pagination.NewOffsetPage(toTaskRows(table.Rows), table.Total, q.Page, q.PageSize), err
	})
}

// usersPeopleTable serves GET /analytics/users/people with page: the whole people table, searched, sorted and paged.
func (h *Handler) usersPeopleTable(ctx *gin.Context) {
	q, ok := parseTableQuery(ctx, platformAnalyticsUseCase.PeopleSortColumns, true)
	if !ok {
		return
	}
	table, err := h.useCase.GetUsersPeoplePage(ctx, q)
	if err != nil {
		response.AbortWithError(ctx, err)
		return
	}
	response.AbortWithData(ctx, pagination.NewOffsetPage(toUsersPeopleResponse(table.Rows), table.Total, q.Page, q.PageSize))
}
