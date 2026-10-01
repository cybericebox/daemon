package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

type inboxSeed struct {
	user     uuid.UUID
	event    *uuid.UUID
	category string
	action   bool
	ref      string
	read     bool
}

func seedInbox(t *testing.T, q *postgres.Queries, s inboxSeed) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	params := postgres.CreateInAppParams{
		ID: id, UserID: s.user, Title: "T", Surface: "inbox", Actions: []byte(`[]`),
		NotificationType: "test", Category: s.category, ActionRequired: s.action,
		SubjectRef: pgtype.Text{String: s.ref, Valid: s.ref != ""},
	}
	if s.event != nil {
		params.ScopeEventID = uuid.NullUUID{UUID: *s.event, Valid: true}
	}
	if err := q.CreateInApp(context.Background(), params); err != nil {
		t.Fatalf("CreateInApp: %v", err)
	}
	if s.read {
		if _, err := q.MarkInAppRead(context.Background(), postgres.MarkInAppReadParams{ID: id, UserID: s.user}); err != nil {
			t.Fatalf("MarkInAppRead: %v", err)
		}
	}
	return id
}

func newInboxUser(t *testing.T, q *postgres.Queries, first, last string, role rbac.Role) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	if _, err := q.CreateUser(context.Background(), postgres.CreateUserParams{
		ID: id, Email: id.String() + "@inbox.test", FirstName: first, LastName: last,
		Role: string(role), Status: string(userModel.UserStatusActive), CreatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	return id
}

// TestInbox_ResolveBySubjectClosesEveryCopy: resolving a request closes every
// recipient's open copy (marked read, kept in the list, resolver named) and
// leaves other subjects open.
func TestInbox_ResolveBySubjectClosesEveryCopy(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	resolver := newInboxUser(t, q, "Іван", "П.", rbac.RoleUser)
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	event := uuid.Must(uuid.NewV7())
	ref := "application:" + event.String() + ":x"
	seedInbox(t, q, inboxSeed{user: first, event: &event, category: "requests", action: true, ref: ref})
	seedInbox(t, q, inboxSeed{user: second, event: &event, category: "requests", action: true, ref: ref, read: true})
	other := seedInbox(t, q, inboxSeed{user: first, event: &event, category: "requests", action: true, ref: "application:other"})

	affected, err := q.ResolveInboxBySubjectRef(ctx, postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: ref, Resolution: "approved", ResolvedBy: uuid.NullUUID{UUID: resolver, Valid: true},
	})
	if err != nil || affected != 2 {
		t.Fatalf("ResolveInboxBySubjectRef = %d, %v; want 2 copies", affected, err)
	}
	again, err := q.ResolveInboxBySubjectRef(ctx, postgres.ResolveInboxBySubjectRefParams{SubjectRef: ref, Resolution: "rejected"})
	if err != nil || again != 0 {
		t.Fatalf("second resolve = %d, %v; resolved copies must stay as they are", again, err)
	}

	for _, user := range []uuid.UUID{first, second} {
		rows, err := q.ListInAppByUser(ctx, postgres.ListInAppByUserParams{
			UserID: user, CategoryFilter: "requests",
			BeforeCreatedAt: time.Now().Add(time.Hour), BeforeID: uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff")),
		})
		if err != nil {
			t.Fatalf("ListInAppByUser: %v", err)
		}
		for _, row := range rows {
			if row.ID == other {
				if row.ResolvedAt.Valid || row.ReadAt.Valid {
					t.Fatalf("another subject was touched: %+v", row)
				}
				continue
			}
			if !row.ResolvedAt.Valid || row.Resolution.String != "approved" || !row.ReadAt.Valid ||
				row.ResolvedBy.UUID != resolver || row.ResolvedByName != "Іван П." {
				t.Fatalf("copy of %s not resolved for everyone: %+v", user, row)
			}
		}
	}
}

