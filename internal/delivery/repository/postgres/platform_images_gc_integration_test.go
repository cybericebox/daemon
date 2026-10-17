package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func gcDigest(c string) string { return "sha256:" + strings.Repeat(c, 64) }

// gcTag is the platform tag of gcDigest(c) under name: <name>-<the whole digest hex>.
func gcTag(name, c string) string { return name + "-" + strings.Repeat(c, 64) }

// The tag collector's query: a digest any version references (draft or published, under any name) is kept, a
// version with "devices": null or a non-array variants is no reference, and a digest with a recent row is kept whole.
func TestListUnreferencedPlatformImages(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	old := t0.Add(-48 * time.Hour)
	before := t0.Add(-24 * time.Hour)

	version := func(n int, status, variants string) {
		ex := uuid.Must(uuid.FromString("01900000-0000-7000-8000-0000000000c" + string(rune('0'+n))))
		if _, err := db.Pool.Exec(ctx, `INSERT INTO exercises (id, name, created_at, updated_at) VALUES ($1, $2, $3, $3)`, ex, "gc-ex-"+ex.String(), t0); err != nil {
			t.Fatal(err)
		}
		id := uuid.Must(uuid.FromString("01900000-0000-7000-8000-0000000000d" + string(rune('0'+n))))
		if _, err := db.Pool.Exec(ctx, `INSERT INTO exercise_versions (id, exercise_id, status, variants, created_at) VALUES ($1, $2, $3, $4::jsonb, $5)`,
			id, ex, status, variants, t0); err != nil {
			t.Fatal(err)
		}
	}
	// a: referenced by a published version; b: by a draft under another name; c: only by a version with devices null;
	// d: unreferenced; e: unreferenced but has a fresh second tag; f: a non-platform image with the same hash text.
	version(1, "published", `[{"topology":{"devices":[{"image":"platform:web@`+gcDigest("a")+`"},{"image":"nginx"}]}}]`)
	version(2, "draft", `[{"topology":{"devices":[{"image":"platform:renamed@`+gcDigest("b")+`"}]}}]`)
	version(3, "published", `[{"topology":{"devices":null}},{"topology":null},{"x":1},{"topology":{"devices":[{"image":"platform:c@`+gcDigest("c")+`"}]}}]`)
	version(4, "draft", `{"not":"an array"}`)
	version(5, "published", `[{"topology":{"devices":[{"image":"docker.io/x@`+gcDigest("d")+`"},{"image":5}]}}]`)

	img := repoSaver(t, db)
	img("a", gcTag("web", "a"), old)
	img("b", gcTag("web", "b"), old)
	img("d", gcTag("web", "d"), old)
	img("d", gcTag("alt", "d"), old.Add(time.Hour))
	img("e", gcTag("web", "e"), old)
	img("e", gcTag("new", "e"), t0)
	img("f", gcTag("web", "f"), old)

	rows, err := db.Queries.ListUnreferencedPlatformImages(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.Tag)
	}
	want := []string{gcTag("alt", "d"), gcTag("web", "d"), gcTag("web", "f")}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("got %v want %v", got, want)
	}

	if _, err = db.Queries.DeletePlatformImage(ctx, postgres.DeletePlatformImageParams{Digest: gcDigest("d"), Tag: gcTag("web", "d"), Before: before}); err != nil {
		t.Fatal(err)
	}
	if rows, err = db.Queries.ListUnreferencedPlatformImages(ctx, before); err != nil || len(rows) != 2 {
		t.Fatalf("after delete: %d %v", len(rows), err)
	}
}

