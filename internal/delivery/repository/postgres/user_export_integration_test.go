package postgres_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/tablePrefsRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userExportRepo"
	tablePrefsModel "github.com/cybericebox/daemon/internal/model/tablePrefs"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The «download my data» queries on real rows: a person in a team with a teammate gets their own participation,
// team title and submissions, and nothing of the teammate.
func TestUserExport_OwnRowsOnly(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	f := anSeed(t, db, "export")

	mate := mustSeedUser(t, db, "export-mate-secret@test.test")
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 1)`,
		f.event, mate, anStart, f.team)
	mine := anAttempt(t, db, f, f.team, f.teamChallenge, true, anStart.Add(time.Minute))
	f2 := f
	f2.user = mate
	theirs := anAttempt(t, db, f2, f.team, f.teamChallenge, false, anStart.Add(2*time.Minute))
	rtExec(t, db, `UPDATE challenge_attempts SET answer = 'MATE-ANSWER' WHERE id = $1`, theirs)
	rtExec(t, db, `INSERT INTO in_app_notifications (id, user_id, title, body, surface) VALUES ($1, $2, 'mine', 'b', 'inbox'), ($3, $4, 'mates', 'b', 'inbox')`,
		uuid.Must(uuid.NewV7()), f.user, uuid.Must(uuid.NewV7()), mate)
	rtExec(t, db, `INSERT INTO notification_user_settings (user_id, notification_type, channel, enabled) VALUES ($1, 'x', 'email', false), ($2, 'y', 'email', true)`, f.user, mate)

	prefs := tablePrefsRepo.New(db.Queries)
	for _, owner := range []uuid.UUID{f.user, mate} {
		ok, err := prefs.Save(ctx, owner, "admin.users", []byte(`{"PageSize":25}`), time.Now(), tablePrefsModel.MaxTablesPerUser)
		if err != nil || !ok {
			t.Fatalf("save table view: %v %v", ok, err)
		}
	}

	var out userModel.DataExport
	if err := userExportRepo.New(db.Queries).Fill(ctx, f.user, &out); err != nil {
		t.Fatal(err)
	}

	if len(out.Participations) != 1 || out.Participations[0].EventID != f.event || out.Participations[0].TeamName != "Blue" || !out.Participations[0].IsCaptain {
		t.Fatalf("participations: %+v", out.Participations)
	}
	if len(out.FlagSubmissions) != 1 || out.FlagSubmissions[0].Answer != "ICE{?}" || out.FlagSubmissions[0].TeamName != "Blue" || !out.FlagSubmissions[0].Correct {
		t.Fatalf("submissions: %+v (mine %s)", out.FlagSubmissions, mine)
	}
	if len(out.Inbox) != 1 || out.Inbox[0].Title != "mine" {
		t.Fatalf("inbox: %+v", out.Inbox)
	}
	if len(out.NotificationSettings) != 1 || out.NotificationSettings[0].Type != "x" {
		t.Fatalf("notification settings: %+v", out.NotificationSettings)
	}
	if len(out.TableViews) != 1 || out.TableViews[0].Table != "admin.users" {
		t.Fatalf("table views: %+v", out.TableViews)
	}
	raw, _ := json.Marshal(out) //nolint:musttag // PascalCase field names are the file format
	for _, foreign := range []string{"MATE-ANSWER", "export-mate-secret", "mates"} {
		if strings.Contains(string(raw), foreign) {
			t.Errorf("the teammate's %q leaked into the export", foreign)
		}
	}
}

func TestTablePrefs_CapAndReset(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	user := mustSeedUser(t, db, "prefs@test.test")
	repo := tablePrefsRepo.New(db.Queries)

	for i := range 3 {
		if ok, err := repo.Save(ctx, user, "t"+string(rune('a'+i)), []byte(`{}`), time.Now(), 3); err != nil || !ok {
			t.Fatalf("save %d: %v %v", i, ok, err)
		}
	}
	if ok, err := repo.Save(ctx, user, "tz", []byte(`{}`), time.Now(), 3); err != nil || ok {
		t.Fatalf("a new table over the cap must be refused, got %v %v", ok, err)
	}
	if ok, err := repo.Save(ctx, user, "ta", []byte(`{"PageSize":50}`), time.Now(), 3); err != nil || !ok {
		t.Fatalf("a known table is always saved: %v %v", ok, err)
	}
	got, err := repo.Get(ctx, user, "ta")
	if err != nil || got == nil || got.PageSize != 50 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if err = repo.Delete(ctx, user, "ta"); err != nil {
		t.Fatal(err)
	}
	if got, err = repo.Get(ctx, user, "ta"); err != nil || got != nil {
		t.Fatalf("after reset: %+v %v", got, err)
	}
}
