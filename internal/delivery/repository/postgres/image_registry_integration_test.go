package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/imageRegistryRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestImageRegistrySettingsSingletonRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := imageRegistryRepo.New(db.Queries)
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)

	if _, err := repo.Get(ctx); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("empty table should be not found, got %v", err)
	}

	first, err := imageRegistryModel.NewSettings("dockerhub", "acme", "acme/platform", t0)
	if err != nil {
		t.Fatal(err)
	}
	first.SetTokens("sealed-push", "sealed-pull", t0)
	if err := repo.Save(ctx, first); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := repo.Get(ctx)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Provider != imageRegistryModel.ProviderDockerHub || got.Account != "acme" || got.Repository != "acme/platform" ||
		got.PushTokenCiphertext != "sealed-push" || got.PullTokenCiphertext != "sealed-pull" || !got.UpdatedAt.Equal(t0) {
		t.Fatalf("round trip: %+v", got)
	}

	// A second save replaces the one row.
	t1 := t0.Add(time.Hour)
	second, _ := imageRegistryModel.NewSettings("ghcr", "octo", "octo/tasks", t1)
	second.SetTokens("sealed-push-2", "", t1)
	if err := repo.Save(ctx, second); err != nil {
		t.Fatalf("second save: %v", err)
	}
	got, err = repo.Get(ctx)
	if err != nil {
		t.Fatalf("get after replace: %v", err)
	}
	if got.Provider != imageRegistryModel.ProviderGHCR || got.Repository != "octo/tasks" ||
		got.PushTokenCiphertext != "sealed-push-2" || got.PullTokenCiphertext != "" || !got.UpdatedAt.Equal(t1) {
		t.Fatalf("replace: %+v", got)
	}
	var rows int
	if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM image_registry_settings").Scan(&rows); err != nil || rows != 1 {
		t.Fatalf("singleton rows=%d err=%v", rows, err)
	}

	if err := repo.Delete(ctx); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := repo.Get(ctx); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("deleted settings should be not found, got %v", err)
	}
}

// The periodic check result is a narrow write guarded by updated_at: a result for settings that were replaced
// meanwhile is dropped, and a whole-row save keeps the checks it is given.
func TestImageRegistryChecksAreGuardedByTheSettingsVersion(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := imageRegistryRepo.New(db.Queries)
	t0 := time.Date(2026, 10, 18, 9, 0, 0, 0, time.UTC)

	s, _ := imageRegistryModel.NewSettings("ghcr", "acme", "acme/platform", t0)
	s.SetTokens("sealed-push", "sealed-pull", t0)
	s.Verified(true, t0)
	if err := repo.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx)
	if err != nil || got.PushCheck.State != imageRegistryModel.CheckOK || !got.PushCheck.CheckedAt.Equal(t0) || got.PullCheck.State != imageRegistryModel.CheckOK {
		t.Fatalf("saved checks: %+v err %v", got, err)
	}

	broken := imageRegistryModel.Check{State: imageRegistryModel.CheckBroken, CheckedAt: t0.Add(time.Hour), Reason: imageRegistryModel.ReasonUnauthorized, AttemptedAt: t0.Add(time.Hour)}
	if stored, err := repo.SaveChecks(ctx, t0, broken, imageRegistryModel.Check{}); err != nil || !stored {
		t.Fatalf("save checks: %v %v", stored, err)
	}
	got, _ = repo.Get(ctx)
	if got.PushCheck != broken || got.PullCheck != (imageRegistryModel.Check{}) || got.PushTokenCiphertext != "sealed-push" {
		t.Fatalf("after save checks: %+v", got)
	}

	// the settings are saved again (tokens replaced): a result for the old version is dropped
	s.SetTokens("sealed-push-2", "", t0.Add(2*time.Hour))
	s.Verified(false, t0.Add(2*time.Hour))
	if err := repo.Save(ctx, s); err != nil {
		t.Fatal(err)
	}
	if stored, err := repo.SaveChecks(ctx, t0, broken, broken); err != nil || stored {
		t.Fatalf("stale result stored: %v %v", stored, err)
	}
	got, _ = repo.Get(ctx)
	if got.PushCheck.State != imageRegistryModel.CheckOK {
		t.Fatalf("stale result overwrote the fresh one: %+v", got.PushCheck)
	}
}