// TestInbox_CountsByCategoryAndOtherEvents: open requests count until
// resolved (read or not); personal/activity count while unread; the scope
// limits the tabs; other Events' attention items are reported separately.
func TestInbox_CountsByCategoryAndOtherEvents(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	user := uuid.Must(uuid.NewV7())
	here, elsewhere := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	seedInbox(t, q, inboxSeed{user: user, event: &here, category: "requests", action: true, ref: "stand:a", read: true}) // open, read
	resolved := seedInbox(t, q, inboxSeed{user: user, event: &here, category: "requests", action: true, ref: "stand:b"})
	seedInbox(t, q, inboxSeed{user: user, event: &here, category: "activity"})
	seedInbox(t, q, inboxSeed{user: user, event: &here, category: "activity", read: true})
	seedInbox(t, q, inboxSeed{user: user, category: "personal"})                                                        // platform item
	seedInbox(t, q, inboxSeed{user: user, event: &elsewhere, category: "personal"})                                     // other Event, unread
	seedInbox(t, q, inboxSeed{user: user, event: &elsewhere, category: "requests", action: true, ref: "c", read: true}) // other Event, open
	seedInbox(t, q, inboxSeed{user: user, event: &elsewhere, category: "activity", read: true})                         // other Event, read
	seedInbox(t, q, inboxSeed{user: uuid.Must(uuid.NewV7()), event: &here, category: "personal"})                       // another user
	if _, err := q.ResolveInboxItem(ctx, postgres.ResolveInboxItemParams{ID: resolved, UserID: user, Resolution: "resolved"}); err != nil {
		t.Fatalf("ResolveInboxItem: %v", err)
	}

	for _, tc := range []struct {
		name   string
		filter string
		want   postgres.CountInboxByCategoryRow
	}{
		{"everything", "", postgres.CountInboxByCategoryRow{AllCount: 5, RequestsCount: 2, PersonalCount: 2, ActivityCount: 1}},
		{"event site", here.String(), postgres.CountInboxByCategoryRow{AllCount: 3, RequestsCount: 1, PersonalCount: 1, ActivityCount: 1, OtherEventsCount: 2}},
		{"platform only", uuid.Nil.String(), postgres.CountInboxByCategoryRow{AllCount: 1, PersonalCount: 1}},
	} {
		got, err := q.CountInboxByCategory(ctx, postgres.CountInboxByCategoryParams{UserID: user, EventFilter: tc.filter})
		if err != nil {
			t.Fatalf("%s: CountInboxByCategory: %v", tc.name, err)
		}
		if got != tc.want {
			t.Errorf("%s: counts = %+v, want %+v", tc.name, got, tc.want)
		}
	}

	// Reading a tab never closes its open requests.
	if err := q.MarkAllInAppReadByUser(ctx, postgres.MarkAllInAppReadByUserParams{UserID: user, CategoryFilter: "personal"}); err != nil {
		t.Fatalf("MarkAllInAppReadByUser: %v", err)
	}
	if err := q.MarkAllInAppReadByUser(ctx, postgres.MarkAllInAppReadByUserParams{UserID: user, CategoryFilter: "requests"}); err != nil {
		t.Fatalf("MarkAllInAppReadByUser: %v", err)
	}
	got, err := q.CountInboxByCategory(ctx, postgres.CountInboxByCategoryParams{UserID: user})
	if err != nil {
		t.Fatalf("CountInboxByCategory: %v", err)
	}
	if want := (postgres.CountInboxByCategoryRow{AllCount: 3, RequestsCount: 2, ActivityCount: 1}); got != want {
		t.Errorf("after read-all by tab: %+v, want %+v", got, want)
	}
}

func TestInbox_ActionRequiredOnlyForRequests(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	err := db.Queries.CreateInApp(context.Background(), postgres.CreateInAppParams{
		ID: uuid.Must(uuid.NewV7()), UserID: uuid.Must(uuid.NewV7()), Surface: "inbox", Actions: []byte(`[]`),
		Category: "personal", ActionRequired: true,
	})
	if err == nil {
		t.Fatal("an action-required personal item must be rejected by the schema")
	}
}

func TestInbox_PlatformAdminRecipients(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	q := db.Queries
	admin := newInboxUser(t, q, "A", "", rbac.RoleAdmin)
	super := newInboxUser(t, q, "S", "", rbac.RoleSuperAdmin)
	newInboxUser(t, q, "V", "", rbac.RoleAdminViewer)
	newInboxUser(t, q, "U", "", rbac.RoleUser)

	ids, err := q.ListPlatformAdminUserIDs(context.Background())
	if err != nil {
		t.Fatalf("ListPlatformAdminUserIDs: %v", err)
	}
	got := map[uuid.UUID]bool{}
	for _, id := range ids {
		got[id] = true
	}
	if !got[admin] || !got[super] || len(ids) != 2 {
		t.Fatalf("admins = %v, want exactly the admin and the super admin", ids)
	}
	recipients, err := q.ListEventStandRecipients(context.Background(), uuid.Must(uuid.NewV7()))
	if err != nil {
		t.Fatalf("ListEventStandRecipients: %v", err)
	}
	if len(recipients) != 2 {
		t.Fatalf("stand recipients without managers = %v, want the two admins", recipients)
	}
}

