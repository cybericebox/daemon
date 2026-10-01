package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func srSignal(t *testing.T, db *testhelpers.TestDB, status string, createdAt time.Time, payload map[string]any) uuid.UUID {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	id := uuid.Must(uuid.NewV7())
	rtExec(t, db, `
INSERT INTO signal_outbox (id, signal_type, occurred_at, payload, status, available_at, created_at)
VALUES ($1, 'participant.enrolled', $2, $3, $4, $2, $2)`, id, createdAt, body, status)
	return id
}

func srPayload(t *testing.T, db *testhelpers.TestDB, id uuid.UUID) map[string]any {
	t.Helper()
	var raw []byte
	if err := db.Pool.QueryRow(context.Background(), `SELECT payload FROM signal_outbox WHERE id = $1`, id).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	out := map[string]any{}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// Deleting an account anonymizes the signal history that names it: the user
// references become the nil UUID, personal fields go, hook errors are
// cleared; the event context stays and other people's signals are untouched.
func TestRetentionPurgeDeletedAccounts_AnonymizesSignals(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := retentionRepo.New(db.Queries)
	users := userRepo.New(db.Queries)

	gone := mustSeedUser(t, db, "signal-gone@test.test")
	kept := mustSeedUser(t, db, "signal-kept@test.test")
	eventID := uuid.Must(uuid.NewV7()).String()
	const nilID = "00000000-0000-0000-0000-000000000000"

	subject := srSignal(t, db, "completed", rtNow, map[string]any{
		"scope_event_id": eventID, "subject_user_id": gone.String(), "actor_user_id": kept.String(),
		"event_name": "CTF", "email": "gone@test.test", "name": "Gone Person",
	})
	actor := srSignal(t, db, "completed", rtNow, map[string]any{
		"scope_event_id": eventID, "subject_user_id": kept.String(), "actor_user_id": gone.String(), "event_name": "CTF",
	})
	other := srSignal(t, db, "completed", rtNow, map[string]any{
		"scope_event_id": eventID, "subject_user_id": kept.String(), "event_name": "CTF", "name": "Kept Person",
	})
	for _, id := range []uuid.UUID{subject, other} {
		rtExec(t, db, `
INSERT INTO signal_hook_executions (signal_id, hook_name, status, last_error)
VALUES ($1, 'notification', 'completed', 'smtp: rejected gone@test.test')`, id)
	}

	u, err := users.GetByID(ctx, gone)
	if err != nil {
		t.Fatal(err)
	}
	expected := u.UpdatedAt
	if err = u.SoftDelete(rtNow); err != nil {
		t.Fatal(err)
	}
	if _, err = users.Update(ctx, u, expected); err != nil {
		t.Fatal(err)
	}
	if n, err := repo.PurgeDeletedAccounts(ctx, rtNow, 10); err != nil || n != 1 {
		t.Fatalf("PurgeDeletedAccounts: n=%d err=%v", n, err)
	}

	got := srPayload(t, db, subject)
	if got["subject_user_id"] != nilID || got["actor_user_id"] != kept.String() {
		t.Fatalf("subject signal references: %+v", got)
	}
	if _, has := got["email"]; has {
		t.Fatalf("personal fields must go: %+v", got)
	}
	if _, has := got["name"]; has {
		t.Fatalf("personal fields must go: %+v", got)
	}
	if got["scope_event_id"] != eventID || got["event_name"] != "CTF" {
		t.Fatalf("the event context must stay: %+v", got)
	}
	if got = srPayload(t, db, actor); got["actor_user_id"] != nilID || got["subject_user_id"] != kept.String() {
		t.Fatalf("actor signal references: %+v", got)
	}
	if got = srPayload(t, db, other); got["name"] != "Kept Person" || got["subject_user_id"] != kept.String() {
		t.Fatalf("another person's signal must stay as is: %+v", got)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM signal_hook_executions WHERE signal_id = $1 AND last_error = ''`, subject); n != 1 {
		t.Fatal("hook errors of an anonymized signal must be cleared")
	}
	if n := rtCount(t, db, `SELECT count(*) FROM signal_hook_executions WHERE signal_id = $1 AND last_error <> ''`, other); n != 1 {
		t.Fatal("hook errors of other signals must stay")
	}
}

// Finished signals go after their period with their hook executions; pending
// and recent ones stay; a second run finds nothing.
func TestRetentionPurgeFinishedSignals(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := retentionRepo.New(db.Queries)
	cutoff := rtNow.AddDate(-1, 0, 0)
	payload := map[string]any{"scope_event_id": uuid.Must(uuid.NewV7()).String()}

	oldCompleted := srSignal(t, db, "completed", cutoff.Add(-time.Hour), payload)
	srSignal(t, db, "failed", cutoff.Add(-2*time.Hour), payload)
	oldPending := srSignal(t, db, "pending", cutoff.Add(-time.Hour), payload)
	recent := srSignal(t, db, "completed", cutoff.Add(time.Hour), payload)
	rtExec(t, db, `INSERT INTO signal_hook_executions (signal_id, hook_name, status) VALUES ($1, 'notification', 'completed')`, oldCompleted)

	if n := rtDrain(t, 1, func() (int64, error) { return repo.PurgeFinishedSignals(ctx, cutoff, 1) }); n != 2 {
		t.Fatalf("finished signals purged = %d, want 2", n)
	}
	for _, id := range []uuid.UUID{oldPending, recent} {
		if n := rtCount(t, db, `SELECT count(*) FROM signal_outbox WHERE id = $1`, id); n != 1 {
			t.Fatalf("signal %s must stay", id)
		}
	}
	if n := rtCount(t, db, `SELECT count(*) FROM signal_hook_executions`); n != 0 {
		t.Fatal("hook executions go with their signal")
	}
	if n, err := repo.PurgeFinishedSignals(ctx, cutoff, 10); err != nil || n != 0 {
		t.Fatalf("second run: n=%d err=%v", n, err)
	}
}
