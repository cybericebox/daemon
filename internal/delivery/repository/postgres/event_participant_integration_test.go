package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/participantRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var epNow = time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)

func TestParticipantTeamInvitationKeepsTargetUntilAcceptance(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "teaminvite")
	captainID := mustSeedUser(t, db, "team-invite-captain@test.test")
	memberID := mustSeedUser(t, db, "team-invite-member@test.test")
	team, err := eventTeamModel.New(event.ID, captainID, "Blue Team", "long-team-code", epNow)
	if err != nil {
		t.Fatal(err)
	}
	createdTeam, err := eventTeamRepo.New(db.Queries).Create(ctx, team)
	if err != nil {
		t.Fatal(err)
	}
	repo := participantRepo.New(db.Queries)
	_, created, err := repo.Invite(ctx, event.ID, memberID, captainID, uuid.NullUUID{UUID: createdTeam.ID, Valid: true}, epNow)
	if err != nil || !created {
		t.Fatalf("Invite: created=%v err=%v", created, err)
	}
	got, err := repo.Get(ctx, event.ID, memberID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != participantModel.StatusPending || !got.Invited || !got.InvitedToTeam || got.InvitedTeamID == nil || *got.InvitedTeamID != createdTeam.ID {
		t.Fatalf("team invitation was not saved: %+v", got)
	}
}

func TestTeamInvitationEmailTemplateContainsTeamAndAcceptanceLink(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	template, err := db.Queries.GetPublishedEmailTemplate(context.Background(), postgres.GetPublishedEmailTemplateParams{NotificationType: "participant.team_invitation.sent"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(template.Subject, "{{.team_name}}") || !strings.Contains(string(template.Body), "team_name") || !strings.Contains(string(template.Body), "{{invite_url}}") {
		t.Fatalf("team invitation template has no team name or link: subject=%q body=%s", template.Subject, template.Body)
	}
}

// epCursorSentinelTime/epMaxUUID are the keyset sentinels for a first page —
// mirrors the events integration harness.
var (
	epCursorSentinelTime = time.Date(9999, 12, 31, 23, 59, 59, 0, time.UTC)
	epMaxUUID            = uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff"))
)

// mustSeedEventForParticipants seeds a creator user and a parent event.
func mustSeedEventForParticipants(t *testing.T, db *testhelpers.TestDB, tag string) eventModel.Event {
	t.Helper()
	ctx := context.Background()
	users := userRepo.New(db.Queries)
	events := eventRepo.New(db.Queries)

	creator, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), tag+"-creator@test.test", epNow))
	if err != nil {
		t.Fatalf("seed creator user: %v", err)
	}

	e, err := eventModel.NewEvent(tag, "Participant Event "+tag, epNow, epNow.Add(30*24*time.Hour), creator.ID, epNow)
	if err != nil {
		t.Fatalf("NewEvent(%q): %v", tag, err)
	}
	created, err := events.Create(ctx, e)
	if err != nil {
		t.Fatalf("Create event(%q): %v", tag, err)
	}
	return created
}

// mustSeedUser seeds a real, FK-referenced user for use as a participant.
func mustSeedUser(t *testing.T, db *testhelpers.TestDB, email string) uuid.UUID {
	t.Helper()
	users := userRepo.New(db.Queries)
	u, err := users.Create(context.Background(), userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), email, epNow))
	if err != nil {
		t.Fatalf("seed user(%q): %v", email, err)
	}
	return u.ID
}

// mustNewPendingParticipant builds a pending Participant via the domain
// factory (RegistrationApproval policy always starts pending).
func mustNewPendingParticipant(t *testing.T, eventID, userID uuid.UUID, now time.Time) participantModel.Participant {
	t.Helper()
	p, err := participantModel.NewParticipant(eventID, userID, eventConfigModel.RegistrationApproval, now)
	if err != nil {
		t.Fatalf("NewParticipant: %v", err)
	}
	return p
}

