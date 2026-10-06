package postgres_test

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventContentRepo"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestEventLiveLayoutDraftPublishRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, _ := mustSeedEventForConfig(t, db, "liveroundtrip")
	repo := eventContentRepo.New(db.Queries)

	settings, err := repo.GetSettings(ctx, event.ID)
	if err != nil {
		t.Fatalf("read migrated live layout: %v", err)
	}
	if settings.Live.Version != 1 || settings.LiveDraft != nil {
		t.Fatalf("unexpected initial live state: version=%d draft=%v", settings.Live.Version, settings.LiveDraft)
	}

	draft := eventContentModel.DefaultLiveLayout()
	draft.Theme = "light"
	affected, err := repo.SaveLiveDraft(ctx, event.ID, draft)
	if err != nil || affected != 1 {
		t.Fatalf("save live draft: affected=%d err=%v", affected, err)
	}
	settings, err = repo.GetSettings(ctx, event.ID)
	if err != nil {
		t.Fatalf("read live draft: %v", err)
	}
	if settings.Live.Theme != "dark" || settings.LiveDraft == nil || settings.LiveDraft.Theme != "light" {
		t.Fatalf("draft changed published layout or did not persist: published=%q draft=%+v", settings.Live.Theme, settings.LiveDraft)
	}

	published, err := repo.PublishLive(ctx, event.ID)
	if err != nil {
		t.Fatalf("publish live draft: %v", err)
	}
	if published.Version != 2 || published.Theme != "light" {
		t.Fatalf("unexpected published live layout: version=%d theme=%q", published.Version, published.Theme)
	}
	settings, err = repo.GetSettings(ctx, event.ID)
	if err != nil {
		t.Fatalf("read published live layout: %v", err)
	}
	if settings.Live.Version != 2 || settings.Live.Theme != "light" || settings.LiveDraft != nil {
		t.Fatalf("published state did not persist: version=%d theme=%q draft=%v", settings.Live.Version, settings.Live.Theme, settings.LiveDraft)
	}
	if _, err = repo.PublishLive(ctx, event.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Fatalf("publishing without a draft should find no row, got %v", err)
	}
}
