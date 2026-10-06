package postgres_test

import (
	"context"
	"github.com/cybericebox/daemon/internal/model/eventContent/testutil"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventContentRepo"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestEventPageDraftsPublishAndNavigationOrderRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, _ := mustSeedEventForConfig(t, db, "pagesroundtrip")
	repo := eventContentRepo.New(db.Queries)

	// Landing: a saved draft is not public until it is published.
	landing := eventContentModel.Document{Blocks: []eventContentModel.Block{
		{ID: "intro", Type: eventContentModel.BlockText, RichText: testutil.RichText("First")},
		{ID: "details", Type: eventContentModel.BlockText, RichText: testutil.RichText("Second"), Anchor: "details"},
	}}
	if affected, err := repo.SaveLandingDraft(ctx, event.ID, landing); err != nil || affected != 1 {
		t.Fatalf("save landing draft: affected=%d err=%v", affected, err)
	}
	settings, err := repo.GetSettings(ctx, event.ID)
	if err != nil || len(settings.Landing.Blocks) != 0 || settings.LandingDraft == nil || len(settings.LandingDraft.Blocks) != 2 {
		t.Fatalf("landing draft leaked or was lost: settings=%+v err=%v", settings, err)
	}
	if affected, err := repo.PublishLanding(ctx, event.ID, *settings.LandingDraft); err != nil || affected != 1 {
		t.Fatalf("publish landing: affected=%d err=%v", affected, err)
	}
	settings, err = repo.GetSettings(ctx, event.ID)
	if err != nil || settings.LandingDraft != nil || len(settings.Landing.Blocks) != 2 || settings.Landing.Blocks[1].Anchor != "details" {
		t.Fatalf("landing not published: settings=%+v err=%v", settings, err)
	}
	if affected, err := repo.DiscardLandingDraft(ctx, event.ID); err != nil || affected != 0 {
		t.Fatalf("discard without draft: affected=%d err=%v", affected, err)
	}

	newPage := func(slug string) eventContentModel.Page {
		draft := eventContentModel.PageDraft{
			Slug: slug, Title: slug, Visibility: eventContentModel.PageVisibilityPublic, Navigation: eventContentModel.PageNavigationNavbar,
			Document: eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: slug, Type: eventContentModel.BlockText, RichText: testutil.RichText(slug)}}},
		}
		page := eventContentModel.Page{ID: uuid.Must(uuid.NewV7()), EventID: event.ID, Draft: &draft, CreatedAt: ecNow, UpdatedAt: ecNow}
		return page.WithDraftValues()
	}
	publish := func(page eventContentModel.Page, after string) {
		t.Helper()
		pages, err := repo.List(ctx, event.ID)
		if err != nil {
			t.Fatal(err)
		}
		for _, stored := range pages {
			if stored.ID == page.ID {
				page = stored
			}
		}
		order := eventContentModel.PlaceInNavigation(pages, page.ID, page.WithDraftValues().Navigation == eventContentModel.PageNavigationNavbar, after)
		if published, err := repo.Publish(ctx, page, order, page.UpdatedAt.Add(time.Minute)); err != nil || published != 1 {
			t.Fatalf("publish %s: published=%d err=%v", page.Slug, published, err)
		}
	}

	about, faq := newPage("about"), newPage("faq")
	for _, page := range []eventContentModel.Page{about, faq} {
		if err := page.Validate(); err != nil {
			t.Fatalf("invalid page fixture: %v", err)
		}
		created, err := repo.Create(ctx, page)
		if err != nil || created.Published() || created.Draft == nil {
			t.Fatalf("create page %s: page=%+v err=%v", page.Slug, created, err)
		}
	}
	publish(about, "")
	publish(faq, eventContentModel.NavigationAfterFirst)
	pages, err := repo.List(ctx, event.ID)
	if err != nil || len(pages) != 2 || pages[0].Slug != "faq" || pages[1].Slug != "about" || !pages[0].Published() || pages[0].Draft != nil {
		t.Fatalf("publish did not apply order: pages=%+v err=%v", pages, err)
	}

	// A draft of a published page keeps the published columns intact.
	aboutPage := pages[1]
	draft := eventContentModel.PageDraft{Slug: aboutPage.Slug, Title: aboutPage.Title, Document: aboutPage.Document, Visibility: aboutPage.Visibility, Navigation: aboutPage.Navigation}
	draft.Slug, draft.Title, draft.NavigationAfter = "about-us", "About us", eventContentModel.NavigationAfterFirst
	saved, err := repo.SaveDraft(ctx, event.ID, aboutPage.ID, draft, ecNow.Add(time.Hour))
	if err != nil || saved.Slug != "about" || saved.Draft == nil || saved.Draft.Slug != "about-us" {
		t.Fatalf("save draft of a published page: page=%+v err=%v", saved, err)
	}
	stale := aboutPage // UpdatedAt from before the draft was saved
	if published, err := repo.Publish(ctx, stale, eventContentModel.NavigationOrder{}, ecNow.Add(2*time.Hour)); err != nil || published != 0 {
		t.Fatalf("stale publish must be a no-op: published=%d err=%v", published, err)
	}
	publish(saved, eventContentModel.NavigationAfterFirst)
	pages, err = repo.List(ctx, event.ID)
	if err != nil || len(pages) != 2 || pages[0].Slug != "about-us" || pages[0].Title != "About us" || pages[1].Slug != "faq" {
		t.Fatalf("published draft or order missing: pages=%+v err=%v", pages, err)
	}
	if _, err := repo.SaveDraft(ctx, event.ID, pages[1].ID, eventContentModel.PageDraft{Slug: "faq", Title: "FAQ v2", Document: pages[1].Document, Navigation: eventContentModel.PageNavigationNavbar}, ecNow.Add(3*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if affected, err := repo.DiscardDraft(ctx, event.ID, pages[1].ID, ecNow.Add(4*time.Hour)); err != nil || affected != 1 {
		t.Fatalf("discard draft: affected=%d err=%v", affected, err)
	}
}
