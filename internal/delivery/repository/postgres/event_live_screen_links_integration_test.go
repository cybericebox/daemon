package postgres_test

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/liveScreenRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func TestEventLiveScreenLinkIsOnePerScreen(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event, actorID := mustSeedEventForConfig(t, db, "livescreenlink")
	repo := liveScreenRepo.New(db.Queries)
	now := time.Now().UTC().Truncate(time.Microsecond)
	screen, err := repo.FirstDisplay(ctx, event.ID)
	if err != nil {
		t.Fatalf("a new event has its first screen: %v", err)
	}
	screenID := screen.ID

	if _, err := repo.Active(ctx, event.ID, screenID, now); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("a new event must have no link: %v", err)
	}
	first, firstToken, _ := eventContentModel.NewLiveScreenLink(event.ID, screenID, actorID, now, eventContentModel.LiveScreenExpiryNone, nil)
	if err := repo.Issue(ctx, first); err != nil {
		t.Fatalf("Issue: %v", err)
	}
	active, err := repo.Active(ctx, event.ID, screenID, now.Add(400*24*time.Hour))
	if err != nil || active.ID != first.ID || active.ExpiresAt != nil || active.CreatedBy != actorID {
		t.Fatalf("active without expiry = %+v, %v", active, err)
	}

	// Regeneration replaces the link in one statement; the old token dies.
	second, secondToken, _ := active.Regenerate(actorID, now.Add(time.Minute))
	if err = repo.Issue(ctx, second); err != nil {
		t.Fatalf("Issue (regenerate): %v", err)
	}
	old, _ := repo.GetByTokenHash(ctx, eventContentModel.HashLiveScreenToken(firstToken))
	if old.RevokedAt == nil {
		t.Fatal("the replaced link must be revoked")
	}
	if active, err = repo.Active(ctx, event.ID, screenID, now); err != nil || active.ID != second.ID || !bytes.Equal(active.TokenHash, eventContentModel.HashLiveScreenToken(secondToken)) {
		t.Fatalf("active after regenerate = %+v, %v", active, err)
	}

	// An expired link is replaced too.
	day, _, _ := eventContentModel.NewLiveScreenLink(event.ID, screenID, actorID, now.Add(2*time.Minute), eventContentModel.LiveScreenExpiryDay, nil)
	if err = repo.Issue(ctx, day); err != nil {
		t.Fatalf("Issue (day): %v", err)
	}
	if _, err = repo.Active(ctx, event.ID, screenID, now.Add(48*time.Hour)); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("an expired link must not be active: %v", err)
	}
	fresh, _, _ := eventContentModel.NewLiveScreenLink(event.ID, screenID, actorID, now.Add(49*time.Hour), eventContentModel.LiveScreenExpiryWeek, nil)
	if err = repo.Issue(ctx, fresh); err != nil {
		t.Fatalf("Issue after expiry: %v", err)
	}

	if affected, err := repo.Revoke(ctx, event.ID, screenID, now.Add(50*time.Hour)); err != nil || affected != 1 {
		t.Fatalf("Revoke: %d, %v", affected, err)
	}
	if affected, _ := repo.Revoke(ctx, event.ID, screenID, now.Add(51*time.Hour)); affected != 0 {
		t.Fatal("turning off twice must touch nothing")
	}
	if _, err = repo.Active(ctx, event.ID, screenID, now.Add(50*time.Hour)); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("no link after «Вимкнути»: %v", err)
	}
	// The database refuses a second unrevoked link outright.
	a, _, _ := eventContentModel.NewLiveScreenLink(event.ID, screenID, actorID, now, eventContentModel.LiveScreenExpiryNone, nil)
	b, _, _ := eventContentModel.NewLiveScreenLink(event.ID, screenID, actorID, now, eventContentModel.LiveScreenExpiryNone, nil)
	if _, err = db.Pool.Exec(ctx, `INSERT INTO event_live_screen_links (id, event_id, screen_id, token_hash, created_at) VALUES ($1, $2, $7, $3, $4), ($5, $2, $7, $6, $4)`,
		a.ID, event.ID, a.TokenHash, now, b.ID, b.TokenHash, screenID); err == nil {
		t.Fatal("two unrevoked links of one screen were accepted")
	}

	// Another screen of the event has a link of its own beside the first one's.
	other, err := repo.CreateDisplay(ctx, eventContentModel.LiveDisplay{ID: uuid.Must(uuid.NewV7()), EventID: event.ID, Name: "Фойє", Layout: eventContentModel.DefaultLiveLayout(), CreatedAt: now})
	if err != nil || other.Position != 1 {
		t.Fatalf("second screen = %+v, %v", other, err)
	}
	if err = repo.Issue(ctx, a); err != nil {
		t.Fatalf("Issue (first screen): %v", err)
	}
	side, _, _ := eventContentModel.NewLiveScreenLink(event.ID, other.ID, actorID, now, eventContentModel.LiveScreenExpiryNone, nil)
	if err = repo.Issue(ctx, side); err != nil {
		t.Fatalf("Issue (second screen): %v", err)
	}
	byScreen, err := repo.ActiveByScreen(ctx, event.ID, now)
	if err != nil || len(byScreen) != 2 || byScreen[screenID].ID != a.ID || byScreen[other.ID].ID != side.ID {
		t.Fatalf("links by screen = %+v, %v", byScreen, err)
	}
	if _, err = repo.Active(ctx, event.ID, screenID, now); err != nil {
		t.Fatalf("the second screen's link replaced the first one's: %v", err)
	}
	// A link names a screen of its own event only.
	foreign, _ := mustSeedEventForConfig(t, db, "livescreenforeign")
	stray, _, _ := eventContentModel.NewLiveScreenLink(foreign.ID, other.ID, actorID, now, eventContentModel.LiveScreenExpiryNone, nil)
	if err = repo.Issue(ctx, stray); err == nil {
		t.Fatal("a link to another event's screen was accepted")
	}
	// Deleting a screen deletes its link: the token no longer resolves.
	if deleted, err := repo.DeleteDisplay(ctx, event.ID, other.ID); err != nil || deleted != 1 {
		t.Fatalf("DeleteDisplay: %d, %v", deleted, err)
	}
	if _, err = repo.GetByTokenHash(ctx, side.TokenHash); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("the deleted screen's link must be gone: %v", err)
	}
}