// TestParticipantUpsert_CreatedThenConflict drives Upsert through the real
// participantRepo: the first call for an (event,user) pair creates the row,
// the second call for the SAME pair is a no-op (created == false).
func TestParticipantUpsert_CreatedThenConflict(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := participantRepo.New(db.Queries)

	event := mustSeedEventForParticipants(t, db, "pupsert")
	userID := mustSeedUser(t, db, "pupsert-user@test.test")

	pending := mustNewPendingParticipant(t, event.ID, userID, epNow)

	created, wasCreated, err := repo.Upsert(ctx, pending)
	if err != nil {
		t.Fatalf("Upsert (first): %v", err)
	}
	if !wasCreated {
		t.Fatal("first Upsert for a new (event,user) pair must report created=true")
	}
	if created.EventID != event.ID || created.UserID != userID || created.Status != participantModel.StatusPending {
		t.Fatalf("unexpected created row: %+v", created)
	}
	if !created.CreatedAt.Equal(epNow) {
		t.Fatalf("unexpected created_at: %+v", created)
	}
	if created.DecidedAt != nil {
		t.Fatalf("a pending participant must have nil DecidedAt, got %v", *created.DecidedAt)
	}

	again, wasCreated, err := repo.Upsert(ctx, pending)
	if err != nil {
		t.Fatalf("Upsert (second/conflict): %v", err)
	}
	if wasCreated {
		t.Fatal("second Upsert for the SAME (event,user) pair must report created=false")
	}
	if again.EventID != uuid.Nil || again.UserID != uuid.Nil {
		t.Fatalf("a conflict Upsert must return a zero-value Participant, got %+v", again)
	}

	// The original row must be untouched by the conflicting upsert.
	got, err := repo.Get(ctx, event.ID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != participantModel.StatusPending {
		t.Fatalf("Get after conflicting Upsert: status = %v, want StatusPending", got.Status)
	}
}

// TestParticipantUpdate_ApproveAndReload drives Update to move a pending
// participant to approved with DecidedAt/DecidedBy set, then reloads.
func TestParticipantUpdate_ApproveAndReload(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := participantRepo.New(db.Queries)

	event := mustSeedEventForParticipants(t, db, "papprove")
	userID := mustSeedUser(t, db, "papprove-user@test.test")
	approverID := mustSeedUser(t, db, "papprove-approver@test.test")

	pending := mustNewPendingParticipant(t, event.ID, userID, epNow)
	created, _, err := repo.Upsert(ctx, pending)
	if err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	decideAt := epNow.Add(time.Hour)
	if err = created.Approve(decideAt, approverID); err != nil {
		t.Fatalf("Approve: %v", err)
	}

	affected, err := repo.Update(ctx, created)
	if err != nil || affected != 1 {
		t.Fatalf("Update: affected=%d err=%v", affected, err)
	}

	got, err := repo.Get(ctx, event.ID, userID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Status != participantModel.StatusApproved {
		t.Fatalf("Status = %v, want StatusApproved", got.Status)
	}
	if got.DecidedAt == nil || !got.DecidedAt.Equal(decideAt) {
		t.Fatalf("DecidedAt = %v, want %v", got.DecidedAt, decideAt)
	}
	if !got.DecidedBy.Valid || got.DecidedBy.UUID != approverID {
		t.Fatalf("DecidedBy = %+v, want valid %v", got.DecidedBy, approverID)
	}
}

// TestParticipantList_StatusFilterAndCount drives List/Count against real
// SQL: a status filter of StatusPending must return only pending rows,
// -1 must return all rows regardless of status, and Count must agree.
func TestParticipantList_StatusFilterAndCount(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := participantRepo.New(db.Queries)

	event := mustSeedEventForParticipants(t, db, "plist")
	userA := mustSeedUser(t, db, "plist-a@test.test")
	userB := mustSeedUser(t, db, "plist-b@test.test")
	approverID := mustSeedUser(t, db, "plist-approver@test.test")

	pendingA := mustNewPendingParticipant(t, event.ID, userA, epNow)
	if _, _, err := repo.Upsert(ctx, pendingA); err != nil {
		t.Fatalf("Upsert A: %v", err)
	}

	pendingB := mustNewPendingParticipant(t, event.ID, userB, epNow.Add(time.Minute))
	createdB, _, err := repo.Upsert(ctx, pendingB)
	if err != nil {
		t.Fatalf("Upsert B: %v", err)
	}
	if err = createdB.Approve(epNow.Add(2*time.Minute), approverID); err != nil {
		t.Fatalf("Approve B: %v", err)
	}
	if _, err = repo.Update(ctx, createdB); err != nil {
		t.Fatalf("Update B: %v", err)
	}

	pendingOnly, err := repo.List(ctx, event.ID, int32(participantModel.StatusPending), participantRepo.KindAll, "", nil, epCursorSentinelTime, epMaxUUID, 10)
	if err != nil {
		t.Fatalf("List (pending only): %v", err)
	}
	if len(pendingOnly) != 1 || pendingOnly[0].Participant.UserID != userA {
		t.Fatalf("List (pending only) = %+v, want exactly [userA]", pendingOnly)
	}

	all, err := repo.List(ctx, event.ID, -1, participantRepo.KindAll, "", nil, epCursorSentinelTime, epMaxUUID, 10)
	if err != nil {
		t.Fatalf("List (all): %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("List (all) = %+v, want 2 rows", all)
	}

	pendingCount, err := repo.Count(ctx, event.ID, int32(participantModel.StatusPending), participantRepo.KindAll)
	if err != nil || pendingCount != 1 {
		t.Fatalf("Count (pending): count=%d err=%v", pendingCount, err)
	}

	allCount, err := repo.Count(ctx, event.ID, -1, participantRepo.KindAll)
	if err != nil || allCount != 2 {
		t.Fatalf("Count (all): count=%d err=%v", allCount, err)
	}

	searched, err := repo.List(ctx, event.ID, -1, participantRepo.KindAll, "PLIST-B@", nil, epCursorSentinelTime, epMaxUUID, 10)
	if err != nil {
		t.Fatalf("List (search): %v", err)
	}
	if len(searched) != 1 || searched[0].Participant.UserID != userB {
		t.Fatalf("List (search) = %+v, want exactly [userB]", searched)
	}
	searchCount, err := repo.CountMatching(ctx, event.ID, -1, participantRepo.KindAll, "PLIST-B@", nil)
	if err != nil || searchCount != 1 {
		t.Fatalf("CountMatching (search): count=%d err=%v", searchCount, err)
	}
	missCount, err := repo.CountMatching(ctx, event.ID, -1, participantRepo.KindAll, "nobody-here", nil)
	if err != nil || missCount != 0 {
		t.Fatalf("CountMatching (miss): count=%d err=%v", missCount, err)
	}
}

// TestParticipant_EventDeleteCascades asserts that deleting the parent event
// removes participant rows via the FK cascade.
func TestParticipant_EventDeleteCascades(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	events := eventRepo.New(db.Queries)
	repo := participantRepo.New(db.Queries)

	event := mustSeedEventForParticipants(t, db, "pcascade")
	userID := mustSeedUser(t, db, "pcascade-user@test.test")

	pending := mustNewPendingParticipant(t, event.ID, userID, epNow)
	if _, _, err := repo.Upsert(ctx, pending); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	affected, err := events.Delete(ctx, event.ID)
	if err != nil || affected != 1 {
		t.Fatalf("Delete event: affected=%d err=%v", affected, err)
	}

	if _, err = repo.Get(ctx, event.ID, userID); !repositoryTools.IsObjectNotFoundError(err) {
		t.Fatalf("Get after parent delete must be not-found, got %v", err)
	}

	count, err := repo.Count(ctx, event.ID, -1, participantRepo.KindAll)
	if err != nil || count != 0 {
		t.Fatalf("Count after parent delete: count=%d err=%v", count, err)
	}
}
