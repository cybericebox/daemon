package event

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

type pagePublishRepo struct {
	IRepository
	pages     []postgres.EventPage
	published *postgres.PublishEventPageParams
	result    int64
	saved     *postgres.SaveEventPageDraftParams
}

func (r *pagePublishRepo) ListEventPages(context.Context, uuid.UUID) ([]postgres.EventPage, error) {
	return r.pages, nil
}

func (r *pagePublishRepo) GetEventPageBySlug(_ context.Context, arg postgres.GetEventPageBySlugParams) (postgres.EventPage, error) {
	for _, page := range r.pages {
		if page.Slug == arg.Slug {
			return page, nil
		}
	}
	return postgres.EventPage{}, pgx.ErrNoRows
}

func (r *pagePublishRepo) PublishEventPage(_ context.Context, arg postgres.PublishEventPageParams) (int64, error) {
	r.published = &arg
	return r.result, nil
}

func (r *pagePublishRepo) SaveEventPageDraft(_ context.Context, arg postgres.SaveEventPageDraftParams) (postgres.EventPage, error) {
	r.saved = &arg
	for _, page := range r.pages {
		if page.ID == arg.ID {
			page.Draft = arg.Draft
			return page, nil
		}
	}
	return postgres.EventPage{}, pgx.ErrNoRows
}

func publishedPageRow(eventID uuid.UUID, slug string, order int32, draft string) postgres.EventPage {
	at := time.Date(2026, 9, 29, 9, 0, 0, 0, time.UTC)
	row := postgres.EventPage{
		ID: uuid.NewV5(uuid.Nil, slug), EventID: eventID, Slug: slug, Title: slug, Document: []byte(`{"blocks":[]}`),
		Navigation: 1, NavigationOrder: order, PublishedAt: pgtype.Timestamptz{Time: at, Valid: true}, CreatedAt: at, UpdatedAt: at,
	}
	if draft != "" {
		row.Draft = []byte(draft)
	}
	return row
}

func TestPublishEventPageAppliesDraftAndNavbarOrderTogether(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	rules := publishedPageRow(eventID, "rules", -1, "")
	faq := publishedPageRow(eventID, "faq", 0, `{"Slug":"faq","Title":"Питання","Document":{"blocks":[]},"Visibility":0,"Navigation":1,"NavigationAfter":"first"}`)
	r := &pagePublishRepo{pages: []postgres.EventPage{rules, faq}, result: 1}
	u := NewEventUseCase(Dependencies{Repo: r})

	if _, err := u.PublishEventPage(context.Background(), eventID, faq.ID); err != nil {
		t.Fatalf("publish: %v", err)
	}
	got := r.published
	if got == nil || got.Title != "Питання" || !got.ExpectedUpdatedAt.Equal(faq.UpdatedAt) {
		t.Fatalf("publish params = %+v", got)
	}
	if len(got.PageIDs) != 2 || got.PageIDs[0] != faq.ID || got.PageIDs[1] != rules.ID || got.ChallengesPosition != 1 || got.ResultsPosition != 2 {
		t.Fatalf("navbar order = %v %d/%d", got.PageIDs, got.ChallengesPosition, got.ResultsPosition)
	}
}

func TestPublishEventPageReportsMissingDraftAndConcurrentChange(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	clean := publishedPageRow(eventID, "rules", 0, "")
	edited := publishedPageRow(eventID, "faq", 1, `{"Slug":"faq","Title":"FAQ","Document":{"blocks":[]},"Visibility":0,"Navigation":1}`)
	r := &pagePublishRepo{pages: []postgres.EventPage{clean, edited}}
	u := NewEventUseCase(Dependencies{Repo: r})

	if _, err := u.PublishEventPage(context.Background(), eventID, clean.ID); !errors.Is(err, eventContentModel.ErrPageDraftNotFound.Err()) {
		t.Fatalf("page without draft: %v", err)
	}
	if _, err := u.PublishEventPage(context.Background(), eventID, uuid.Must(uuid.NewV7())); !errors.Is(err, eventContentModel.ErrPageNotFound.Err()) {
		t.Fatalf("missing page: %v", err)
	}
	r.result = 0
	if _, err := u.PublishEventPage(context.Background(), eventID, edited.ID); !errors.Is(err, eventContentModel.ErrPageModified.Err()) {
		t.Fatalf("concurrent change: %v", err)
	}
}

func TestSaveEventPageDraftRejectsAnotherPagesAddress(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	rules := publishedPageRow(eventID, "rules", 0, "")
	faq := publishedPageRow(eventID, "faq", 1, `{"Slug":"help","Title":"FAQ","Document":{"blocks":[]},"Visibility":0,"Navigation":1}`)
	r := &pagePublishRepo{pages: []postgres.EventPage{rules, faq}}
	u := NewEventUseCase(Dependencies{Repo: r})
	input := EventPageInput{Title: "Rules", Document: eventContentModel.Document{Blocks: []eventContentModel.Block{}}, Navigation: eventContentModel.PageNavigationNavbar}

	for _, taken := range []string{"faq", "help"} {
		input.Slug = taken
		if _, err := u.SaveEventPageDraft(context.Background(), eventID, rules.ID, input); !errors.Is(err, eventContentModel.ErrPageDraftSlugTaken.Err()) {
			t.Fatalf("slug %q: %v", taken, err)
		}
	}
	input.Slug, input.NavigationAfter = "rules-2026", eventContentModel.NavigationAfterResults
	view, err := u.SaveEventPageDraft(context.Background(), eventID, rules.ID, input)
	if err != nil || r.saved == nil || r.saved.Slug != "rules-2026" || view.Draft == nil || view.Draft.NavigationAfter != eventContentModel.NavigationAfterResults || view.Slug != "rules" {
		t.Fatalf("save draft: view=%+v params=%+v err=%v", view, r.saved, err)
	}
}

func TestEditorFindsPageByDraftSlugButSiteDoesNot(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	faq := publishedPageRow(eventID, "faq", 1, `{"Slug":"help","Title":"FAQ","Document":{"blocks":[]},"Visibility":0,"Navigation":1}`)
	fresh := publishedPageRow(eventID, "fresh", 2, `{"Slug":"fresh","Title":"New","Document":{"blocks":[]},"Visibility":0,"Navigation":1}`)
	fresh.PublishedAt = pgtype.Timestamptz{}
	r := &pagePublishRepo{pages: []postgres.EventPage{faq, fresh}}
	u := NewEventUseCase(Dependencies{Repo: r})

	if view, err := u.GetEventPage(context.Background(), eventID, "help"); err != nil || view.ID != faq.ID {
		t.Fatalf("editor by draft slug: %+v %v", view, err)
	}
	if _, err := u.getPublishedPage(context.Background(), eventID, "fresh"); !errors.Is(err, eventContentModel.ErrPageNotFound.Err()) {
		t.Fatalf("unpublished page must be hidden on the site: %v", err)
	}
}