func repoSaver(t *testing.T, db *testhelpers.TestDB) func(c, tag string, at time.Time) {
	t.Helper()
	return func(c, tag string, at time.Time) {
		if _, err := db.Pool.Exec(context.Background(),
			`INSERT INTO platform_images (digest, tag, name, uploaded_at) VALUES ($1, $2, $3, $4)`, gcDigest(c), tag, strings.SplitN(tag, "-", 2)[0], at); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDeleteStaleImageBlobPushes(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	for i, st := range []struct {
		c, state string
		at       time.Time
	}{{"1", "failed", t0.Add(-8 * 24 * time.Hour)}, {"2", "pushing", t0.Add(-8 * 24 * time.Hour)}, {"3", "failed", t0.Add(-time.Hour)}} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO image_blob_pushes (digest, state, updated_at) VALUES ($1, $2, $3)`, gcDigest(st.c), st.state, st.at); err != nil {
			t.Fatal(i, err)
		}
	}
	n, err := db.Queries.DeleteStaleImageBlobPushes(ctx, t0.Add(-7*24*time.Hour))
	if err != nil || n != 2 {
		t.Fatalf("deleted %d %v", n, err)
	}
}

// The re-check and the conditional delete see what changed after the list: a new reference, a fresh upload.
func TestPlatformImageRecheckAndConditionalDelete(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	before := t0.Add(-24 * time.Hour)
	save := repoSaver(t, db)
	save("a", gcTag("web", "a"), t0.Add(-48*time.Hour))
	save("b", gcTag("web", "b"), t0.Add(-48*time.Hour))

	collectable := func(c string) bool {
		v, err := db.Queries.IsDigestCollectable(ctx, postgres.IsDigestCollectableParams{TargetDigest: gcDigest(c), Before: before})
		if err != nil {
			t.Fatal(err)
		}
		return v.Valid && v.Bool
	}
	if !collectable("a") || !collectable("b") || collectable("z") {
		t.Fatal("old unreferenced digests are collectable, an unknown one is not")
	}

	// a: a draft starts referencing it; b: it is uploaded again.
	ex := uuid.Must(uuid.FromString("01900000-0000-7000-8000-0000000000c9"))
	if _, err := db.Pool.Exec(ctx, `INSERT INTO exercises (id, name, created_at, updated_at) VALUES ($1, 'gc-re', $2, $2)`, ex, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `INSERT INTO exercise_versions (id, exercise_id, status, variants, created_at) VALUES ($1, $2, 'draft', $3::jsonb, $4)`,
		uuid.Must(uuid.FromString("01900000-0000-7000-8000-0000000000e9")), ex, `[{"topology":{"devices":[{"image":"platform:new@`+gcDigest("a")+`"}]}}]`, t0); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Pool.Exec(ctx, `UPDATE platform_images SET uploaded_at = $1 WHERE digest = $2`, t0, gcDigest("b")); err != nil {
		t.Fatal(err)
	}
	if collectable("a") || collectable("b") {
		t.Fatal("re-referenced and refreshed digests are no longer collectable")
	}
	for _, c := range []struct{ d, tag string }{{"a", gcTag("web", "a")}, {"b", gcTag("web", "b")}} {
		n, err := db.Queries.DeletePlatformImage(ctx, postgres.DeletePlatformImageParams{Digest: gcDigest(c.d), Tag: c.tag, Before: before})
		if err != nil || n != 0 {
			t.Fatalf("%s: deleted %d %v", c.d, n, err)
		}
	}
	var rows int
	if err := db.Pool.QueryRow(ctx, "SELECT count(*) FROM platform_images").Scan(&rows); err != nil || rows != 2 {
		t.Fatalf("rows=%d %v", rows, err)
	}
}

// Defense in depth against a tag that two digests map to (only a bug or an old short tag can make one): the collector
// never removes such a tag. The candidate list skips every digest that has one, the re-check refuses it, and the
// conditional delete keeps its rows; the digest's records stay until the other digest's row goes. UNIQUE (tag) makes
// such rows impossible, so this per-test database drops it to stage them.
func TestPlatformImageSharedTagIsNeverCollected(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	if _, err := db.Pool.Exec(ctx, `ALTER TABLE platform_images DROP CONSTRAINT platform_images_tag_key`); err != nil {
		t.Fatal(err)
	}
	t0 := time.Date(2026, 10, 17, 9, 0, 0, 0, time.UTC)
	old := t0.Add(-48 * time.Hour)
	before := t0.Add(-24 * time.Hour)
	ex := uuid.Must(uuid.FromString("01900000-0000-7000-8000-0000000000ca"))
	if _, err := db.Pool.Exec(ctx, `INSERT INTO exercises (id, name, created_at, updated_at) VALUES ($1, 'gc-shared', $2, $2)`, ex, t0); err != nil {
		t.Fatal(err)
	}
	// a is referenced; b is not, and its row maps the same tag string as a's (the tag now serves one of them).
	if _, err := db.Pool.Exec(ctx, `INSERT INTO exercise_versions (id, exercise_id, status, variants, created_at) VALUES ($1, $2, 'published', $3::jsonb, $4)`,
		uuid.Must(uuid.FromString("01900000-0000-7000-8000-0000000000ea")), ex, `[{"topology":{"devices":[{"image":"platform:web@`+gcDigest("a")+`"}]}}]`, t0); err != nil {
		t.Fatal(err)
	}
	shared := "web-aaaaaaaaaaaa"
	for _, r := range []struct{ c, tag string }{{"a", shared}, {"b", shared}, {"b", gcTag("web", "b")}, {"c", gcTag("web", "c")}} {
		if _, err := db.Pool.Exec(ctx, `INSERT INTO platform_images (digest, tag, name, uploaded_at) VALUES ($1, $2, 'web', $3)`, gcDigest(r.c), r.tag, old); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := db.Queries.ListUnreferencedPlatformImages(ctx, before)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0].Digest != gcDigest("c") {
		t.Fatalf("candidates %+v: a digest with a shared tag must not be listed", rows)
	}
	v, err := db.Queries.IsDigestCollectable(ctx, postgres.IsDigestCollectableParams{TargetDigest: gcDigest("b"), Before: before})
	if err != nil || (v.Valid && v.Bool) {
		t.Fatalf("re-check of the digest with a shared tag: %v %v", v, err)
	}
	for _, tag := range []string{shared, gcTag("web", "b")} {
		n, err := db.Queries.DeletePlatformImage(ctx, postgres.DeletePlatformImageParams{Digest: gcDigest("b"), Tag: tag, Before: before})
		if err != nil || n != 0 {
			t.Fatalf("%s: deleted %d %v", tag, n, err)
		}
	}
	// Once the other digest's row is gone the tag is no longer shared and b is collected as usual.
	if _, err = db.Pool.Exec(ctx, `DELETE FROM platform_images WHERE digest = $1`, gcDigest("a")); err != nil {
		t.Fatal(err)
	}
	if v, err = db.Queries.IsDigestCollectable(ctx, postgres.IsDigestCollectableParams{TargetDigest: gcDigest("b"), Before: before}); err != nil || !v.Valid || !v.Bool {
		t.Fatalf("b after the shared row went: %v %v", v, err)
	}
}
