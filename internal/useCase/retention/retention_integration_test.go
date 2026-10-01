package retention_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/internal/useCase/retention"
)

// The whole inactive-account flow against the real schema: warning, no
// deletion inside the grace period, deletion after it through the deletion
// cascade, then the purge keeps the competition result under the anonymized
// placeholder. A warned user who signs in again keeps the account.
func TestAccountInactivity_WarnThenDeleteThenAnonymize(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	t0 := time.Now().UTC().Truncate(time.Second)
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := db.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("exec %q: %v", sql, err)
		}
	}
	count := func(sql string, args ...any) int {
		t.Helper()
		var n int
		if err := db.Pool.QueryRow(ctx, sql, args...).Scan(&n); err != nil {
			t.Fatalf("count %q: %v", sql, err)
		}
		return n
	}
	users := userRepo.New(db.Queries)
	seedUser := func(email string, lastSeen time.Time) uuid.UUID {
		t.Helper()
		u, err := users.Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), email, lastSeen))
		if err != nil {
			t.Fatal(err)
		}
		exec(`UPDATE users SET status = 'active', first_name = 'Olena', last_seen = $2 WHERE id = $1`, u.ID, lastSeen)
		return u.ID
	}

	dormant := seedUser("dormant@test.test", t0.AddDate(-4, 0, 0))
	returning := seedUser("returning@test.test", t0.AddDate(-4, 0, 0))
	active := seedUser("active@test.test", t0.AddDate(0, -1, 0))

	// The dormant user's result in an individual event, under a pseudonym.
	creator := seedUser("creator@test.test", t0)
	e, err := eventModel.NewEvent("inactivity", "Inactivity", t0.AddDate(-5, 0, 0), t0.AddDate(1, 0, 0), creator, t0.AddDate(-5, 0, 0))
	if err != nil {
		t.Fatal(err)
	}
	event, err := eventRepo.New(db.Queries).Create(ctx, e)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventConfigRepo.New(db.Queries).Create(ctx, eventConfigModel.NewEventConfig(event.ID, t0)); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE event_configs SET allow_pseudonyms = true WHERE event_id = $1`, event.ID)
	team, err := eventTeamModel.NewIndividual(event.ID, dormant, "Solo-"+dormant.String(), uuid.Must(uuid.NewV4()).String(), t0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = eventTeamRepo.New(db.Queries).Create(ctx, team); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role, pseudonym)
VALUES ($1, $2, 1, $3, $4, 1, 'ZeroCool')`, event.ID, dormant, t0, team.ID)

	notifier := &fakeNotifier{}
	clock := t0
	uc := retention.New(retention.Dependencies{
		Store:     retentionRepo.New(db.Queries),
		Notifier:  notifier,
		Accounts:  auth.NewAuthUseCase(auth.Dependencies{Repo: db.Queries, Notifier: notifier}),
		Policy:    retentionModel.DefaultPolicy(),
		SignInURL: "https://id.example.test/sign-in",
		Now:       func() time.Time { return clock },
	})

	// Run 1: both inactive accounts are warned, the active one is not.
	if err = uc.EnforceAccountInactivity(ctx); err != nil {
		t.Fatalf("run 1: %v", err)
	}
	if len(notifier.to) != 2 {
		t.Fatalf("warnings sent to %v, want the two inactive accounts", notifier.to)
	}
	if got := notifier.sent[0].DeletionDate; got != t0.AddDate(0, 0, 30).Format("02.01.2006") {
		t.Fatalf("deletion date in the warning = %q", got)
	}
	if n := count(`SELECT count(*) FROM users WHERE inactivity_warned_at IS NOT NULL`); n != 2 {
		t.Fatalf("warned accounts = %d, want 2", n)
	}

	// The returning user signs in during the grace period.
	exec(`UPDATE users SET last_seen = $2 WHERE id = $1`, returning, t0.AddDate(0, 0, 3))

	// Run 2, inside the grace period: nothing is deleted, nobody warned twice.
	clock = t0.AddDate(0, 0, 10)
	if err = uc.EnforceAccountInactivity(ctx); err != nil {
		t.Fatalf("run 2: %v", err)
	}
	if n := count(`SELECT count(*) FROM users WHERE deleted_at IS NOT NULL`); n != 0 {
		t.Fatalf("deleted inside the grace period: %d", n)
	}
	if len(notifier.to) != 2 {
		t.Fatalf("a warned account must not be warned again: %v", notifier.to)
	}

	// Run 3, after the grace period: only the still-inactive account goes.
	clock = t0.AddDate(0, 0, 31)
	if err = uc.EnforceAccountInactivity(ctx); err != nil {
		t.Fatalf("run 3: %v", err)
	}
	if n := count(`SELECT count(*) FROM users WHERE id = $1 AND deleted_at IS NOT NULL AND email LIKE 'deleted+%'`, dormant); n != 1 {
		t.Fatal("the dormant account must be deleted and scrubbed")
	}
	if n := count(`SELECT count(*) FROM users WHERE id = ANY($1) AND deleted_at IS NULL AND inactivity_warned_at IS NULL`, []uuid.UUID{returning, active}); n != 2 {
		t.Fatal("the returning and the active accounts stay, with no pending warning")
	}

	// The purge run anonymizes the result the deleted account left behind.
	if err = uc.EnforceDataRetention(ctx); err != nil {
		t.Fatalf("purge run: %v", err)
	}
	var name string
	if err = db.Pool.QueryRow(ctx, `SELECT event_team_public_name(true, $1, $2, $3)`, event.ID, dormant, team.Name).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "Учасник" {
		t.Fatalf("public name of the deleted account's result = %q, want the placeholder", name)
	}
	if n := count(`SELECT count(*) FROM event_teams WHERE id = $1`, team.ID); n != 1 {
		t.Fatal("the result's team must stay")
	}
}
