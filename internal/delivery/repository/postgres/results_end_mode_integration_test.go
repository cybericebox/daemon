package postgres_test

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/jackc/pgx/v5/pgconn"
)

func assertResultsEndMode(t *testing.T, settings eventConfigModel.ResultsSettings, want string) {
	t.Helper()
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["FreezeEndMode"]) != `"`+want+`"` {
		t.Errorf("persisted mode=%s, want %s", fields["FreezeEndMode"], want)
	}
}

func TestResultsFreezeEndModeConfigRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventConfigRepo.New(db.Queries)
	ev, actor := mustSeedEventForConfig(t, db, "freezeendmode")
	cfg, err := repo.Create(ctx, eventConfigModel.NewEventConfig(ev.ID, ecNow))
	if err != nil {
		t.Fatal(err)
	}
	assertResultsEndMode(t, cfg.Results, "at_finish")
	settings := cfg.Results
	if err := json.Unmarshal([]byte(`{"FreezeEnabled":true,"FreezeEndMode":"manual","FreezeBannerText":"  Фінальні результати згодом.  ","FreezeBannerLevel":"critical"}`), &settings); err != nil {
		t.Fatal(err)
	}
	if err := cfg.SetResultsSettings(eventConfigModel.VisibilityPublic, settings, ecNow.Add(time.Minute), actor); err != nil {
		t.Fatal(err)
	}
	changed, err := repo.Update(ctx, cfg, ecNow)
	if err != nil || changed != 1 {
		t.Fatalf("update=%d err=%v", changed, err)
	}
	got, err := repo.Get(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	assertResultsEndMode(t, got.Results, "manual")
	assertStoredFreezeBanner(t, got.Results)
	finish := ecNow.Add(-time.Hour)
	if !got.Results.FreezeActive(&finish, ecNow) {
		t.Error("stored manual freeze ended at finish")
	}
	// Creating a config with a manual policy must persist it too.
	manualEvent, manualActor := mustSeedEventForConfig(t, db, "freezeendmanual")
	manual := eventConfigModel.NewEventConfig(manualEvent.ID, ecNow)
	if err := manual.SetResultsSettings(eventConfigModel.VisibilityPublic, settings, ecNow, manualActor); err != nil {
		t.Fatal(err)
	}
	created, err := repo.Create(ctx, manual)
	if err != nil {
		t.Fatal(err)
	}
	assertResultsEndMode(t, created.Results, "manual")
	assertStoredFreezeBanner(t, created.Results)
}

func assertStoredFreezeBanner(t *testing.T, settings eventConfigModel.ResultsSettings) {
	t.Helper()
	body, err := json.Marshal(settings)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(body, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["FreezeBannerText"]) != `"Фінальні результати згодом."` || string(fields["FreezeBannerLevel"]) != `"critical"` {
		t.Errorf("stored banner lost: %s", body)
	}
}

func TestResultsFreezeSettingsMigrationPreservesExistingConfig(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventConfigRepo.New(db.Queries)
	ev, actor := mustSeedEventForConfig(t, db, "freezemigration")
	cfg := eventConfigModel.NewEventConfig(ev.ID, ecNow)
	settings := cfg.Results
	settings.FreezeEnabled, settings.FreezeMinutes = true, 45
	if err := cfg.SetResultsSettings(eventConfigModel.VisibilityPublic, settings, ecNow, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Create(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	// Roll back only the new optional presentation fields, then upgrade the
	// existing row again: the old freeze schedule and audience survive.
	for _, direction := range []string{"down", "up"} {
		script, err := os.ReadFile("migrations/0190_results_freeze_end_mode." + direction + ".sql")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Pool.Exec(ctx, string(script)); err != nil {
			t.Fatalf("%s migration: %v", direction, err)
		}
	}
	got, err := repo.Get(ctx, ev.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Results.FreezeEnabled || got.Results.FreezeMinutes != 45 || got.ScoreboardVisibility != eventConfigModel.VisibilityPublic || got.Results.FreezeEndMode != eventConfigModel.FreezeEndAtFinish || got.Results.FreezeBannerText != "" || got.Results.FreezeBannerLevel != "warning" {
		t.Fatalf("migration lost existing policy/defaults: %+v", got.Results)
	}
	for _, tc := range []struct{ column, value string }{
		{"results_freeze_end_mode", "forever"}, {"results_freeze_banner_level", "danger"},
		{"results_freeze_banner_text", strings.Repeat("я", 281)},
	} {
		_, err := db.Pool.Exec(ctx, "UPDATE event_configs SET "+tc.column+"=$2 WHERE event_id=$1", ev.ID, tc.value)
		var pgErr *pgconn.PgError
		if !errors.As(err, &pgErr) || pgErr.Code != "23514" {
			t.Errorf("%s constraint: %v", tc.column, err)
		}
	}
}
