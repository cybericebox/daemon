package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func standWith(name string, known bool, cpu, memory int64) PlatformStandView {
	view := PlatformStandView{TeamName: name}
	view.Resources.Known = known
	view.Resources.CPUMillicores = cpu
	view.Resources.MemoryBytes = memory
	return view
}

func names(views []PlatformStandView) []string {
	out := make([]string, 0, len(views))
	for _, v := range views {
		out = append(out, v.TeamName)
	}
	return out
}

// Stands sort by the CPU or memory their labs use now; a stand nobody observed is last in both directions.
func TestPageOfStandsByResources(t *testing.T) {
	all := func() []PlatformStandView {
		return []PlatformStandView{
			standWith("unknown", false, 0, 0),
			standWith("small", true, 100, 900),
			standWith("big", true, 900, 100),
			standWith("mid", true, 500, 500),
		}
	}
	require.Equal(t, []string{"big", "mid", "small", "unknown"},
		names(pageOfStandsByResources(all(), StandsFilter{SortBy: StandSortCPU, SortDir: "desc", Page: 1, PageSize: 10})))
	require.Equal(t, []string{"small", "mid", "big", "unknown"},
		names(pageOfStandsByResources(all(), StandsFilter{SortBy: StandSortCPU, SortDir: "asc", Page: 1, PageSize: 10})))
	require.Equal(t, []string{"small", "mid", "big", "unknown"},
		names(pageOfStandsByResources(all(), StandsFilter{SortBy: StandSortMemory, Page: 1, PageSize: 10})))
	require.Equal(t, []string{"small", "mid"}, names(pageOfStandsByResources(all(), StandsFilter{SortBy: StandSortMemory, SortDir: "desc", Page: 1, PageSize: 2})),
		"memory descending, first page of two")
	require.Equal(t, []string{"big", "unknown"}, names(pageOfStandsByResources(all(), StandsFilter{SortBy: StandSortMemory, SortDir: "desc", Page: 2, PageSize: 2})))
	require.Empty(t, pageOfStandsByResources(all(), StandsFilter{SortBy: StandSortCPU, Page: 9, PageSize: 10}), "a page past the end is empty")
}
