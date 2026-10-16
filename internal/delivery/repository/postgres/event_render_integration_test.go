package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventContentRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/model/eventContent/testutil"
	"github.com/cybericebox/daemon/internal/testhelpers"
	eventUC "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestEventRenderPublishedProjectionAndRevision(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	e := mustSeedEventForParticipants(t, db, "rendercommon")
	now := time.Now().UTC()
	_, err := db.Pool.Exec(ctx, `UPDATE events SET available_from=$2,archive_at=NULL,lifecycle_configured=true,publish_at=$2,start_at=$3 WHERE id=$1`, e.ID, now.Add(-2*time.Hour), now.Add(-time.Hour))
	require.NoError(t, err)
	_, err = eventConfigRepo.New(db.Queries).Create(ctx, eventConfigModel.NewEventConfig(e.ID, now))
	require.NoError(t, err)
	published := testutil.DocumentWithBinding("intro", "PUBLIC {{event.name}}", "event.name", "text")
	draft := testutil.DocumentWithBinding("intro", "DRAFT {{event.teamCount}}", "event.teamCount", "number")
	_, err = db.Pool.Exec(ctx, `UPDATE events SET landing_document=$2,landing_draft=$3 WHERE id=$1`, e.ID, published, draft)
	require.NoError(t, err)
	uc := eventUC.NewEventUseCase(eventUC.Dependencies{Repo: db.Queries})
	one, err := uc.GetEventRenderManifest(ctx, e.ID, "", false, now)
	require.NoError(t, err)
	page, err := uc.GetEventRenderPage(ctx, e.ID, "", one.Revision, false, now)
	require.NoError(t, err)
	require.Equal(t, e.Name, page.Variables["event.name"])
	require.NotContains(t, page.Variables, "event.teamCount")
	require.NotContains(t, string(renderJSON(t, page)), "DRAFT")
	// A publication that changes no rendered source keeps its cache identity,
	// including when a newer draft remains pending.
	_, err = db.Pool.Exec(ctx, `UPDATE events SET landing_draft=$2 WHERE id=$1`, e.ID, draft)
	require.NoError(t, err)
	_, err = eventContentRepo.New(db.Queries).PublishLanding(ctx, e.ID, page.Document, now)
	require.NoError(t, err)
	republished, err := uc.GetEventRenderManifest(ctx, e.ID, "", false, now)
	require.NoError(t, err)
	require.Equal(t, one.Revision, republished.Revision)
	// The published document itself also contributes to the cache identity.
	next := testutil.Document("intro", "PUBLIC NEW")
	_, err = db.Pool.Exec(ctx, `UPDATE events SET landing_document=$2,landing_draft=NULL WHERE id=$1`, e.ID, next)
	require.NoError(t, err)
	two, err := uc.GetEventRenderManifest(ctx, e.ID, "", false, now)
	require.NoError(t, err)
	require.NotEqual(t, one.Revision, two.Revision)
	_, err = uc.GetEventRenderPage(ctx, e.ID, "", one.Revision, false, now)
	require.ErrorIs(t, err, eventUC.ErrRenderRevisionChanged)
	// Branding is an independent revision input.
	_, err = db.Pool.Exec(ctx, `UPDATE event_configs SET brand_color='#123456' WHERE event_id=$1`, e.ID)
	require.NoError(t, err)
	three, err := uc.GetEventRenderManifest(ctx, e.ID, "", false, now)
	require.NoError(t, err)
	require.NotEqual(t, two.Revision, three.Revision)
	// Published participant-only pages never reach the common renderer, even for preview.
	_, err = db.Pool.Exec(ctx, `INSERT INTO event_pages(id,event_id,slug,title,document,visibility,navigation,navigation_order,published_at,created_at,updated_at) VALUES($1,$2,'restricted','Restricted',$3,1,0,0,$4,$4,$4)`, uuid.Must(uuid.NewV4()), e.ID, published, now)
	require.NoError(t, err)
	_, err = uc.GetEventRenderManifest(ctx, e.ID, "restricted", true, now)
	require.Error(t, err)
	missing, err := db.Queries.GetEventRenderMetadata(ctx, postgres.GetEventRenderMetadataParams{EventID: e.ID, PageSlug: "missing"})
	require.NoError(t, err)
	require.False(t, missing.Allowed)
	// A staff preview reads the same published document and PUBLIC projection.
	_, err = db.Pool.Exec(ctx, `UPDATE events SET lifecycle_configured=false,landing_draft=$2 WHERE id=$1`, e.ID, draft)
	require.NoError(t, err)
	_, err = uc.GetEventRenderManifest(ctx, e.ID, "", false, now)
	require.Error(t, err)
	preview, err := uc.GetEventRenderManifest(ctx, e.ID, "", true, now)
	require.NoError(t, err)
	require.Equal(t, "staff", preview.Audience)
	bundle, err := uc.GetEventRenderPage(ctx, e.ID, "", preview.Revision, true, now)
	require.NoError(t, err)
	require.False(t, strings.Contains(string(renderJSON(t, bundle)), "DRAFT"))
	require.NotContains(t, bundle.Variables, "event.teamCount")
}

