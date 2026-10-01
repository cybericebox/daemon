package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// TestEventFormUsesBackendSuppliedUUID exercises the real schema boundary: the
// form identity must be the caller's UUIDv7, never a database default.
func TestEventFormUsesBackendSuppliedUUID(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	users := userRepo.New(db.Queries)
	events := eventRepo.New(db.Queries)
	now := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	actor, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "forms-actor@test.test", now))
	if err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	e := mustCreateEvent(t, events, "legacyforms", "Legacy forms", now, now.Add(30*24*time.Hour), actor.ID, now)
	firstVersionID := uuid.Must(uuid.NewV7())
	secondVersionID := uuid.Must(uuid.NewV7())
	document, err := json.Marshal(map[string]any{"blocks": []any{}})
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	if _, err = db.Pool.Exec(ctx, `
INSERT INTO event_forms (id, event_id, title, enabled, required, created_at, updated_at)
VALUES ($1, $2, 'Registration form', true, true, $3, $3)`, firstVersionID, e.ID, now); err != nil {
		t.Fatalf("create form: %v", err)
	}
	for _, row := range []struct {
		id      uuid.UUID
		version int32
	}{
		{id: firstVersionID, version: 1},
		{id: secondVersionID, version: 2},
	} {
		if _, err = db.Pool.Exec(ctx, `
INSERT INTO event_form_versions (id, form_id, event_id, version, enabled, required, document, created_at)
VALUES ($1, $2, $3, $4, true, true, $5, $6)`, row.id, firstVersionID, e.ID, row.version, document, now.Add(time.Duration(row.version)*time.Minute)); err != nil {
			t.Fatalf("create version %d: %v", row.version, err)
		}
	}

	var formID uuid.UUID
	if err = db.Pool.QueryRow(ctx, `SELECT id FROM event_forms WHERE event_id = $1`, e.ID).Scan(&formID); err != nil {
		t.Fatalf("read form: %v", err)
	}
	if formID != firstVersionID {
		t.Fatalf("form ID = %s, want supplied backend UUIDv7 %s", formID, firstVersionID)
	}
	var attached int
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM event_form_versions WHERE event_id = $1 AND form_id = $2`, e.ID, formID).Scan(&attached); err != nil {
		t.Fatalf("count attached versions: %v", err)
	}
	if attached != 2 {
		t.Fatalf("attached versions = %d, want 2", attached)
	}
}

func TestRegistrationFormVersionsStaySeparateFromOtherForms(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	now := time.Now().UTC()
	actor, err := userRepo.New(db.Queries).Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "registration-form@test.test", now))
	if err != nil {
		t.Fatalf("seed actor: %v", err)
	}
	e := mustCreateEvent(t, eventRepo.New(db.Queries), "registrationform", "Registration form", now, now.Add(24*time.Hour), actor.ID, now)
	registrationID, surveyID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	registrationVersionID, surveyVersionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	document := []byte(`{"blocks":[]}`)
	for _, row := range []struct {
		formID, versionID uuid.UUID
		title             string
		purpose           string
	}{
		{registrationID, registrationVersionID, "Registration form", "registration"},
		{surveyID, surveyVersionID, "Survey", "other"},
	} {
		_, err = db.Queries.CreateInitialEventForm(ctx, postgres.CreateInitialEventFormParams{
			ID: row.versionID, FormID: row.formID, EventID: e.ID, Title: row.title, Purpose: row.purpose,
			Version: 1, Enabled: true, Required: true, Document: document, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("create %s: %v", row.title, err)
		}
	}
	latest, err := db.Queries.GetLatestEventFormVersion(ctx, e.ID)
	if err != nil || latest.ID != registrationVersionID {
		t.Fatalf("registration version = %s, err %v", latest.ID, err)
	}
	forms, err := db.Queries.ListEventForms(ctx, e.ID)
	if err != nil || len(forms) != 1 || forms[0].ID != surveyID {
		t.Fatalf("other forms = %+v, err %v", forms, err)
	}
	nextVersionID := uuid.Must(uuid.NewV7())
	_, err = db.Queries.CreateEventFormVersion(ctx, postgres.CreateEventFormVersionParams{
		ID: nextVersionID, FormID: registrationID, EventID: e.ID, Version: 2,
		Enabled: true, Required: true, Document: document, CreatedAt: now.Add(time.Minute),
	})
	if err != nil {
		t.Fatalf("create second registration version: %v", err)
	}
	latest, err = db.Queries.GetLatestEventFormVersion(ctx, e.ID)
	if err != nil || latest.ID != nextVersionID {
		t.Fatalf("latest registration version = %s, err %v", latest.ID, err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO event_form_answers (event_id, user_id, form_version_id, answers, submitted_at) VALUES ($1, $2, $3, '{}'::jsonb, $4)`, e.ID, actor.ID, registrationVersionID, now); err != nil {
		t.Fatalf("seed old answer: %v", err)
	}
	answerKey := postgres.GetEventFormAnswerParams{EventID: e.ID, UserID: actor.ID, FormVersionID: nextVersionID}
	if _, err = db.Queries.GetEventFormAnswer(ctx, answerKey); err != pgx.ErrNoRows {
		t.Fatalf("old answer must not complete the new version: %v", err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO event_form_answers (event_id, user_id, form_version_id, answers, submitted_at) VALUES ($1, $2, $3, '{}'::jsonb, $4)`, e.ID, actor.ID, nextVersionID, now.Add(time.Minute)); err != nil {
		t.Fatalf("seed current answer: %v", err)
	}
	if _, err = db.Pool.Exec(ctx, `INSERT INTO event_form_answers (event_id, user_id, form_version_id, answers, submitted_at) VALUES ($1, $2, $3, '{}'::jsonb, $4)`, e.ID, actor.ID, surveyVersionID, now.Add(2*time.Minute)); err != nil {
		t.Fatalf("seed survey answer: %v", err)
	}
	answer, err := db.Queries.GetEventFormAnswer(ctx, answerKey)
	if err != nil || answer.FormVersionID != nextVersionID {
		t.Fatalf("current answer = %+v, err %v", answer, err)
	}
	listed, err := db.Queries.ListEventFormAnswers(ctx, postgres.ListEventFormAnswersParams{EventID: e.ID, FormID: registrationID})
	if err != nil || len(listed) != 2 {
		t.Fatalf("listed answers = %+v, err %v", listed, err)
	}
	if listed[0].FormID != registrationID || listed[0].Version != 2 || !json.Valid(listed[0].Document) || listed[0].Email != "registration-form@test.test" {
		t.Fatalf("latest answer is missing its version and participant metadata: %+v", listed[0])
	}
	surveyAnswers, err := db.Queries.ListEventFormAnswers(ctx, postgres.ListEventFormAnswersParams{EventID: e.ID, FormID: surveyID})
	if err != nil || len(surveyAnswers) != 1 || surveyAnswers[0].FormID != surveyID {
		t.Fatalf("survey answers must be separate: %+v, err %v", surveyAnswers, err)
	}
}
