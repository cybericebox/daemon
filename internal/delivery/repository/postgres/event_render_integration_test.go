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
	// Republishing identical content is still a successful publication and must
	// create a new identity, including when a newer draft remains pending.
	_, err = db.Pool.Exec(ctx, `UPDATE events SET landing_draft=$2 WHERE id=$1`, e.ID, draft)
	require.NoError(t, err)
	_, err = eventContentRepo.New(db.Queries).PublishLanding(ctx, e.ID, page.Document, now)
	require.NoError(t, err)
	republished, err := uc.GetEventRenderManifest(ctx, e.ID, "", false, now)
	require.NoError(t, err)
	require.NotEqual(t, one.Revision, republished.Revision)
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

func renderJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
