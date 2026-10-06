// Package eventContentRepo maps event page JSON documents to PostgreSQL while
// keeping sqlc row types out of event use cases.
package eventContentRepo

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

type Queries interface {
	GetEventContentSettings(context.Context, uuid.UUID) (postgres.GetEventContentSettingsRow, error)
	SaveEventLandingDraft(context.Context, postgres.SaveEventLandingDraftParams) (int64, error)
	PublishEventLandingDraft(context.Context, postgres.PublishEventLandingDraftParams) (int64, error)
	DiscardEventLandingDraft(context.Context, uuid.UUID) (int64, error)
	SaveEventLiveLayoutDraft(context.Context, postgres.SaveEventLiveLayoutDraftParams) (int64, error)
	PublishEventLiveLayout(context.Context, uuid.UUID) ([]byte, error)
	CreateEventPage(context.Context, postgres.CreateEventPageParams) (postgres.EventPage, error)
	GetEventPageBySlug(context.Context, postgres.GetEventPageBySlugParams) (postgres.EventPage, error)
	ListEventPages(context.Context, uuid.UUID) ([]postgres.EventPage, error)
	SaveEventPageDraft(context.Context, postgres.SaveEventPageDraftParams) (postgres.EventPage, error)
	DiscardEventPageDraft(context.Context, postgres.DiscardEventPageDraftParams) (int64, error)
	DeleteEventPage(context.Context, postgres.DeleteEventPageParams) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) GetSettings(ctx context.Context, eventID uuid.UUID) (eventContentModel.Settings, error) {
	row, err := r.q.GetEventContentSettings(ctx, eventID)
	if err != nil {
		return eventContentModel.Settings{}, err
	}
	var settings eventContentModel.Settings
	if err = json.Unmarshal(row.LandingDocument, &settings.Landing); err != nil {
		return eventContentModel.Settings{}, err
	}
	if len(row.LandingDraft) > 0 {
		var draft eventContentModel.Document
		if err = json.Unmarshal(row.LandingDraft, &draft); err != nil {
			return eventContentModel.Settings{}, err
		}
		settings.LandingDraft = &draft
	}
	if err = json.Unmarshal(row.LiveLayout, &settings.Live); err != nil {
		return eventContentModel.Settings{}, err
	}
	if len(row.LiveLayoutDraft) > 0 {
		var draft eventContentModel.LiveLayout
		if err = json.Unmarshal(row.LiveLayoutDraft, &draft); err != nil {
			return eventContentModel.Settings{}, err
		}
		settings.LiveDraft = &draft
	}
	return settings, settings.ValidateStored()
}

func (r *Repository) SaveLandingDraft(ctx context.Context, eventID uuid.UUID, document eventContentModel.Document) (int64, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return 0, err
	}
	return r.q.SaveEventLandingDraft(ctx, postgres.SaveEventLandingDraftParams{EventID: eventID, LandingDraft: encoded})
}

// PublishLanding makes the validated draft document the published landing.
// A draft saved after it was read stays pending (see the query).
func (r *Repository) PublishLanding(ctx context.Context, eventID uuid.UUID, document eventContentModel.Document) (int64, error) {
	encoded, err := json.Marshal(document)
	if err != nil {
		return 0, err
	}
	return r.q.PublishEventLandingDraft(ctx, postgres.PublishEventLandingDraftParams{EventID: eventID, LandingDocument: encoded})
}

func (r *Repository) DiscardLandingDraft(ctx context.Context, eventID uuid.UUID) (int64, error) {
	return r.q.DiscardEventLandingDraft(ctx, eventID)
}

func (r *Repository) SaveLiveDraft(ctx context.Context, eventID uuid.UUID, layout eventContentModel.LiveLayout) (int64, error) {
	encoded, err := json.Marshal(layout)
	if err != nil {
		return 0, err
	}
	return r.q.SaveEventLiveLayoutDraft(ctx, postgres.SaveEventLiveLayoutDraftParams{
		EventID: eventID, LiveLayoutDraft: encoded,
	})
}

func (r *Repository) PublishLive(ctx context.Context, eventID uuid.UUID) (eventContentModel.LiveLayout, error) {
	encoded, err := r.q.PublishEventLiveLayout(ctx, eventID)
	if err != nil {
		return eventContentModel.LiveLayout{}, err
	}
	var layout eventContentModel.LiveLayout
	if err := json.Unmarshal(encoded, &layout); err != nil {
		return eventContentModel.LiveLayout{}, err
	}
	return layout, layout.ValidateStored()
}

