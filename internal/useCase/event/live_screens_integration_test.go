package event_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

func liveScreensFixture(t *testing.T, tag string) (*event.EventUseCase, *testhelpers.TestDB, uuid.UUID, uuid.UUID) {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now()
	user, err := userRepo.New(db.Queries).Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), tag+"@screens.test", now))
	if err != nil {
		t.Fatal(err)
	}
	e, err := eventModel.NewEvent(tag, "Screens "+tag, now.Add(-24*time.Hour), time.Time{}, user.ID, now)
	if err != nil {
		t.Fatal(err)
	}
	created, err := eventRepo.New(db.Queries).Create(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventConfigRepo.New(db.Queries).Create(ctx, eventConfigModel.NewEventConfig(created.ID, now)); err != nil {
		t.Fatal(err)
	}
	uc := event.NewEventUseCase(event.Dependencies{Repo: db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(db.Pool))})
	return uc, db, created.ID, user.ID
}

func TestLiveScreensLifecycle(t *testing.T) {
	uc, _, eventID, userID := liveScreensFixture(t, "screens")
	ctx := context.Background()

	screens, err := uc.ListLiveScreens(ctx, eventID, true)
	if err != nil || len(screens) != 1 || screens[0].Name != "Екран 1" || screens[0].Version != 1 || screens[0].HasDraft || screens[0].Link != nil {
		t.Fatalf("a new event has «Екран 1»: %+v, %v", screens, err)
	}
	first := screens[0].ID

	// Old single-screen calls (uuid.Nil) are the first screen.
	draft := eventContentModel.DefaultLiveLayout()
	draft.Theme = "light"
	if err = uc.SaveLiveLayoutDraft(ctx, eventID, uuid.Nil, draft); err != nil {
		t.Fatal(err)
	}
	if editor, err := uc.GetLiveScreenEditor(ctx, eventID, first); err != nil || editor.Draft == nil || editor.Draft.Theme != "light" || !editor.HasDraft {
		t.Fatalf("the single-screen draft went elsewhere: %+v, %v", editor, err)
	}

	// A second screen without a name is «Екран 2»; a copy carries layout and draft.
	second, err := uc.CreateLiveScreen(ctx, eventID, event.CreateLiveScreenInput{})
	if err != nil || second.Name != "Екран 2" || second.Position != 1 {
		t.Fatalf("second = %+v, %v", second, err)
	}
	copied, err := uc.CreateLiveScreen(ctx, eventID, event.CreateLiveScreenInput{Name: "  Фойє ", CopyFrom: &first})
	if err != nil || copied.Name != "Фойє" || !copied.HasDraft {
		t.Fatalf("copy = %+v, %v", copied, err)
	}
	template := eventContentModel.DefaultLiveLayout()
	template.Version, template.Aspect = 99, "4:3"
	fromTemplate, err := uc.CreateLiveScreen(ctx, eventID, event.CreateLiveScreenInput{Layout: &template})
	if err != nil || fromTemplate.Name != "Екран 4" || fromTemplate.Version != 1 || fromTemplate.HasDraft {
		t.Fatalf("template = %+v, %v", fromTemplate, err)
	}
	if _, err = uc.CreateLiveScreen(ctx, eventID, event.CreateLiveScreenInput{Layout: &template, CopyFrom: &first}); !errors.Is(err, eventContentModel.ErrLiveLayoutInvalid.Err()) {
		t.Fatalf("copy and template together = %v", err)
	}
	bad := eventContentModel.LiveLayout{}
	if _, err = uc.CreateLiveScreen(ctx, eventID, event.CreateLiveScreenInput{Layout: &bad}); !errors.Is(err, eventContentModel.ErrLiveLayoutInvalid.Err()) {
		t.Fatalf("an invalid template = %v", err)
	}

	// Draft and publish per screen: the versions are the screen's own.
	sideDraft := eventContentModel.DefaultLiveLayout()
	sideDraft.Aspect = "16:10"
	if err = uc.SaveLiveLayoutDraft(ctx, eventID, second.ID, sideDraft); err != nil {
		t.Fatal(err)
	}
	if layout, err := uc.PublishLiveLayout(ctx, eventID, second.ID); err != nil || layout.Version != 2 || layout.Aspect != "16:10" {
		t.Fatalf("publish second = %+v, %v", layout, err)
	}
	if editor, _ := uc.GetLiveScreenEditor(ctx, eventID, first); editor.Published.Version != 1 || editor.Draft == nil {
		t.Fatalf("publishing the second screen touched the first: %+v", editor)
	}
	if _, err = uc.PublishLiveLayout(ctx, eventID, second.ID); !errors.Is(err, eventContentModel.ErrLiveDraftNotFound.Err()) {
		t.Fatalf("publish without a draft = %v", err)
	}

	// Rename and reorder.
	if renamed, err := uc.RenameLiveScreen(ctx, eventID, second.ID, " Головний зал "); err != nil || renamed.Name != "Головний зал" {
		t.Fatalf("rename = %+v, %v", renamed, err)
	}
	if _, err = uc.RenameLiveScreen(ctx, eventID, second.ID, "  "); !errors.Is(err, eventContentModel.ErrLiveScreenNameInvalid.Err()) {
		t.Fatalf("an empty name = %v", err)
	}
	order := []uuid.UUID{second.ID, first, copied.ID, fromTemplate.ID}
	reordered, err := uc.ReorderLiveScreens(ctx, eventID, order)
	if err != nil || reordered[0].ID != second.ID || reordered[1].ID != first {
		t.Fatalf("reorder = %+v, %v", reordered, err)
	}
	for _, wrong := range [][]uuid.UUID{order[:3], {second.ID, second.ID, copied.ID, fromTemplate.ID}, {second.ID, first, copied.ID, uuid.Must(uuid.NewV7())}} {
		if _, err = uc.ReorderLiveScreens(ctx, eventID, wrong); !errors.Is(err, eventContentModel.ErrLiveScreenOrderInvalid.Err()) {
			t.Fatalf("reorder %v = %v", wrong, err)
		}
	}
	// The single-screen routes follow the order: now «Головний зал».
	if editor, _ := uc.GetLiveScreenEditor(ctx, eventID, uuid.Nil); editor.ID != second.ID {
		t.Fatalf("the first screen after reordering = %+v", editor)
	}

	// One link per screen; the list shows them.
	linkA, err := uc.IssueLiveScreenLink(ctx, eventID, first, userID, eventContentModel.LiveScreenExpiryNone)
	if err != nil {
		t.Fatal(err)
	}
	linkB, err := uc.IssueLiveScreenLink(ctx, eventID, second.ID, userID, eventContentModel.LiveScreenExpiryNone)
	if err != nil {
		t.Fatal(err)
	}
	if list, _ := uc.ListLiveScreens(ctx, eventID, true); list[0].Link == nil || list[0].Link.ID != linkB.ID || list[1].Link == nil || list[1].Link.ID != linkA.ID || list[2].Link != nil {
		t.Fatalf("links in the list = %+v", list)
	}
	if list, _ := uc.ListLiveScreens(ctx, eventID, false); list[0].Link != nil {
		t.Fatal("links must be hidden without manage access")
	}

	// A link opens its own screen, and polls its own version.
	view, err := uc.GetLiveScreen(ctx, eventID, linkA.Token)
	if err != nil || view.ScreenID != first || view.ScreenName != "Екран 1" || view.Layout.Version != 1 {
		t.Fatalf("link A opens = %+v, %v", view.ScreenID, err)
	}
	view, err = uc.GetLiveScreen(ctx, eventID, linkB.Token)
	if err != nil || view.ScreenID != second.ID || view.Layout.Aspect != "16:10" {
		t.Fatalf("link B opens = %+v, %v", view.ScreenID, err)
	}
	if version, err := uc.GetLiveScreenVersion(ctx, eventID, linkB.Token); err != nil || version.ScreenID != second.ID || version.Version != 2 {
		t.Fatalf("link B version = %+v, %v", version, err)
	}
	if _, err = uc.GetLiveScreenVersion(ctx, uuid.Must(uuid.NewV7()), linkB.Token); !errors.Is(err, eventContentModel.ErrLiveScreenTokenInvalid.Err()) {
		t.Fatalf("a link on another event = %v", err)
	}

	// Deleting a screen ends its link at once.
	if !uc.IsActiveLiveScreenToken(ctx, linkB.Token, time.Now()) {
		t.Fatal("link B must be known")
	}
	if err = uc.DeleteLiveScreen(ctx, eventID, second.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = uc.GetLiveScreen(ctx, eventID, linkB.Token); !errors.Is(err, eventContentModel.ErrLiveScreenTokenInvalid.Err()) {
		t.Fatalf("the deleted screen's link = %v", err)
	}
	if uc.IsActiveLiveScreenToken(ctx, linkB.Token, time.Now()) {
		t.Fatal("the deleted screen's link must lose its budget")
	}
	if _, err = uc.GetLiveScreen(ctx, eventID, linkA.Token); err != nil {
		t.Fatalf("link A must keep working: %v", err)
	}
	if _, err = uc.GetLiveScreenEditor(ctx, eventID, second.ID); !errors.Is(err, eventContentModel.ErrLiveScreenNotFound.Err()) {
		t.Fatalf("the deleted screen = %v", err)
	}
	if err = uc.SaveLiveLayoutDraft(ctx, eventID, second.ID, draft); !errors.Is(err, eventContentModel.ErrLiveScreenNotFound.Err()) {
		t.Fatalf("a draft of the deleted screen = %v", err)
	}

	// Never the last screen.
	for _, id := range []uuid.UUID{copied.ID, fromTemplate.ID} {
		if err = uc.DeleteLiveScreen(ctx, eventID, id); err != nil {
			t.Fatal(err)
		}
	}
	if err = uc.DeleteLiveScreen(ctx, eventID, first); !errors.Is(err, eventContentModel.ErrLiveScreenLast.Err()) {
		t.Fatalf("deleting the last screen = %v", err)
	}
	if _, err = uc.GetLiveScreen(ctx, eventID, linkA.Token); err != nil {
		t.Fatalf("the last screen keeps its link: %v", err)
	}
}

// At most 10 screens, also when they are added at the same time.
func TestLiveScreensLimitHoldsUnderConcurrency(t *testing.T) {
	uc, _, eventID, _ := liveScreensFixture(t, "screenslimit")
	ctx := context.Background()
	var wg sync.WaitGroup
	errs := make(chan error, 14)
	for i := 0; i < 14; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := uc.CreateLiveScreen(ctx, eventID, event.CreateLiveScreenInput{Name: fmt.Sprintf("Екран %d", i+2)})
			errs <- err
		}(i)
	}
	wg.Wait()
	close(errs)
	created, limited := 0, 0
	for err := range errs {
		switch {
		case err == nil:
			created++
		case errors.Is(err, eventContentModel.ErrLiveScreenLimit.Err()):
			limited++
		default:
			t.Fatalf("create: %v", err)
		}
	}
	screens, err := uc.ListLiveScreens(ctx, eventID, false)
	if err != nil || created != 9 || limited != 5 || len(screens) != eventContentModel.LiveDisplaysMax {
		t.Fatalf("created=%d limited=%d screens=%d err=%v", created, limited, len(screens), err)
	}
	if _, err = uc.DuplicateLiveScreen(ctx, eventID, screens[0].ID, ""); !errors.Is(err, eventContentModel.ErrLiveScreenLimit.Err()) {
		t.Fatalf("duplicate past the limit = %v", err)
	}
	// Deleting concurrently never leaves the event without a screen.
	errs = make(chan error, len(screens))
	for _, screen := range screens {
		wg.Add(1)
		go func(id uuid.UUID) {
			defer wg.Done()
			errs <- uc.DeleteLiveScreen(ctx, eventID, id)
		}(screen.ID)
	}
	wg.Wait()
	close(errs)
	last := 0
	for err := range errs {
		if errors.Is(err, eventContentModel.ErrLiveScreenLast.Err()) {
			last++
		} else if err != nil {
			t.Fatalf("delete: %v", err)
		}
	}
	if left, err := uc.ListLiveScreens(ctx, eventID, false); err != nil || len(left) != 1 || last != 1 {
		t.Fatalf("left=%d last=%d err=%v", len(left), last, err)
	}
}
