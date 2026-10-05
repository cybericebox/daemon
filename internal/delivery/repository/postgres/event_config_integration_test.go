package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var ecNow = time.Date(2026, 7, 13, 9, 0, 0, 0, time.UTC)

// mustSeedEventForConfig seeds an actor user and a parent event, returning
// both — every EventConfig row needs a real FK-referenced event to hang off.
func mustSeedEventForConfig(t *testing.T, db *testhelpers.TestDB, tag string) (eventModel.Event, uuid.UUID) {
	t.Helper()
	ctx := context.Background()
	users := userRepo.New(db.Queries)
	events := eventRepo.New(db.Queries)

	actor, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), tag+"@test.test", ecNow))
	if err != nil {
		t.Fatalf("seed actor user: %v", err)
	}

	e, err := eventModel.NewEvent(tag, "Config Event "+tag, ecNow, ecNow.Add(30*24*time.Hour), actor.ID, ecNow)
	if err != nil {
		t.Fatalf("NewEvent(%q): %v", tag, err)
	}
	created, err := events.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create event(%q): %v", tag, err)
	}
	return created, actor.ID
}

// TestEventConfigCreateGetRoundTrip drives Create -> Get through the real
// eventConfigRepo and asserts every field round-trips, including a config
// created via NewEventConfig having a nil Participation after reload.
func TestEventConfigCreateGetRoundTrip(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventConfigRepo.New(db.Queries)

	event, actorID := mustSeedEventForConfig(t, db, "cfgroundtrip")

	cfg := eventConfigModel.NewEventConfig(event.ID, ecNow)
	in := eventConfigModel.ConfigInput{
		Registration:           eventConfigModel.RegistrationApproval,
		ScoreboardVisibility:   eventConfigModel.VisibilityPublic,
		ParticipantsVisibility: eventConfigModel.VisibilityPrivate,
		PreviewDescription:     "A description",
		PreviewPicture:         "https://example.test/pic.png",
		ShowDifficulty:         false,
		HintsDisabled:          true,
	}
	if err := cfg.Update(in, ecNow, actorID); err != nil {
		t.Fatalf("Update: %v", err)
	}
	if err := cfg.SetStandTiming(eventStandModel.Timing{TeardownDelayMinutes: 15}, ecNow, actorID); err != nil {
		t.Fatalf("SetStandTiming: %v", err)
	}

	created, err := repo.Create(ctx, cfg)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.Participation != nil {
		t.Fatalf("a config built via NewEventConfig must have nil Participation, got %v", *created.Participation)
	}
	if created.Registration != in.Registration {
		t.Fatalf("unexpected registration fields: %+v", created)
	}
	if created.ScoreboardVisibility != in.ScoreboardVisibility || created.ParticipantsVisibility != in.ParticipantsVisibility {
		t.Fatalf("unexpected visibility fields: %+v", created)
	}
	if created.PreviewDescription != in.PreviewDescription || created.PreviewPicture != in.PreviewPicture {
		t.Fatalf("unexpected preview fields: %+v", created)
	}
	if created.StandTiming.TeardownDelayMinutes != 15 {
		t.Fatalf("create did not persist stand timing: %+v", created.StandTiming)
	}
	if !created.CreatedAt.Equal(ecNow) || !created.UpdatedAt.Equal(ecNow) {
		t.Fatalf("unexpected timestamps: %+v", created)
	}
	if !created.UpdatedBy.Valid || created.UpdatedBy.UUID != actorID {
		t.Fatalf("updated_by must round-trip the actor: %+v", created.UpdatedBy)
	}

	got, err := repo.Get(ctx, event.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Participation != nil {
		t.Fatalf("Get must forgive/round-trip a nil Participation, got %v", *got.Participation)
	}
	if got.EventID != created.EventID || got.Registration != created.Registration {
		t.Fatalf("Get identity mismatch: got %+v, want %+v", got, created)
	}
	if got.ScoreboardVisibility != created.ScoreboardVisibility || got.ParticipantsVisibility != created.ParticipantsVisibility {
		t.Fatalf("Get visibility mismatch: got %+v, want %+v", got, created)
	}
	if got.PreviewDescription != created.PreviewDescription || got.PreviewPicture != created.PreviewPicture {
		t.Fatalf("Get preview mismatch: got %+v, want %+v", got, created)
	}
	if got.ShowDifficulty || !got.HintsDisabled {
		t.Fatalf("Get did not round-trip board presentation: %+v", got)
	}
	if got.StandTiming != created.StandTiming {
		t.Fatalf("Get did not return stand timing: %+v", got.StandTiming)
	}
	if !got.CreatedAt.Equal(created.CreatedAt) || !got.UpdatedAt.Equal(created.UpdatedAt) {
		t.Fatalf("Get timestamps mismatch: got %+v, want %+v", got, created)
	}
}

// TestEventConfigUpdate_OptimisticLock exercises the real "updated_at IS NOT
// DISTINCT FROM $expected" guard: a stale expected_updated_at must hit 0
// rows, the fresh one must write.
func TestEventConfigUpdate_OptimisticLock(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventConfigRepo.New(db.Queries)

	event, actorID := mustSeedEventForConfig(t, db, "cflock")

	created, err := repo.Create(ctx, eventConfigModel.NewEventConfig(event.ID, ecNow))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if created.StandTiming != eventStandModel.DefaultTiming() {
		t.Fatalf("new rows must use the default stand timing: %+v", created.StandTiming)
	}

	// First writer holds the loaded snapshot and wins.
	fresh := created
	in := eventConfigModel.ConfigInput{
		Registration:           eventConfigModel.RegistrationOpen,
		ScoreboardVisibility:   eventConfigModel.VisibilityPublic,
		ParticipantsVisibility: eventConfigModel.VisibilityPublic,
		PreviewDescription:     "v2",
	}
	if err = fresh.Update(in, ecNow.Add(time.Hour), actorID); err != nil {
		t.Fatalf("Update (fresh): %v", err)
	}
	if err = fresh.SetStandTiming(eventStandModel.Timing{TeardownDelayMinutes: 30}, ecNow.Add(time.Hour), actorID); err != nil {
		t.Fatalf("SetStandTiming (fresh): %v", err)
	}
	affected, err := repo.Update(ctx, fresh, created.UpdatedAt)
	if err != nil || affected != 1 {
		t.Fatalf("first write: affected=%d err=%v", affected, err)
	}
	got, err := repo.Get(ctx, event.ID)
	if err != nil || got.StandTiming.TeardownDelayMinutes != 30 {
		t.Fatalf("stand timing did not survive update: %+v, %v", got, err)
	}

	// Second writer still holds the OLD snapshot — must hit 0 rows.
	stale := created
	in.PreviewDescription = "v3"
	if err = stale.Update(in, ecNow.Add(2*time.Hour), actorID); err != nil {
		t.Fatalf("Update (stale): %v", err)
	}
	affected, err = repo.Update(ctx, stale, created.UpdatedAt)
	if err != nil {
		t.Fatalf("stale write err: %v", err)
	}
	if affected != 0 {
		t.Fatalf("stale snapshot must not overwrite (lost update), affected=%d", affected)
	}
	got, err = repo.Get(ctx, event.ID)
	if err != nil || got.StandTiming.TeardownDelayMinutes != 30 || got.PreviewDescription != "v2" {
		t.Fatalf("stale update changed the config: %+v, %v", got, err)
	}
}

// TestEventConfigSetParticipation_Persists sets participation once, reloads,
// and asserts a non-nil Participation round-trips.
func TestEventConfigSetParticipation_Persists(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventConfigRepo.New(db.Queries)

	event, actorID := mustSeedEventForConfig(t, db, "cfpart")

	created, err := repo.Create(ctx, eventConfigModel.NewEventConfig(event.ID, ecNow))
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	updated := created
	if err = updated.SetParticipation(eventConfigModel.ParticipationTeam, false, ecNow.Add(time.Hour), actorID); err != nil {
		t.Fatalf("SetParticipation: %v", err)
	}
	affected, err := repo.Update(ctx, updated, created.UpdatedAt)
	if err != nil || affected != 1 {
		t.Fatalf("Update after SetParticipation: affected=%d err=%v", affected, err)
	}

	got, err := repo.Get(ctx, event.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Participation == nil {
		t.Fatal("Participation must round-trip as non-nil after being set")
	}
	if *got.Participation != eventConfigModel.ParticipationTeam {
		t.Fatalf("Participation = %v, want %v", *got.Participation, eventConfigModel.ParticipationTeam)
	}
}

// TestEventConfig_EventDeleteCascades asserts that deleting the parent event
// removes the 1:1 config row via the FK cascade.
func TestEventConfig_EventDeleteCascades(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	events := eventRepo.New(db.Queries)
	repo := eventConfigRepo.New(db.Queries)

	event, _ := mustSeedEventForConfig(t, db, "cfcascade")

	if _, err := repo.Create(ctx, eventConfigModel.NewEventConfig(event.ID, ecNow)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	affected, err := events.Delete(ctx, event.ID)
	if err != nil || affected != 1 {
		t.Fatalf("Delete event: affected=%d err=%v", affected, err)
	}

	if _, err = repo.Get(ctx, event.ID); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("Get after parent delete must be not-found, got %v", err)
	}
}