// Create stores a new, unpublished page: its columns mirror its draft.
func (r *Repository) Create(ctx context.Context, page eventContentModel.Page) (eventContentModel.Page, error) {
	document, err := json.Marshal(page.Document)
	if err != nil {
		return eventContentModel.Page{}, err
	}
	draft, err := json.Marshal(page.Draft)
	if err != nil {
		return eventContentModel.Page{}, err
	}
	row, err := r.q.CreateEventPage(ctx, postgres.CreateEventPageParams{
		ID: page.ID, EventID: page.EventID, Slug: page.Slug, Title: page.Title,
		Document: document, Visibility: int16(page.Visibility), Navigation: int16(page.Navigation),
		NavigationOrder: page.NavigationOrder, Draft: draft, CreatedAt: page.CreatedAt, UpdatedAt: page.UpdatedAt,
	})
	if err != nil {
		return eventContentModel.Page{}, err
	}
	return pageFromRow(row)
}

func (r *Repository) GetBySlug(ctx context.Context, eventID uuid.UUID, slug string) (eventContentModel.Page, error) {
	row, err := r.q.GetEventPageBySlug(ctx, postgres.GetEventPageBySlugParams{EventID: eventID, Slug: slug})
	if err != nil {
		return eventContentModel.Page{}, err
	}
	return pageFromRow(row)
}

func (r *Repository) List(ctx context.Context, eventID uuid.UUID) ([]eventContentModel.Page, error) {
	rows, err := r.q.ListEventPages(ctx, eventID)
	if err != nil {
		return nil, err
	}
	pages := make([]eventContentModel.Page, 0, len(rows))
	for _, row := range rows {
		page, mapErr := pageFromRow(row)
		if mapErr != nil {
			return nil, mapErr
		}
		pages = append(pages, page)
	}
	return pages, nil
}

// SaveDraft stores the page draft; a never-published page also mirrors it in
// its columns (see the query).
func (r *Repository) SaveDraft(ctx context.Context, eventID, pageID uuid.UUID, draft eventContentModel.PageDraft, now time.Time) (eventContentModel.Page, error) {
	encodedDraft, err := json.Marshal(draft)
	if err != nil {
		return eventContentModel.Page{}, err
	}
	document, err := json.Marshal(draft.Document)
	if err != nil {
		return eventContentModel.Page{}, err
	}
	row, err := r.q.SaveEventPageDraft(ctx, postgres.SaveEventPageDraftParams{
		Draft: encodedDraft, Slug: draft.Slug, Title: draft.Title, Document: document,
		Visibility: int16(draft.Visibility), Navigation: int16(draft.Navigation), UpdatedAt: now,
		ID: pageID, EventID: eventID,
	})
	if err != nil {
		return eventContentModel.Page{}, err
	}
	return pageFromRow(row)
}

func (r *Repository) DiscardDraft(ctx context.Context, eventID, pageID uuid.UUID, now time.Time) (int64, error) {
	return r.q.DiscardEventPageDraft(ctx, postgres.DiscardEventPageDraftParams{ID: pageID, EventID: eventID, UpdatedAt: now})
}

// Publish promotes the page draft and applies the navbar order in one
// statement, guarded by the page's UpdatedAt as read. It returns the number of
// pages published (0 or 1).
func (r *Repository) Publish(ctx context.Context, page eventContentModel.Page, order eventContentModel.NavigationOrder, now time.Time) (int64, error) {
	publisher, ok := r.q.(interface {
		PublishEventPage(context.Context, postgres.PublishEventPageParams) (int64, error)
	})
	if !ok {
		return 0, errors.New("event page publish query is unavailable")
	}
	published := page.WithDraftValues()
	document, err := json.Marshal(published.Document)
	if err != nil {
		return 0, err
	}
	return publisher.PublishEventPage(ctx, postgres.PublishEventPageParams{
		ID: page.ID, EventID: page.EventID, ExpectedUpdatedAt: page.UpdatedAt,
		Slug: published.Slug, Title: published.Title, Document: document,
		Visibility: int16(published.Visibility), Navigation: int16(published.Navigation),
		PageIDs: order.PageIDs, ChallengesPosition: order.ChallengesPosition, ResultsPosition: order.ResultsPosition,
		Now: now,
	})
}

func (r *Repository) Delete(ctx context.Context, eventID, pageID uuid.UUID) (int64, error) {
	return r.q.DeleteEventPage(ctx, postgres.DeleteEventPageParams{ID: pageID, EventID: eventID})
}

func pageFromRow(row postgres.EventPage) (eventContentModel.Page, error) {
	var document eventContentModel.Document
	if err := json.Unmarshal(row.Document, &document); err != nil {
		return eventContentModel.Page{}, err
	}
	page := eventContentModel.Page{
		ID: row.ID, EventID: row.EventID, Slug: row.Slug, Title: row.Title, Document: document,
		Visibility: eventContentModel.PageVisibility(row.Visibility), Navigation: eventContentModel.PageNavigation(row.Navigation),
		NavigationOrder: row.NavigationOrder, PublishedAt: timePtr(row.PublishedAt), CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if len(row.Draft) > 0 && string(row.Draft) != "null" {
		var draft eventContentModel.PageDraft
		if err := json.Unmarshal(row.Draft, &draft); err != nil {
			return eventContentModel.Page{}, err
		}
		page.Draft = &draft
	}
	return page, nil
}

func timePtr(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	at := value.Time
	return &at
}
