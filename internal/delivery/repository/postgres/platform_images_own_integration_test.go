package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The queries behind the own registry: the name an earlier upload recorded for a digest (newest row wins), every
// platform reference a device of any version names (distinct, drafts included, other images and malformed
// variants ignored), and the recorded images oldest first.
func TestOwnRegistryPlatformImageQueries(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	t0 := time.Date(2026, 11, 13, 9, 0, 0, 0, time.UTC)

	if _, err := db.Queries.PlatformImageNameByDigest(ctx, gcDigest("a")); err == nil {
		t.Fatal("a digest nobody recorded has no name")
	}
	img := repoSaver(t, db)
	img("a", gcTag("first", "a"), t0)
	img("a", gcTag("second", "a"), t0.Add(time.Hour))
	img("b", gcTag("other", "b"), t0.Add(-time.Hour))
	name, err := db.Queries.PlatformImageNameByDigest(ctx, gcDigest("a"))
	if err != nil || name != "second" {
		t.Fatalf("name %q %v", name, err)
	}

	all, err := db.Queries.ListPlatformImages(ctx)
	if err != nil || len(all) != 3 || all[0].Digest != gcDigest("b") || all[2].Name != "second" {
		t.Fatalf("list %+v %v", all, err)
	}

	version := func(n int, status, variants string) {
		ex := uuid.Must(uuid.FromString("01900000-0000-7000-8000-0000000000c" + string(rune('0'+n))))
		if _, err := db.Pool.Exec(ctx, `INSERT INTO exercises (id, name, created_at, updated_at) VALUES ($1, $2, $3, $3)`, ex, "own-ex-"+ex.String(), t0); err != nil {
			t.Fatal(err)
		}
		id := uuid.Must(uuid.FromString("01900000-0000-7000-8000-0000000000d" + string(rune('0'+n))))
		if _, err := db.Pool.Exec(ctx, `INSERT INTO exercise_versions (id, exercise_id, status, variants, created_at, name, description, tags) SELECT $1, e.id, $3, $4::jsonb, $5, e.name, e.description, e.tags FROM exercises e WHERE e.id = $2`,
			id, ex, status, variants, t0); err != nil {
			t.Fatal(err)
		}
	}
	version(1, "published", `[{"topology":{"devices":[{"image":"platform:web@`+gcDigest("a")+`"},{"image":"nginx"}]}}]`)
	version(2, "draft", `[{"topology":{"devices":[{"image":"platform:renamed@`+gcDigest("a")+`"},{"image":"platform:web@`+gcDigest("a")+`"}]}}]`)
	version(3, "published", `[{"topology":{"devices":null}},{"topology":null},{"x":1}]`)
	version(4, "draft", `{"not":"an array"}`)
	version(5, "published", `[{"topology":{"devices":[{"image":"docker.io/x@`+gcDigest("c")+`"},{"image":5}]}}]`)

	refs, err := db.Queries.ListReferencedPlatformImages(ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"platform:renamed@" + gcDigest("a"), "platform:web@" + gcDigest("a")}
	if strings.Join(refs, ",") != strings.Join(want, ",") {
		t.Fatalf("refs %v want %v", refs, want)
	}
}