// TestInbox_LateCopyOfDecidedRequestIsCreatedResolved: a decision taken
// before a copy was delivered resolves that copy at insert; a request raised
// after the decision (a new failure of the same lab) stays open.
func TestInbox_LateCopyOfDecidedRequestIsCreatedResolved(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	moderator := newInboxUser(t, q, "Олена", "К.", rbac.RoleUser)
	late := uuid.Must(uuid.NewV7())
	ref := "stand:e:t"
	raised := time.Now().Add(-time.Minute)

	if _, err := q.ResolveInboxBySubjectRef(ctx, postgres.ResolveInboxBySubjectRefParams{
		SubjectRef: ref, Resolution: "fixed", ResolvedBy: uuid.NullUUID{UUID: moderator, Valid: true},
	}); err != nil {
		t.Fatalf("resolve before delivery: %v", err)
	}
	insert := func(raisedAt time.Time) uuid.UUID {
		id := uuid.Must(uuid.NewV7())
		if err := q.CreateInApp(ctx, postgres.CreateInAppParams{
			ID: id, UserID: late, Surface: "inbox", Actions: []byte(`[]`), NotificationType: "event.lab.failed",
			Category: "requests", ActionRequired: true, SubjectRef: pgtype.Text{String: ref, Valid: true},
			RaisedAt: pgtype.Timestamptz{Time: raisedAt, Valid: true},
		}); err != nil {
			t.Fatalf("CreateInApp: %v", err)
		}
		return id
	}
	decidedCopy, newFailure := insert(raised), insert(time.Now().Add(time.Minute))

	rows, err := q.ListInAppByUser(ctx, postgres.ListInAppByUserParams{
		UserID: late, BeforeCreatedAt: time.Now().Add(time.Hour), BeforeID: uuid.Must(uuid.FromString("ffffffff-ffff-ffff-ffff-ffffffffffff")),
	})
	if err != nil {
		t.Fatalf("ListInAppByUser: %v", err)
	}
	for _, row := range rows {
		switch row.ID {
		case decidedCopy:
			if !row.ResolvedAt.Valid || row.Resolution.String != "fixed" || !row.ReadAt.Valid || row.ResolvedByName != "Олена К." {
				t.Fatalf("late copy not created resolved: %+v", row)
			}
		case newFailure:
			if row.ResolvedAt.Valid || row.ReadAt.Valid {
				t.Fatalf("a request raised after the decision must stay open: %+v", row)
			}
		}
	}
}

// TestInbox_ResolveByPatternExpiresAndRemembers: an Event finish expires only
// that Event's open applications, and a copy delivered afterwards is created
// expired too.
func TestInbox_ResolveByPatternExpiresAndRemembers(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	manager := uuid.Must(uuid.NewV7())
	event, other := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	applicant := uuid.Must(uuid.NewV7())
	ref := "application:" + event.String() + ":" + applicant.String()
	seedInbox(t, q, inboxSeed{user: manager, event: &event, category: "requests", action: true, ref: ref})
	seedInbox(t, q, inboxSeed{user: manager, event: &other, category: "requests", action: true, ref: "application:" + other.String() + ":" + applicant.String()})
	raised := time.Now().Add(-time.Minute)

	n, err := q.ResolveInboxBySubjectPattern(ctx, postgres.ResolveInboxBySubjectPatternParams{
		SubjectPattern: "application:" + event.String() + ":%", Resolution: "expired",
	})
	if err != nil || n != 1 {
		t.Fatalf("ResolveInboxBySubjectPattern = %d, %v; want only the finished Event's application", n, err)
	}
	late := uuid.Must(uuid.NewV7())
	if err = q.CreateInApp(ctx, postgres.CreateInAppParams{
		ID: uuid.Must(uuid.NewV7()), UserID: late, Surface: "inbox", Actions: []byte(`[]`), Category: "requests",
		ActionRequired: true, SubjectRef: pgtype.Text{String: ref, Valid: true}, RaisedAt: pgtype.Timestamptz{Time: raised, Valid: true},
	}); err != nil {
		t.Fatalf("CreateInApp: %v", err)
	}
	counts, err := q.CountInboxByCategory(ctx, postgres.CountInboxByCategoryParams{UserID: late})
	if err != nil || counts.RequestsCount != 0 {
		t.Fatalf("late copy of an expired application counts as open: %+v %v", counts, err)
	}
	counts, err = q.CountInboxByCategory(ctx, postgres.CountInboxByCategoryParams{UserID: manager})
	if err != nil || counts.RequestsCount != 1 {
		t.Fatalf("the other Event's application must stay open: %+v %v", counts, err)
	}
}

func TestInbox_ApplicationRecipientsAreWriteManagers(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	eventID := mustCreateSubscriptionEvent(t, db, "inboxmgr")
	owner, moderator, viewer := newInboxUser(t, q, "O", "", rbac.RoleUser), newInboxUser(t, q, "M", "", rbac.RoleUser), newInboxUser(t, q, "V", "", rbac.RoleUser)
	for user, role := range map[uuid.UUID]int16{owner: 0, moderator: 1, viewer: 2} {
		if _, err := q.CreateEventManager(ctx, postgres.CreateEventManagerParams{EventID: eventID, UserID: user, Role: role, CreatedAt: time.Now()}); err != nil {
			t.Fatalf("CreateEventManager: %v", err)
		}
	}
	write, err := q.ListEventWriteManagerUserIDs(ctx, eventID)
	if err != nil {
		t.Fatalf("ListEventWriteManagerUserIDs: %v", err)
	}
	if len(write) != 2 || !containsID(write, owner) || !containsID(write, moderator) {
		t.Fatalf("application recipients = %v, want owner and moderator", write)
	}
	stand, err := q.ListEventStandRecipients(ctx, eventID)
	if err != nil {
		t.Fatalf("ListEventStandRecipients: %v", err)
	}
	if len(stand) != 3 || !containsID(stand, viewer) {
		t.Fatalf("lab failure recipients = %v, want every manager incl. the viewer", stand)
	}
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, v := range ids {
		if v == id {
			return true
		}
	}
	return false
}