// The source version catches published changes, rather than aggregate write
// timestamps. Both SQL read paths must agree so a cache miss can load a bundle.
func TestEventRenderSourceIgnoresDraftAndOperationalWrites(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	e := mustSeedEventForParticipants(t, db, "renderstable")
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	_, err := eventConfigRepo.New(db.Queries).Create(ctx, eventConfigModel.NewEventConfig(e.ID, now))
	require.NoError(t, err)
	document := testutil.Document("intro", "Published")
	pageID := uuid.Must(uuid.NewV4())
	_, err = db.Pool.Exec(ctx, `UPDATE events SET landing_document=$2 WHERE id=$1`, e.ID, document)
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `INSERT INTO event_pages(id,event_id,slug,title,document,visibility,navigation,navigation_order,published_at,created_at,updated_at) VALUES($1,$2,'rules','Rules',$3,0,1,0,$4,$4,$4)`, pageID, e.ID, document, now)
	require.NoError(t, err)
	landing := renderSourceRevision(t, db, e.ID, "")
	page := renderSourceRevision(t, db, e.ID, "rules")
	for _, tc := range []struct{ name, query string }{
		{"event audit timestamp", `UPDATE events SET updated_at=now(),updated_by=created_by WHERE id=$1`},
		{"landing draft", `UPDATE events SET landing_draft='{"version":1,"blocks":[]}' WHERE id=$1`},
		{"live layout publication and draft", `UPDATE events SET live_layout=jsonb_set(live_layout,'{version}','999'),live_layout_draft=live_layout WHERE id=$1`},
		{"internal event name", `UPDATE events SET internal_name='Internal working name' WHERE id=$1`},
		{"unrelated scoring parameter", `UPDATE events SET dynamic_min_points=dynamic_min_points+1 WHERE id=$1`},
		{"config audit timestamp", `UPDATE event_configs SET updated_at=now(),updated_by=NULL WHERE event_id=$1`},
		{"capacity estimate", `UPDATE event_configs SET capacity_expected_dynamic_tasks=1 WHERE event_id=$1`},
		{"participant task setting", `UPDATE event_configs SET hints_disabled=NOT hints_disabled WHERE event_id=$1`},
		{"live results state", `UPDATE event_configs SET results_opened_at=now() WHERE event_id=$1`},
		{"published page draft", `UPDATE event_pages SET draft='{"title":"Draft title"}',updated_at=now() WHERE event_id=$1 AND slug='rules'`},
		{"discard published page draft", `UPDATE event_pages SET draft=NULL,updated_at=now() WHERE event_id=$1 AND slug='rules'`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Pool.Exec(ctx, tc.query, e.ID)
			require.NoError(t, err)
			require.Equal(t, landing, renderSourceRevision(t, db, e.ID, ""))
			require.Equal(t, page, renderSourceRevision(t, db, e.ID, "rules"))
		})
	}
	// Live participant counts are intentionally refreshed outside source HTML.
	user := mustSeedUser(t, db, "renderstable-participant@test.test")
	_, err = db.Pool.Exec(ctx, `INSERT INTO event_participants(event_id,user_id,status,created_at) VALUES($1,$2,0,$3)`, e.ID, user, now)
	require.NoError(t, err)
	require.Equal(t, landing, renderSourceRevision(t, db, e.ID, ""))
	require.Equal(t, page, renderSourceRevision(t, db, e.ID, "rules"))
}

