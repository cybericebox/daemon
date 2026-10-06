package eventContentModel

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"
)

func navPage(slug string, order int32, navigation PageNavigation) Page {
	published := time.Date(2026, 9, 29, 10, 0, 0, 0, time.UTC)
	return Page{ID: uuid.NewV5(uuid.Nil, slug), Slug: slug, NavigationOrder: order, Navigation: navigation, PublishedAt: &published}
}

func slugsOf(order NavigationOrder, pages ...Page) []string {
	bySlug := map[uuid.UUID]string{}
	for _, page := range pages {
		bySlug[page.ID] = page.Slug
	}
	out := make([]string, 0, len(order.PageIDs))
	for _, id := range order.PageIDs {
		out = append(out, bySlug[id])
	}
	return out
}

func TestPlaceInNavigationKeepsTheThreeRangesAroundFixedEntries(t *testing.T) {
	// Navbar: intro | «Завдання» | rules | «Результати» | faq, news.
	intro := navPage("intro", -1_000_000_000, PageNavigationNavbar)
	rules := navPage("rules", -1, PageNavigationNavbar)
	faq := navPage("faq", 0, PageNavigationNavbar)
	news := navPage("news", 1, PageNavigationNavbar)
	hidden := navPage("hidden", 5, PageNavigationNone)
	draftOnly := navPage("draft", 6, PageNavigationNavbar)
	draftOnly.PublishedAt = nil
	fresh := navPage("fresh", 0, PageNavigationNone)
	pages := []Page{news, faq, rules, intro, hidden, draftOnly, fresh}

	cases := []struct {
		name       string
		page       Page
		inNavbar   bool
		after      string
		want       []string
		challenges int
		results    int
	}{
		{"first", fresh, true, NavigationAfterFirst, []string{"fresh", "intro", "rules", "faq", "news"}, 2, 3},
		{"after challenges", fresh, true, NavigationAfterChallenges, []string{"intro", "fresh", "rules", "faq", "news"}, 1, 3},
		{"after results", fresh, true, NavigationAfterResults, []string{"intro", "rules", "fresh", "faq", "news"}, 1, 2},
		{"after a page", fresh, true, faq.ID.String(), []string{"intro", "rules", "faq", "fresh", "news"}, 1, 2},
		{"after a before-challenges page", fresh, true, intro.ID.String(), []string{"intro", "fresh", "rules", "faq", "news"}, 2, 3},
		{"new page without placement goes last", fresh, true, "", []string{"intro", "rules", "faq", "news", "fresh"}, 1, 2},
		{"stale placement keeps the old place", rules, true, hidden.ID.String(), []string{"intro", "rules", "faq", "news"}, 1, 2},
		{"moving a page", news, true, NavigationAfterFirst, []string{"news", "intro", "rules", "faq"}, 2, 3},
		{"leaving the navbar", rules, false, NavigationAfterFirst, []string{"intro", "faq", "news"}, 1, 1},
	}
	for _, tc := range cases {
		order := PlaceInNavigation(pages, tc.page.ID, tc.inNavbar, tc.after)
		got := slugsOf(order, pages...)
		if len(got) != len(tc.want) {
			t.Errorf("%s: order = %v, want %v", tc.name, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("%s: order = %v, want %v", tc.name, got, tc.want)
				break
			}
		}
		if order.ChallengesPosition != tc.challenges || order.ResultsPosition != tc.results {
			t.Errorf("%s: positions = %d/%d, want %d/%d", tc.name, order.ChallengesPosition, order.ResultsPosition, tc.challenges, tc.results)
		}
	}
}

func TestPageDraftPlacementIsValidated(t *testing.T) {
	page := navPage("rules", 0, PageNavigationNavbar)
	page.Title = "Rules"
	for _, after := range []string{"", NavigationAfterFirst, NavigationAfterChallenges, NavigationAfterResults, uuid.Must(uuid.NewV7()).String()} {
		page.Draft = &PageDraft{NavigationAfter: after}
		if err := page.Validate(); err != nil {
			t.Errorf("placement %q rejected: %v", after, err)
		}
	}
	page.Draft = &PageDraft{NavigationAfter: "sidebar"}
	if err := page.Validate(); err == nil {
		t.Fatal("unknown placement accepted")
	}
}
