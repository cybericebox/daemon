package platformAnalytics

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSortTasksByColumnAndDirection(t *testing.T) {
	slow, fast := int64(900), int64(60)
	rows := func() []TaskRowView {
		return []TaskRowView{
			{Task: "b", EventsUsed: 1, Solves: 5, MedianSolveSeconds: &slow},
			{Task: "a", EventsUsed: 3, Solves: 1, MedianSolveSeconds: &fast},
			{Task: "c", EventsUsed: 2, Solves: 9},
		}
	}
	names := func(by, dir string) string {
		r := rows()
		sortTasks(r, by, dir)
		return r[0].Task + r[1].Task + r[2].Task
	}
	require.Equal(t, "acb", names("", ""), "the default: the most used first")
	require.Equal(t, "abc", names("task", "asc"))
	require.Equal(t, "cba", names("task", "desc"))
	require.Equal(t, "cba", names("solves", "desc"))
	require.Equal(t, "cab", names("medianSolveSeconds", "asc"), "no median sorts as -1, first")
}

func TestTableQueryLimitsAndOffsets(t *testing.T) {
	limit, offset := TableQuery{Page: 3, PageSize: 20}.limitOffset()
	require.EqualValues(t, 20, limit)
	require.EqualValues(t, 40, offset)
	limit, offset = TableQuery{}.limitOffset()
	require.EqualValues(t, MaxTableRows, limit, "page 0 is everything (the export)")
	require.Zero(t, offset)
}