func TestEventRenderSourceTracksPublishedDependencies(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	e := mustSeedEventForParticipants(t, db, "renderchanges")
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	_, err := eventConfigRepo.New(db.Queries).Create(ctx, eventConfigModel.NewEventConfig(e.ID, now))
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `INSERT INTO event_pages(id,event_id,slug,title,document,visibility,navigation,navigation_order,published_at,created_at,updated_at) VALUES($1,$2,'rules','Rules',$3,0,1,0,$4,$4,$4)`, uuid.Must(uuid.NewV4()), e.ID, testutil.Document("intro", "Published"), now)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, query   string
		landing, page bool
	}{
		{"event title", `UPDATE events SET name='Public name' WHERE id=$1`, true, true},
		{"registration start schedule", `UPDATE events SET start_at=start_at+interval '1 hour' WHERE id=$1`, true, true},
		{"publication schedule", `UPDATE events SET publish_at=publish_at+interval '1 minute' WHERE id=$1`, true, true},
		{"finish schedule", `UPDATE events SET finish_at=start_at+interval '2 hours' WHERE id=$1`, true, true},
		{"manual finish", `UPDATE events SET manual_finished_at=start_at+interval '1 hour' WHERE id=$1`, true, true},
		{"withdrawal schedule", `UPDATE events SET withdraw_at=start_at+interval '3 hours' WHERE id=$1`, true, true},
		{"tenant admission", `UPDATE events SET available_from=available_from+interval '1 second' WHERE id=$1`, true, true},
		{"lifecycle admission", `UPDATE events SET lifecycle_configured=NOT lifecycle_configured WHERE id=$1`, true, true},
		{"registration join policy", `UPDATE events SET join_policy=1-join_policy WHERE id=$1`, true, true},
		{"scoring profile variable", `UPDATE events SET scoring_mode=1 WHERE id=$1`, true, true},
		{"registration policy", `UPDATE event_configs SET registration=2 WHERE event_id=$1`, true, true},
		{"registration limits", `UPDATE event_configs SET max_teams=100,min_team_size=2,max_team_size=6 WHERE event_id=$1`, true, true},
		{"public result visibility", `UPDATE event_configs SET scoreboard_visibility=2 WHERE event_id=$1`, true, true},
		{"brand palette", `UPDATE event_configs SET brand_color='#123456' WHERE event_id=$1`, true, true},
		{"preview metadata", `UPDATE event_configs SET preview_description='New preview' WHERE event_id=$1`, true, true},
		{"countdown policy", `UPDATE event_configs SET show_start_countdown=NOT show_start_countdown WHERE event_id=$1`, true, true},
		{"website footer", `UPDATE event_configs SET footer_signature='{"version":1,"blocks":[]}' WHERE event_id=$1`, true, true},
		{"landing document", `UPDATE events SET landing_document='{"version":1,"blocks":[]}' WHERE id=$1`, true, false},
		{"custom page document", `UPDATE event_pages SET document='{"version":1,"blocks":[]}' WHERE event_id=$1 AND slug='rules'`, false, true},
		{"custom page title and navigation", `UPDATE event_pages SET title='New rules',navigation_order=2 WHERE event_id=$1 AND slug='rules'`, false, true},
		{"custom page access", `UPDATE event_pages SET visibility=1 WHERE event_id=$1 AND slug='rules'`, false, true},
		{"custom page publication", `UPDATE event_pages SET published_at=NULL WHERE event_id=$1 AND slug='rules'`, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			landing := renderSourceRevision(t, db, e.ID, "")
			page := renderSourceRevision(t, db, e.ID, "rules")
			_, err := db.Pool.Exec(ctx, tc.query, e.ID)
			require.NoError(t, err)
			require.Equal(t, tc.landing, landing != renderSourceRevision(t, db, e.ID, ""))
			require.Equal(t, tc.page, page != renderSourceRevision(t, db, e.ID, "rules"))
		})
	}
}

