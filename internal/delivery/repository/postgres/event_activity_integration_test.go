package postgres_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventActivityRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventTeamRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
	eventTeamModel "github.com/cybericebox/daemon/internal/model/eventTeam"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

type eaRow struct {
	UserID  uuid.NullUUID
	TeamID  uuid.NullUUID
	Kind    string
	Subject uuid.NullUUID
	Data    map[string]any
}

func eaRows(t *testing.T, db *testhelpers.TestDB, eventID uuid.UUID) []eaRow {
	t.Helper()
	rows, err := db.Pool.Query(context.Background(), `SELECT user_id, team_id, kind, subject_id, data FROM event_activity WHERE event_id = $1 ORDER BY id`, eventID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var out []eaRow
	for rows.Next() {
		var r eaRow
		var data []byte
		if err = rows.Scan(&r.UserID, &r.TeamID, &r.Kind, &r.Subject, &data); err != nil {
			t.Fatal(err)
		}
		if err = json.Unmarshal(data, &r.Data); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func eaTeam(t *testing.T, db *testhelpers.TestDB, eventID, captainID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	team, err := eventTeamModel.New(eventID, captainID, name, uuid.Must(uuid.NewV4()).String(), rtNow)
	if err != nil {
		t.Fatal(err)
	}
	if team, err = eventTeamRepo.New(db.Queries).Create(context.Background(), team); err != nil {
		t.Fatal(err)
	}
	return team.ID
}

// The participation log is written by triggers on every write path:
// registration, a decision, joining, switching and leaving a team, and a
// captain change.
func TestEventActivity_TriggersLogParticipation(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	event := mustSeedEventForParticipants(t, db, "activitytriggers")
	captain := mustSeedUser(t, db, "activity-captain@test.test")
	member := mustSeedUser(t, db, "activity-member@test.test")
	manager := mustSeedUser(t, db, "activity-manager@test.test")
	first := eaTeam(t, db, event.ID, captain, "First")
	second := eaTeam(t, db, event.ID, captain, "Second")

	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at) VALUES ($1, $2, 1, $3)`, event.ID, member, rtNow)
	rtExec(t, db, `UPDATE event_participants SET status = 2, decided_by = $3, decided_at = $4 WHERE event_id = $1 AND user_id = $2`, event.ID, member, manager, rtNow)
	rtExec(t, db, `UPDATE event_participants SET team_id = $3, team_role = 1 WHERE event_id = $1 AND user_id = $2`, event.ID, member, first)
	rtExec(t, db, `UPDATE event_participants SET team_id = $3 WHERE event_id = $1 AND user_id = $2`, event.ID, member, second)
	rtExec(t, db, `UPDATE event_participants SET team_id = NULL, team_role = NULL WHERE event_id = $1 AND user_id = $2`, event.ID, member)
	// A write that touches neither status nor team logs nothing.
	rtExec(t, db, `UPDATE event_participants SET pseudonym = 'Neo' WHERE event_id = $1 AND user_id = $2`, event.ID, member)
	rtExec(t, db, `UPDATE event_teams SET captain_id = $2 WHERE id = $1`, second, member)

	got := eaRows(t, db, event.ID)
	want := []struct {
		kind string
		user uuid.UUID
		team uuid.UUID
	}{
		{"participant_status_changed", member, uuid.Nil},
		{"participant_status_changed", member, uuid.Nil},
		{"team_joined", member, first},
		{"team_left", member, first},
		{"team_joined", member, second},
		{"team_left", member, second},
		{"captain_changed", member, second},
	}
	if len(got) != len(want) {
		t.Fatalf("activity rows = %+v, want %d", got, len(want))
	}
	for i, w := range want {
		if got[i].Kind != w.kind || got[i].UserID.UUID != w.user || got[i].TeamID.UUID != w.team {
			t.Fatalf("row %d = %+v, want %s user %s team %s", i, got[i], w.kind, w.user, w.team)
		}
	}
	if got[0].Data["to"] != float64(1) {
		t.Fatalf("registration row data = %+v", got[0].Data)
	}
	if got[1].Data["from"] != float64(1) || got[1].Data["to"] != float64(2) || got[1].Data["by"] != manager.String() {
		t.Fatalf("decision row data = %+v", got[1].Data)
	}
	if got[6].Data["from"] != captain.String() {
		t.Fatalf("captain row data = %+v", got[6].Data)
	}
}

// Deleting an event cascades through its teams, participants and stands;
// the log triggers must not block it, and nothing of the event is left.
func TestEventActivity_EventDeletionIsNotBlocked(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	event := mustSeedEventForParticipants(t, db, "activitydelete")
	captain := mustSeedUser(t, db, "activity-delete@test.test")
	team := eaTeam(t, db, event.ID, captain, "Doomed")
	rtExec(t, db, `INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role) VALUES ($1, $2, 2, $3, $4, 0)`, event.ID, captain, rtNow, team)
	if _, err := db.Queries.CreateEventTeamStand(context.Background(), postgres.CreateEventTeamStandParams{EventTeamID: team, EventID: event.ID, Status: 1, Now: rtNow}); err != nil {
		t.Fatal(err)
	}

	rtExec(t, db, `DELETE FROM events WHERE id = $1`, event.ID)
	if n := rtCount(t, db, `SELECT count(*) FROM event_activity WHERE event_id = $1`, event.ID) +
		rtCount(t, db, `SELECT count(*) FROM event_stand_transitions WHERE event_id = $1`, event.ID); n != 0 {
		t.Fatalf("log rows of a deleted event left: %d", n)
	}
}

// Stand and laboratory status changes are logged with their generation; an
// update that keeps the status logs nothing.
func TestEventStandTransitions_LogStatusChanges(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	event := mustSeedEventForParticipants(t, db, "standtransitions")
	captain := mustSeedUser(t, db, "stand-captain@test.test")
	team := eaTeam(t, db, event.ID, captain, "Stand")
	challenge := mustCreateScoringChallenge(t, db.Queries, event.ID)

	if _, err := db.Queries.CreateEventTeamStand(ctx, postgres.CreateEventTeamStandParams{EventTeamID: team, EventID: event.ID, Status: 1, Now: rtNow}); err != nil {
		t.Fatal(err)
	}
	rtExec(t, db, `UPDATE event_team_stands SET status = 3, reason = 'ImagePullBackOff', status_changed_at = $2 WHERE event_team_id = $1`, team, rtNow.Add(time.Minute))
	rtExec(t, db, `UPDATE event_team_stands SET updated_at = $2 WHERE event_team_id = $1`, team, rtNow.Add(2*time.Minute))
	rtExec(t, db, `UPDATE event_team_stands SET status = 1, generation = 1, reason = NULL, status_changed_at = $2 WHERE event_team_id = $1`, team, rtNow.Add(3*time.Minute))

	binding, err := db.Queries.CreateLabBinding(ctx, postgres.CreateLabBindingParams{
		ID: uuid.Must(uuid.NewV7()), EventID: event.ID, EventTeamID: team, EventChallengeID: challenge.ID,
		LabGroupName: "group", LabName: "lab", CreatedAt: rtNow,
	})
	if err != nil {
		t.Fatal(err)
	}
	rtExec(t, db, `UPDATE lab_bindings SET readiness = 1 WHERE id = $1`, binding.ID)

	rows, err := db.Pool.Query(ctx, `
SELECT source, challenge_id, generation, from_status, to_status, COALESCE(reason, ''), at
FROM event_stand_transitions WHERE event_id = $1 ORDER BY id`, event.ID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	type transition struct {
		source     string
		challenge  uuid.NullUUID
		generation int32
		from       *int16
		to         int16
		reason     string
		at         time.Time
	}
	var got []transition
	for rows.Next() {
		var tr transition
		if err = rows.Scan(&tr.source, &tr.challenge, &tr.generation, &tr.from, &tr.to, &tr.reason, &tr.at); err != nil {
			t.Fatal(err)
		}
		got = append(got, tr)
	}
	if len(got) != 5 {
		t.Fatalf("transitions = %+v, want 5", got)
	}
	if got[0].source != "stand" || got[0].from != nil || got[0].to != 1 || !got[0].at.Equal(rtNow) {
		t.Fatalf("stand created: %+v", got[0])
	}
	if *got[1].from != 1 || got[1].to != 3 || got[1].reason != "ImagePullBackOff" || !got[1].at.Equal(rtNow.Add(time.Minute)) {
		t.Fatalf("stand failed: %+v", got[1])
	}
	if *got[2].from != 3 || got[2].to != 1 || got[2].generation != 1 {
		t.Fatalf("stand recreated: %+v", got[2])
	}
	if got[3].source != "lab" || got[3].challenge.UUID != challenge.ID || got[3].to != 0 || *got[4].from != 0 || got[4].to != 1 {
		t.Fatalf("lab transitions: %+v %+v", got[3], got[4])
	}
}

// The repository writes rows; AppendOnce keeps one row per user and subject
// inside the window.
func TestEventActivityRepo_AppendAndDedupe(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := eventActivityRepo.New(db.Queries)
	event := mustSeedEventForParticipants(t, db, "activityrepo")
	user := mustSeedUser(t, db, "activity-repo@test.test")
	team, challenge := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	open := eventActivityModel.TaskOpened(event.ID, user, team, challenge, rtNow)
	for i, at := range []time.Time{rtNow, rtNow.Add(30 * time.Second), rtNow.Add(61 * time.Second)} {
		open.At = at
		wrote, err := repo.AppendOnce(ctx, open, eventActivityModel.TaskOpenWindow)
		if err != nil {
			t.Fatal(err)
		}
		if want := i != 1; wrote != want {
			t.Fatalf("open at +%s wrote=%v, want %v", at.Sub(rtNow), wrote, want)
		}
	}
	if err := repo.Append(ctx, eventActivityModel.AttemptRejected(event.ID, user, nil, challenge, eventActivityModel.RejectRateLimit, rtNow)); err != nil {
		t.Fatal(err)
	}
	got := eaRows(t, db, event.ID)
	if len(got) != 3 || got[2].Kind != "attempt_rejected" || got[2].TeamID.Valid || got[2].Data["reason"] != "rate_limit" || got[2].Subject.UUID != challenge {
		t.Fatalf("rows = %+v", got)
	}
}

// Retention: the logs of an event past its period go; a deleted account's
// rows stay without the user reference.
func TestEventActivity_Retention(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := retentionRepo.New(db.Queries)
	activity := eventActivityRepo.New(db.Queries)
	cutoff := rtNow.AddDate(-1, 0, 0)
	old := rtFinishedEvent(t, db, "activityold", cutoff.AddDate(0, 0, -1))
	recent := rtFinishedEvent(t, db, "activityrecent", cutoff.AddDate(0, 0, 1))
	gone := mustSeedUser(t, db, "activity-gone@test.test")
	kept := mustSeedUser(t, db, "activity-kept@test.test")
	for _, e := range []uuid.UUID{old, recent} {
		for _, u := range []uuid.UUID{gone, kept} {
			if err := activity.Append(ctx, eventActivityModel.TaskOpened(e, u, uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), rtNow)); err != nil {
				t.Fatal(err)
			}
		}
		rtExec(t, db, `INSERT INTO event_stand_transitions (event_id, team_id, source, generation, to_status, at) VALUES ($1, $2, 'stand', 0, 1, $3)`, e, uuid.Must(uuid.NewV7()), rtNow)
	}

	if n := rtDrain(t, 1, func() (int64, error) { return repo.PurgeEventAnalytics(ctx, cutoff, 1) }); n != 3 {
		t.Fatalf("analytics rows purged = %d, want 3", n)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_activity WHERE event_id = $1`, recent) + rtCount(t, db, `SELECT count(*) FROM event_stand_transitions WHERE event_id = $1`, recent); n != 3 {
		t.Fatalf("rows of the recent event = %d, want 3", n)
	}

	users := userRepo.New(db.Queries)
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
	if _, err = repo.PurgeDeletedAccounts(ctx, rtNow, 10); err != nil {
		t.Fatal(err)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_activity WHERE user_id = $1`, gone); n != 0 {
		t.Fatalf("rows still naming the deleted account: %d", n)
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_activity WHERE event_id = $1 AND user_id IS NULL`, recent); n != 1 {
		t.Fatal("the deleted account's row must stay, anonymized")
	}
	if n := rtCount(t, db, `SELECT count(*) FROM event_activity WHERE user_id = $1`, kept); n != 1 {
		t.Fatal("another account's row must keep its user")
	}
}
