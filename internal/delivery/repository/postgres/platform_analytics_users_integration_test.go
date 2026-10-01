package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/platformAnalyticsRepo"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The role and blocked counts are the admin stats' own statements; the
// registration and active counts are shared with the overview.
func TestPlatformAnalyticsUsers_RolesAndBlocked(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := platformAnalyticsRepo.New(db.Queries)
	f := paovSeed(t, db)
	rtExec(t, db, `UPDATE users SET role = 'super_admin' WHERE id = $1`, f.u1)
	rtExec(t, db, `UPDATE users SET status = 'blocked' WHERE id = $1`, f.u2)

	roles, err := repo.UsersByRole(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var total, admins int64
	for _, r := range roles {
		total += r.Count
		if r.Role == "super_admin" {
			admins = r.Count
		}
	}
	var accounts int64
	if err = db.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE deleted_at IS NULL`).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	if total != accounts || admins != 1 {
		t.Fatalf("roles = %+v, accounts %d", roles, accounts)
	}
	if blocked, err := repo.UsersBlocked(ctx, "blocked"); err != nil || blocked != 1 {
		t.Fatalf("blocked = %d, %v", blocked, err)
	}
}

// DAU counts accounts with a sign-in or a last-seen that day; WAU the accounts
// active in the 7 days ending that day. Deleted accounts never count.
func TestPlatformAnalyticsUsers_ActiveDays(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := platformAnalyticsRepo.New(db.Queries)
	paovSeed(t, db)

	days, err := repo.UsersActiveDays(context.Background(), paovWindow.From, paovWindow.To)
	if err != nil || len(days) != 10 {
		t.Fatalf("days = %+v, %v", days, err)
	}
	want := map[int][2]int64{ // day -> {dau, wau}
		20: {0, 0}, 24: {0, 0}, 25: {1, 1}, 26: {1, 1}, 27: {1, 2}, 28: {0, 2}, 29: {0, 2},
	}
	for _, d := range days {
		w, ok := want[d.Day.Day()]
		if !ok {
			continue
		}
		if d.DAU != w[0] || d.WAU != w[1] {
			t.Fatalf("day %d: dau %d wau %d, want %v", d.Day.Day(), d.DAU, d.WAU, w)
		}
	}
	// The week window reaches back before the period start: 09-20 sees 09-14..09-20.
	early, err := repo.UsersActiveDays(context.Background(), paovDay(9, 12), paovDay(9, 14))
	if err != nil || len(early) != 2 || early[0].DAU != 1 || early[0].WAU != 1 || early[1].DAU != 0 || early[1].WAU != 1 {
		t.Fatalf("early = %+v, %v", early, err)
	}
}

func TestPlatformAnalyticsUsers_Methods(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := platformAnalyticsRepo.New(db.Queries)
	paovSeed(t, db)

	m, err := repo.UsersMethods(context.Background(), paovWindow.From, paovWindow.To)
	if err != nil {
		t.Fatal(err)
	}
	// u2 password only (registered in the previous window), u3 Google only,
	// u1 both; u5 and the 8 seeded accounts have neither. u4 is deleted.
	want := platformAnalyticsRepo.UsersMethods{
		PasswordOnly: 1, ProviderOnly: 1, Both: 1, None: 9,
		ProviderOnlyNew: 1, BothNew: 1,
	}
	if m != want {
		t.Fatalf("methods = %+v, want %+v", m, want)
	}
}

// Retention counts approved participations per account (moderators team and
// deleted accounts left out), up to the end of the period.
func TestPlatformAnalyticsUsers_Retention(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := platformAnalyticsRepo.New(db.Queries)
	paovSeed(t, db)
	ctx := context.Background()

	var accounts int64
	if err := db.Pool.QueryRow(ctx, `SELECT count(*) FROM users WHERE deleted_at IS NULL`).Scan(&accounts); err != nil {
		t.Fatal(err)
	}
	r, err := repo.UsersRetention(ctx, paovWindow.To)
	if err != nil {
		t.Fatal(err)
	}
	// u1 joined e1, e2 and e3 (3+); the seeded member joined e1 (1).
	if r.One != 1 || r.Two != 0 || r.ThreePlus != 1 || r.Never != accounts-2 {
		t.Fatalf("retention = %+v (accounts %d)", r, accounts)
	}
	// By the end of 09-15 u1 had joined e2 (09-12) and e3 (09-05).
	early, err := repo.UsersRetention(ctx, paovDay(9, 15))
	if err != nil || early.Two != 1 || early.One != 0 || early.ThreePlus != 0 {
		t.Fatalf("early retention = %+v, %v", early, err)
	}
	empty, err := repo.UsersRetention(ctx, time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC))
	if err != nil || empty != (platformAnalyticsRepo.UsersRetention{}) {
		t.Fatalf("empty retention = %+v, %v", empty, err)
	}
}

// People: the most active accounts, bounded, moderators and deleted accounts
// left out; solves are distinct effectively correct tasks on non-moderator boards.
func TestPlatformAnalyticsUsers_People(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := platformAnalyticsRepo.New(db.Queries)
	f := paovSeed(t, db)
	ctx := context.Background()

	anAttempt(t, db, f.anFixture, f.team, f.teamChallenge, false, anStart.Add(time.Minute))
	anAttempt(t, db, f.anFixture, f.team, f.teamChallenge, true, anStart.Add(2*time.Minute))
	anAttempt(t, db, f.anFixture, f.team, f.teamChallenge, true, anStart.Add(3*time.Minute))
	anAttempt(t, db, f.anFixture, f.moderators, f.modChallenge, true, anStart.Add(time.Minute))

	rows, err := repo.UsersPeople(ctx, 100)
	if err != nil || len(rows) != 2 {
		t.Fatalf("people = %+v, %v", rows, err)
	}
	if rows[0].ID != f.u1 || rows[0].EventsJoined != 3 || rows[0].Solves != 0 || rows[0].Email != "paov-u1@test.test" {
		t.Fatalf("first = %+v", rows[0])
	}
	if rows[1].ID != f.user || rows[1].EventsJoined != 1 || rows[1].Solves != 1 {
		t.Fatalf("second = %+v (one distinct solved task, the moderators board is left out)", rows[1])
	}
	limited, err := repo.UsersPeople(ctx, 1)
	if err != nil || len(limited) != 1 || limited[0].ID != f.u1 {
		t.Fatalf("limited = %+v, %v", limited, err)
	}
}