func TestEventRenderSourceTracksOnlyRenderedMedia(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	e := mustSeedEventForParticipants(t, db, "rendermedia")
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	_, err := db.Pool.Exec(ctx, `INSERT INTO file_blobs(content_hash,created_at) VALUES('render-image',$1)`, now)
	require.NoError(t, err)
	addImage := func(refType string) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV4())
		_, err := db.Pool.Exec(ctx, `INSERT INTO files(id,name,content_hash,created_at) VALUES($1,'image','render-image',$2)`, id, now)
		require.NoError(t, err)
		_, err = db.Pool.Exec(ctx, `INSERT INTO file_references(ref_type,ref_id,file_id) VALUES($1,$2,$3)`, refType, e.ID, id)
		require.NoError(t, err)
		return id
	}
	for _, refType := range []string{"event_logo", "event_favicon", "event_preview_picture"} {
		before := renderSourceRevision(t, db, e.ID, "")
		addImage(refType)
		require.NotEqual(t, before, renderSourceRevision(t, db, e.ID, ""), refType)
	}
	beforeUpload := renderSourceRevision(t, db, e.ID, "")
	image := addImage("event_content_image")
	require.Equal(t, beforeUpload, renderSourceRevision(t, db, e.ID, ""), "an image uploaded for a draft is not rendered source")
	imageURL := "/api/events/" + e.ID.String() + "/content-images/" + image.String()
	document := map[string]any{"version": 1, "blocks": []any{map[string]any{"type": "banner", "imageURL": imageURL}}}
	_, err = db.Pool.Exec(ctx, `UPDATE events SET landing_document=$2 WHERE id=$1`, e.ID, renderJSON(t, document))
	require.NoError(t, err)
	published := renderSourceRevision(t, db, e.ID, "")
	require.NotEqual(t, beforeUpload, published)
	_, err = db.Pool.Exec(ctx, `DELETE FROM file_references WHERE ref_type='event_content_image' AND ref_id=$1 AND file_id=$2`, e.ID, image)
	require.NoError(t, err)
	require.NotEqual(t, published, renderSourceRevision(t, db, e.ID, ""), "removing a rendered image changes its availability")
}

func TestEventRenderRevisionChangesAtLifecycleBoundaryNotTimerTicks(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	e := mustSeedEventForParticipants(t, db, "rendertimer")
	now := time.Date(2026, 10, 16, 10, 0, 0, 0, time.UTC)
	_, err := eventConfigRepo.New(db.Queries).Create(ctx, eventConfigModel.NewEventConfig(e.ID, now))
	require.NoError(t, err)
	_, err = db.Pool.Exec(ctx, `UPDATE events SET lifecycle_configured=true,publish_at=$2,start_at=$3 WHERE id=$1`, e.ID, now.Add(-time.Hour), now.Add(time.Minute))
	require.NoError(t, err)
	uc := eventUC.NewEventUseCase(eventUC.Dependencies{Repo: db.Queries})
	before, err := uc.GetEventRenderManifest(ctx, e.ID, "", false, now)
	require.NoError(t, err)
	tick, err := uc.GetEventRenderManifest(ctx, e.ID, "", false, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, before.Revision, tick.Revision)
	started, err := uc.GetEventRenderManifest(ctx, e.ID, "", false, now.Add(time.Minute))
	require.NoError(t, err)
	require.NotEqual(t, before.Revision, started.Revision, "the phase hash still prevents caching old lifecycle flags")
}

func renderSourceRevision(t *testing.T, db *testhelpers.TestDB, eventID uuid.UUID, slug string) string {
	t.Helper()
	meta, err := db.Queries.GetEventRenderMetadata(context.Background(), postgres.GetEventRenderMetadataParams{EventID: eventID, PageSlug: slug})
	require.NoError(t, err)
	document, err := db.Queries.GetEventRenderDocument(context.Background(), postgres.GetEventRenderDocumentParams{EventID: eventID, PageSlug: slug})
	require.NoError(t, err)
	require.Equal(t, meta.Revision, document.Revision)
	require.Equal(t, meta.Allowed, document.Allowed)
	return meta.Revision
}

func renderJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
