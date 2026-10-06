package eventContentRepo

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/model/eventContent/testutil"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

func TestRepositoryDecodesSettingsAndWritesStaticPage(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	pageID := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	q := &fakeQueries{
		settings: postgres.GetEventContentSettingsRow{
			ID:              eventID,
			LandingDocument: testutil.Document("intro", "Welcome"),
			LandingDraft:    testutil.Document("draft", "Draft welcome"),
			LiveLayout:      mustLiveJSON(t),
		},
		created: postgres.EventPage{
			ID: pageID, EventID: eventID, Slug: "rules", Title: "Rules",
			Document:   testutil.Document("rules", "Be kind."),
			Visibility: int16(eventContentModel.PageVisibilityPublic), Navigation: int16(eventContentModel.PageNavigationNavbar),
			CreatedAt: now, UpdatedAt: now,
		},
	}
	repo := New(q)

	settings, err := repo.GetSettings(context.Background(), eventID)
	if err != nil {
		t.Fatalf("get settings: %v", err)
	}
	if settings.Live.Grid.Cols != 12 || settings.Landing.Blocks[0].ID != "intro" || settings.LandingDraft == nil || settings.LandingDraft.Blocks[0].ID != "draft" {
		t.Fatalf("unexpected settings: %+v", settings)
	}
	page, err := repo.Create(context.Background(), eventContentModel.Page{
		ID: pageID, EventID: eventID, Slug: "rules", Title: "Rules",
		Document:   eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "rules", Type: eventContentModel.BlockText, RichText: testutil.RichText("Be kind.")}}},
		Visibility: eventContentModel.PageVisibilityPublic, Navigation: eventContentModel.PageNavigationNavbar,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("create page: %v", err)
	}
	if page.Slug != "rules" || q.create.Document == nil || q.create.Visibility != int16(eventContentModel.PageVisibilityPublic) {
		t.Fatalf("unexpected created page/params: page=%+v params=%+v", page, q.create)
	}
	landing := eventContentModel.Document{Blocks: []eventContentModel.Block{{ID: "new-intro", Type: eventContentModel.BlockText, RichText: testutil.RichText("New welcome")}}}
	if affected, err := repo.SaveLandingDraft(context.Background(), eventID, landing); err != nil || affected != 1 || q.landing.EventID != eventID {
		t.Fatalf("save landing draft: affected=%d params=%+v err=%v", affected, q.landing, err)
	}
	if affected, err := repo.PublishLanding(context.Background(), eventID, landing); err != nil || affected != 1 || q.publishLanding.EventID != eventID || !json.Valid(q.publishLanding.LandingDocument) {
		t.Fatalf("publish landing: affected=%d params=%+v err=%v", affected, q.publishLanding, err)
	}
	live := eventContentModel.DefaultLiveLayout()
	if affected, err := repo.SaveLiveDraft(context.Background(), eventID, live); err != nil || affected != 1 || q.live.EventID != eventID {
		t.Fatalf("save live draft: affected=%d params=%+v err=%v", affected, q.live, err)
	}
	q.published = mustLiveJSON(t)
	if published, err := repo.PublishLive(context.Background(), eventID); err != nil || published.Version != 1 {
		t.Fatalf("publish live: %+v %v", published, err)
	}
}

