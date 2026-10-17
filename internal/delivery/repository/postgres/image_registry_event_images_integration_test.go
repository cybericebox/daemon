package postgres_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/imageRegistryRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The availability check reads every active attachment's pinned images: all
// variants, distinct, forwarding devices (no image) skipped; superseded or
// detached attachments and other events are not listed.
func TestListEventExerciseImages(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	exercises := exerciseRepo.New(db.Queries)
	events := eventRepo.New(db.Queries)
	repo := imageRegistryRepo.New(db.Queries)

	device := func(name, image string, typ exerciseModel.DeviceType) exerciseModel.Device {
		return exerciseModel.Device{ID: uuid.Must(uuid.FromString("0199a000-0000-7000-8000-0000000000d" + name)), Name: "d" + name, Type: typ, Image: image}
	}
	variants := itVariants()
	variants[0].Topology.Devices = []exerciseModel.Device{
		device("1", "nginx:1.27", exerciseModel.DeviceTypeContainer),
		device("2", "", exerciseModel.DeviceTypeUnmanagedSwitch),
		device("3", "platform:web@sha256:"+hex64, exerciseModel.DeviceTypeContainer),
	}
	second := itVariants()[0]
	second.Index = 1
	second.Topology.Devices = []exerciseModel.Device{device("4", "nginx:1.27", exerciseModel.DeviceTypeContainer), device("5", "ghcr.io/acme/x:1", exerciseModel.DeviceTypeContainer)}
	variants = append(variants, second)

	ex := mustCreateExercise(t, exercises, "IT images exercise")
	version, err := exercises.UpsertDraft(ctx, ex.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: variants}, itNow, uuid.NullUUID{})
	if err != nil {
		t.Fatal(err)
	}
	event := mustCreateEvent(t, events, "imagesevent", "Images CTF", itNow.Add(-time.Hour), itNow.Add(48*time.Hour), uuid.Nil, itNow)
	other := mustCreateEvent(t, events, "imagesother", "Other CTF", itNow.Add(-time.Hour), itNow.Add(48*time.Hour), uuid.Nil, itNow)
	attach := func(eventID uuid.UUID, revision int32, status int16) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		if _, err := db.Queries.CreateEventExercise(ctx, postgres.CreateEventExerciseParams{
			ID: id, EventID: eventID, ExerciseID: ex.ID, ExerciseVersionID: version.ID,
			Revision: revision, Status: status, CreatedAt: itNow,
		}); err != nil {
			t.Fatalf("attach: %v", err)
		}
		return id
	}
	attach(event.ID, 1, 1) // superseded
	active := attach(event.ID, 2, 0)
	attach(other.ID, 1, 0)

	got, err := repo.EventExerciseImages(ctx, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].EventExerciseID != active || got[0].ExerciseID != ex.ID || got[0].ExerciseName != "IT images exercise" {
		t.Fatalf("rows: %+v", got)
	}
	want := []string{"ghcr.io/acme/x:1", "nginx:1.27", "platform:web@sha256:" + hex64}
	if !slices.Equal(got[0].Images, want) {
		t.Fatalf("images %v, want %v", got[0].Images, want)
	}
	if none, err := repo.EventExerciseImages(ctx, uuid.Must(uuid.NewV7())); err != nil || len(none) != 0 {
		t.Fatalf("unknown event: %+v %v", none, err)
	}
}

const hex64 = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