func TestEventSecretCheckListingAndGuard(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	secrets := imageRegistryRepo.NewEventSecrets(db.Queries)
	live := mustSeedEventForParticipants(t, db, "regchecklive")
	finished := mustSeedEventForParticipants(t, db, "regcheckfinished")
	// the second event finishes (and is withdrawn) before the first one's window ends
	rtExec(t, db, `UPDATE events SET finish_at = $2::timestamptz, withdraw_at = $2::timestamptz + interval '1 hour' WHERE id = $1`, finished.ID, epNow.Add(5*24*time.Hour))

	mk := func(eventID uuid.UUID, host string) imageRegistryModel.EventSecret {
		s, err := imageRegistryModel.NewEventSecret(eventID.String(), host, "", "bob", epNow)
		if err != nil {
			t.Fatal(err)
		}
		s.SetToken("sealed-"+host, epNow)
		if err := secrets.Create(ctx, s); err != nil {
			t.Fatal(err)
		}
		return s
	}
	a, b := mk(live.ID, "a.example.org"), mk(finished.ID, "b.example.org")

	// a new secret was never checked
	got, err := secrets.Get(ctx, live.ID, a.ID)
	if err != nil || got.Check != (imageRegistryModel.Check{}) {
		t.Fatalf("new secret: %+v %v", got.Check, err)
	}

	hosts := func(now time.Time) []string {
		items, err := secrets.ListToCheck(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		var out []string
		for _, it := range items {
			out = append(out, it.Secret.Host+"@"+it.EventTag)
		}
		return out
	}
	if got := hosts(epNow.Add(24 * time.Hour)); len(got) != 2 {
		t.Fatalf("both events are live: %v", got)
	}
	if got := hosts(epNow.Add(6 * 24 * time.Hour)); len(got) != 1 || got[0] != "a.example.org@regchecklive" {
		t.Fatalf("a finished event is skipped: %v", got)
	}
	if got := hosts(epNow.Add(31 * 24 * time.Hour)); len(got) != 0 {
		t.Fatalf("an archived event is skipped: %v", got)
	}
	// a broken secret of an ended event is listed for closing, one of a live event is not
	for _, sec := range []imageRegistryModel.EventSecret{a, b} {
		sec.Check = imageRegistryModel.Check{State: imageRegistryModel.CheckBroken, CheckedAt: epNow, Reason: imageRegistryModel.ReasonUnauthorized}
		if stored, err := secrets.SaveCheck(ctx, sec); err != nil || !stored {
			t.Fatal(err)
		}
	}
	if ended, err := secrets.ListEndedBroken(ctx, epNow.Add(6*24*time.Hour)); err != nil || len(ended) != 1 || ended[0].ID != b.ID {
		t.Fatalf("ended broken: %+v %v", ended, err)
	}
	a.Check = imageRegistryModel.Check{}

	// the result is written while the secret is as it was checked, and dropped after an edit
	check := imageRegistryModel.Check{State: imageRegistryModel.CheckBroken, CheckedAt: epNow.Add(time.Hour), Reason: imageRegistryModel.ReasonUnauthorized, AttemptedAt: epNow.Add(time.Hour)}
	a.Check = check
	if stored, err := secrets.SaveCheck(ctx, a); err != nil || !stored {
		t.Fatalf("save check: %v %v", stored, err)
	}
	got, _ = secrets.Get(ctx, live.ID, a.ID)
	if got.Check != check {
		t.Fatalf("stored check: %+v", got.Check)
	}
	// a whole-row edit keeps the check and moves updated_at
	edited := got
	if err := edited.Edit("a.example.org", "", "alice", epNow.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if found, err := secrets.Update(ctx, edited); err != nil || !found {
		t.Fatalf("update: %v %v", found, err)
	}
	got, _ = secrets.Get(ctx, live.ID, a.ID)
	if got.Check != check || got.Username != "alice" {
		t.Fatalf("edit lost the check: %+v", got)
	}
	stale := a
	stale.Check = imageRegistryModel.Check{State: imageRegistryModel.CheckOK, CheckedAt: epNow.Add(3 * time.Hour)}
	if stored, err := secrets.SaveCheck(ctx, stale); err != nil || stored {
		t.Fatalf("a result for the old version was stored: %v %v", stored, err)
	}
}
