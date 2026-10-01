package eventContentModel

import (
	"sort"

	"github.com/gofrs/uuid"
)

// Navbar placements a draft may request besides "after this page ID". An
// empty placement keeps the page where it is (a page new to the navbar goes
// last).
const (
	NavigationAfterFirst      = "first"
	NavigationAfterChallenges = "challenges"
	NavigationAfterResults    = "results"
)

// The stored order splits the navbar in three ranges around the fixed
// «Завдання» and «Результати» entries: far negative before Challenges,
// negative between the two, nonnegative after Results.
const beforeChallengesLimit = -500_000_000

type navigationGroup int

const (
	groupBeforeChallenges navigationGroup = iota
	groupBeforeResults
	groupAfterResults
)

func groupOf(order int32) navigationGroup {
	switch {
	case order < beforeChallengesLimit:
		return groupBeforeChallenges
	case order < 0:
		return groupBeforeResults
	default:
		return groupAfterResults
	}
}

func validNavigationAfter(after string) bool {
	switch after {
	case "", NavigationAfterFirst, NavigationAfterChallenges, NavigationAfterResults:
		return true
	}
	_, err := uuid.FromString(after)
	return err == nil
}

// NavigationOrder is the navbar order in the shape the storage expects: page
// IDs in display order, how many come before Challenges and how many before
// Results.
type NavigationOrder struct {
	PageIDs            []uuid.UUID
	ChallengesPosition int
	ResultsPosition    int
}

// PlaceInNavigation computes the navbar order after publishing one page.
// pages are the published pages as stored; only those in the navbar (and
// published) take part. The published page is removed from its old place and,
// when inNavbar, inserted according to after. An unknown or stale placement
// (e.g. the referenced page left the navbar) falls back to the page's old
// place, or to the end for a page new to the navbar.
func PlaceInNavigation(pages []Page, pageID uuid.UUID, inNavbar bool, after string) NavigationOrder {
	type entry struct {
		id    uuid.UUID
		group navigationGroup
	}
	current := make([]Page, 0, len(pages))
	for _, page := range pages {
		if page.Published() && page.Navigation != PageNavigationNone {
			current = append(current, page)
		}
	}
	sort.SliceStable(current, func(i, j int) bool {
		if current[i].NavigationOrder == current[j].NavigationOrder {
			return current[i].Slug < current[j].Slug
		}
		return current[i].NavigationOrder < current[j].NavigationOrder
	})
	entries := make([]entry, 0, len(current)+1)
	oldIndex, oldGroup := -1, groupAfterResults
	for _, page := range current {
		if page.ID == pageID {
			oldIndex, oldGroup = len(entries), groupOf(page.NavigationOrder)
			continue
		}
		entries = append(entries, entry{id: page.ID, group: groupOf(page.NavigationOrder)})
	}
	if inNavbar {
		lastIndexOf := func(group navigationGroup) int {
			index := 0
			for position, item := range entries {
				if item.group <= group {
					index = position + 1
				}
			}
			return index
		}
		index, group := len(entries), groupAfterResults
		placed := true
		switch after {
		case NavigationAfterFirst:
			index, group = 0, groupBeforeChallenges
		case NavigationAfterChallenges:
			// Directly after «Завдання»: before the pages already there.
			index, group = lastIndexOf(groupBeforeChallenges), groupBeforeResults
		case NavigationAfterResults:
			index, group = lastIndexOf(groupBeforeResults), groupAfterResults
		case "":
			placed = false
		default:
			placed = false
			if id, err := uuid.FromString(after); err == nil && id != pageID {
				for position, item := range entries {
					if item.id == id {
						index, group, placed = position+1, item.group, true
						break
					}
				}
			}
		}
		if !placed && oldIndex >= 0 {
			index, group = oldIndex, oldGroup
		}
		entries = append(entries, entry{})
		copy(entries[index+1:], entries[index:])
		entries[index] = entry{id: pageID, group: group}
	}
	order := NavigationOrder{PageIDs: make([]uuid.UUID, 0, len(entries))}
	for _, item := range entries {
		order.PageIDs = append(order.PageIDs, item.id)
		if item.group == groupBeforeChallenges {
			order.ChallengesPosition++
		}
		if item.group != groupAfterResults {
			order.ResultsPosition++
		}
	}
	return order
}
