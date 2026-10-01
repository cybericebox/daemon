package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnswerFileRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// Answer files against the real schema: upload keeps the blob referenced,
// saving an answer attaches its file and releases the one it replaced, and
// retention removes unused uploads and orphaned references.
func TestEventAnswerFilesLifecycle(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC)
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", sql, err)
		}
		return n
	}
	user, err := userRepo.New(db.Queries).Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "answer-files@test.test", now))
	if err != nil {
		t.Fatal(err)
	}
	e := mustCreateEvent(t, eventRepo.New(db.Queries), "answerfiles", "Answer files", now, now.Add(30*24*time.Hour), user.ID, now)
	if _, err = db.Pool.Exec(ctx, `INSERT INTO file_blobs (content_hash, size_bytes, ref_count, created_at) VALUES ('hash', 4, 3, $1)`, now); err != nil {
		t.Fatal(err)
	}
	repo := eventAnswerFileRepo.New(db.Queries)
	upload := func(createdAt time.Time) uuid.UUID {
		t.Helper()
		id := uuid.Must(uuid.NewV7())
		if _, err := db.Pool.Exec(ctx, `INSERT INTO files (id, name, content_type, size_bytes, content_hash, created_at) VALUES ($1, 'cv.pdf', 'application/pdf', 4, 'hash', $2)`, id, createdAt); err != nil {
			t.Fatal(err)
		}
		if err := repo.Create(ctx, eventFormModel.NewAnswerFile(id, e.ID, eventFormModel.AnswerScopeParticipant, "cv", "cv.pdf", 4, "application/pdf", user.ID, createdAt)); err != nil {
			t.Fatal(err)
		}
		return id
	}
	first, second, unused := upload(now), upload(now), upload(now.Add(-48*time.Hour))
	if n := count(`SELECT count(*) FROM file_references WHERE ref_type = 'event_answer_file'`); n != 3 {
		t.Fatalf("every upload must be referenced, got %d", n)
	}

	if err = repo.Attach(ctx, e.ID, eventFormModel.AnswerScopeParticipant, user.ID, []uuid.UUID{first}, now); err != nil {
		t.Fatal(err)
	}
	got, err := repo.Get(ctx, first)
	if err != nil || got.AttachedAt == nil || got.OwnerID.UUID != user.ID {
		t.Fatalf("attached file = %+v, %v", got, err)
	}
	// A new answer replaces the file: the old one is released.
	if err = repo.Attach(ctx, e.ID, eventFormModel.AnswerScopeParticipant, user.ID, []uuid.UUID{second}, now); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM event_answer_files WHERE file_id = $1`, first); n != 0 {
		t.Fatal("the replaced file must be released")
	}
	if n := count(`SELECT count(*) FROM file_references WHERE ref_id = $1`, first); n != 0 {
		t.Fatal("the replaced file must lose its reference")
	}

	// An orphaned reference (its row removed with a team or an account).
	orphan := uuid.Must(uuid.NewV7())
	if _, err = db.Pool.Exec(ctx, `INSERT INTO files (id, name, content_hash, created_at) VALUES ($1, 'x', 'hash', $2)`, orphan, now); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO file_references (ref_type, ref_id, file_id) VALUES ('event_answer_file', $1, $1)`, orphan); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Queries.PurgeEventAnswerFiles(ctx, postgres.PurgeEventAnswerFilesParams{EndedBefore: now.Add(-365 * 24 * time.Hour), PendingBefore: now.Add(-24 * time.Hour), BatchSize: 100}); err != nil {
		t.Fatal(err)
	}
	if n := count(`SELECT count(*) FROM event_answer_files WHERE file_id = $1`, unused); n != 0 {
		t.Fatal("an upload unused past its period must be removed")
	}
	if n := count(`SELECT count(*) FROM file_references WHERE ref_type = 'event_answer_file'`); n != 1 {
		t.Fatalf("only the attached file keeps its reference, got %d", n)
	}
	if n := count(`SELECT count(*) FROM event_answer_files WHERE file_id = $1`, second); n != 1 {
		t.Fatal("the attached file of a running event must stay")
	}
}