func mustLiveJSON(t *testing.T) []byte {
	t.Helper()
	value, err := json.Marshal(eventContentModel.DefaultLiveLayout())
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestRepositoryReadsUpdatesListsAndDeletesPages(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	pageID := uuid.Must(uuid.NewV7())
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	row := postgres.EventPage{
		ID: pageID, EventID: eventID, Slug: "rules", Title: "Rules",
		Document:   testutil.Document("rules", "Be kind."),
		Visibility: int16(eventContentModel.PageVisibilityParticipant), Navigation: int16(eventContentModel.PageNavigationNavbar),
		NavigationOrder: 2, Draft: []byte(`{"Slug":"rules-v2","Title":"Rules v2","Document":{"blocks":[]},"Visibility":1,"Navigation":1,"NavigationAfter":"first"}`),
		PublishedAt: pgtype.Timestamptz{Time: now, Valid: true}, CreatedAt: now, UpdatedAt: now,
	}
	q := &fakeQueries{bySlug: row, listed: []postgres.EventPage{row}, updated: row}
	repo := New(q)

	bySlug, err := repo.GetBySlug(context.Background(), eventID, "rules")
	if err != nil || bySlug.ID != pageID {
		t.Fatalf("get by slug: page=%+v err=%v", bySlug, err)
	}
	listed, err := repo.List(context.Background(), eventID)
	if err != nil || len(listed) != 1 || listed[0].Navigation != eventContentModel.PageNavigationNavbar ||
		listed[0].PublishedAt == nil || listed[0].Draft == nil || listed[0].Draft.NavigationAfter != "first" || listed[0].EditorSlug() != "rules-v2" {
		t.Fatalf("list pages: pages=%+v err=%v", listed, err)
	}
	updated, err := repo.SaveDraft(context.Background(), eventID, pageID, eventContentModel.PageDraft{
		Slug: "rules", Title: "Updated", Document: bySlug.Document,
		Visibility: eventContentModel.PageVisibilityParticipant, Navigation: eventContentModel.PageNavigationNavbar,
	}, now.Add(time.Minute))
	if err != nil || updated.ID != pageID || q.saveDraft.Title != "Updated" || !json.Valid(q.saveDraft.Draft) {
		t.Fatalf("save page draft: page=%+v params=%+v err=%v", updated, q.saveDraft, err)
	}
	if affected, err := repo.DiscardDraft(context.Background(), eventID, pageID, now); err != nil || affected != 1 || q.discard.ID != pageID {
		t.Fatalf("discard draft: affected=%d params=%+v err=%v", affected, q.discard, err)
	}
	affected, err := repo.Delete(context.Background(), eventID, pageID)
	if err != nil || affected != 1 || q.delete.ID != pageID {
		t.Fatalf("delete page: affected=%d params=%+v err=%v", affected, q.delete, err)
	}
}

type fakeQueries struct {
	settings       postgres.GetEventContentSettingsRow
	created        postgres.EventPage
	create         postgres.CreateEventPageParams
	bySlug         postgres.EventPage
	listed         []postgres.EventPage
	updated        postgres.EventPage
	saveDraft      postgres.SaveEventPageDraftParams
	discard        postgres.DiscardEventPageDraftParams
	delete         postgres.DeleteEventPageParams
	landing        postgres.SaveEventLandingDraftParams
	publishLanding postgres.PublishEventLandingDraftParams
	live           postgres.SaveEventLiveLayoutDraftParams
	published      []byte
}

func (q *fakeQueries) GetEventContentSettings(context.Context, uuid.UUID) (postgres.GetEventContentSettingsRow, error) {
	return q.settings, nil
}

func (q *fakeQueries) CreateEventPage(_ context.Context, arg postgres.CreateEventPageParams) (postgres.EventPage, error) {
	q.create = arg
	return q.created, nil
}

func (q *fakeQueries) GetEventPageBySlug(_ context.Context, _ postgres.GetEventPageBySlugParams) (postgres.EventPage, error) {
	return q.bySlug, nil
}

func (q *fakeQueries) ListEventPages(context.Context, uuid.UUID) ([]postgres.EventPage, error) {
	return q.listed, nil
}

func (q *fakeQueries) SaveEventPageDraft(_ context.Context, arg postgres.SaveEventPageDraftParams) (postgres.EventPage, error) {
	q.saveDraft = arg
	return q.updated, nil
}

func (q *fakeQueries) DiscardEventPageDraft(_ context.Context, arg postgres.DiscardEventPageDraftParams) (int64, error) {
	q.discard = arg
	return 1, nil
}

func (q *fakeQueries) DeleteEventPage(_ context.Context, arg postgres.DeleteEventPageParams) (int64, error) {
	q.delete = arg
	return 1, nil
}

func (q *fakeQueries) SaveEventLandingDraft(_ context.Context, arg postgres.SaveEventLandingDraftParams) (int64, error) {
	q.landing = arg
	return 1, nil
}

func (q *fakeQueries) PublishEventLandingDraft(_ context.Context, arg postgres.PublishEventLandingDraftParams) (int64, error) {
	q.publishLanding = arg
	return 1, nil
}

func (q *fakeQueries) DiscardEventLandingDraft(context.Context, uuid.UUID) (int64, error) {
	return 1, nil
}

func (q *fakeQueries) SaveEventLiveLayoutDraft(_ context.Context, arg postgres.SaveEventLiveLayoutDraftParams) (int64, error) {
	q.live = arg
	return 1, nil
}

func (q *fakeQueries) PublishEventLiveLayout(context.Context, uuid.UUID) ([]byte, error) {
	return q.published, nil
}
