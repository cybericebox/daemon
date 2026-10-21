package event_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	"github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
)

func TestParticipantTaskSolutionsFreezeIncludesOwnTeam(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	finish, cutoff := now.Add(time.Hour), now.Add(-30*time.Minute)
	exec(`UPDATE events SET infrastructure_allowed = false, start_at = $2, finish_at = $3 WHERE id = $1`, f.eventID, now.Add(-2*time.Hour), finish)
	exec(`UPDATE event_configs SET scoreboard_visibility = $2, results_freeze_enabled = true, results_freeze_minutes = 90 WHERE event_id = $1`, f.eventID, int16(eventConfigModel.VisibilityPublic))
	uc := event.NewEventUseCase(event.Dependencies{Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)), SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now)})
	if err := uc.ReconcileEventStands(ctx); err != nil {
		t.Fatal(err)
	}
	var captainID uuid.UUID
	if err := f.db.Pool.QueryRow(ctx, `SELECT captain_id FROM event_teams WHERE id = $1`, f.blueID).Scan(&captainID); err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO event_participants (event_id,user_id,status,created_at,team_id,team_role) VALUES ($1,$2,$3,$4,$5,0)`, f.eventID, captainID, int16(participantModel.StatusApproved), now, f.blueID)
	// Red solved before the cutoff; the caller Blue solved afterward. Both are
	// normally visible, and Blue's personal completion must remain current.
	exec(`INSERT INTO team_challenge_solves (team_challenge_id, solved_at) SELECT id, $3 FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, f.redID, f.staticChallenge, cutoff.Add(-time.Minute))
	submitted, err := uc.SubmitChallenge(ctx, f.eventID, captainID, f.staticChallenge, event.SubmitChallengeInput{Answer: "ICE{x}", IdempotencyKey: uuid.Must(uuid.NewV7()), ReceivedAt: now})
	if err != nil || !submitted.Correct {
		t.Fatalf("own post-cutoff submission: %+v, %v", submitted, err)
	}
	board, err := uc.ListOwnChallenges(ctx, f.eventID, captainID)
	if err != nil || len(board) != 1 || board[0].SolvedAt == nil || board[0].AwardedPoints == nil {
		t.Fatalf("personal completion lost: %+v, %v", board, err)
	}
	if board[0].SolveCount == nil || *board[0].SolveCount != 1 {
		t.Errorf("task count must freeze own post-cutoff solve too: %+v", board[0].SolveCount)
	}
	page, err := uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].TeamName != "Red" || page.Items[0].Own {
		t.Errorf("frozen task solutions: %+v", page)
	}
	assertTaskSolutionsFrozen(t, page, true)
	// An empty frozen list still carries the applied-freeze state.
	exec(`UPDATE team_challenge_solves SET solved_at = $2 WHERE team_challenge_id IN (SELECT id FROM team_challenges WHERE event_id = $1)`, f.eventID, now)
	page, err = uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Errorf("empty frozen task solutions: %+v", page)
	}
	assertTaskSolutionsFrozen(t, page, true)
	// The moderators list stays live, and reopening results removes the cutoff.
	moderatorPage, err := uc.ListModeratorsChallengeSolves(ctx, f.eventID, f.staticChallenge, uuid.Nil, 30)
	if err != nil || moderatorPage.Total != 2 || len(moderatorPage.Items) != 2 {
		t.Fatalf("moderators must stay live: %+v, %v", moderatorPage, err)
	}
	assertTaskSolutionsFrozen(t, moderatorPage, false)
	exec(`UPDATE event_configs SET results_opened_at = $2 WHERE event_id = $1`, f.eventID, now)
	page, err = uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30)
	if err != nil || page.Total != 2 || len(page.Items) != 2 || !page.Items[1].Own && !page.Items[0].Own {
		t.Fatalf("opened task solutions: %+v, %v", page, err)
	}
	assertTaskSolutionsFrozen(t, page, false)

	// Manual mode keeps every participant result surface frozen after finish.
	settings := eventConfigModel.DefaultResultsSettings()
	if err := json.Unmarshal([]byte(`{"FreezeEnabled":true,"FreezeMinutes":90,"FreezeEndMode":"manual","FreezeBannerText":"Фінальні результати згодом.","FreezeBannerLevel":"critical"}`), &settings); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.UpdateResultsSettings(ctx, f.eventID, eventConfigModel.VisibilityPublic, settings, f.ownerID); err != nil {
		t.Fatal(err)
	}
	if _, err := uc.SetResultsOpened(ctx, f.eventID, false, f.ownerID); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE events SET finish_at = $2 WHERE id = $1`, f.eventID, now.Add(-time.Minute))
	page, err = uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30)
	if err != nil || page.Total != 0 || len(page.Items) != 0 {
		t.Errorf("manual post-finish solutions: %+v, %v", page, err)
	}
	assertTaskSolutionsFrozen(t, page, true)
	board, err = uc.ListOwnChallenges(ctx, f.eventID, captainID)
	if err != nil || len(board) != 1 || board[0].SolveCount == nil || *board[0].SolveCount != 0 || board[0].SolvedAt == nil || board[0].AwardedPoints == nil {
		t.Errorf("manual post-finish task count/completion: %+v, %v", board, err)
	}
	snapshot, err := uc.GetResultsSnapshot(ctx, f.eventID, event.ResultsAccess{UserID: &captainID}, false)
	if err != nil || !snapshot.Freeze.Active || !snapshot.Freeze.Applied {
		t.Errorf("manual post-finish public freeze: %+v, %v", snapshot.Freeze, err)
	}
	banner, err := json.Marshal(snapshot.Freeze)
	if err != nil {
		t.Fatal(err)
	}
	var bannerFields map[string]json.RawMessage
	if err := json.Unmarshal(banner, &bannerFields); err != nil {
		t.Fatal(err)
	}
	if string(bannerFields["BannerText"]) != `"Фінальні результати згодом."` || string(bannerFields["BannerLevel"]) != `"critical"` {
		t.Errorf("participant freeze banner lost: %s", banner)
	}
	staffSnapshot, err := uc.GetResultsSnapshot(ctx, f.eventID, event.ResultsAccess{UserID: &f.ownerID}, false)
	if err != nil {
		t.Fatal(err)
	}
	staffBanner, err := json.Marshal(staffSnapshot.Freeze)
	if err != nil {
		t.Fatal(err)
	}
	var staffFields map[string]json.RawMessage
	if err := json.Unmarshal(staffBanner, &staffFields); err != nil {
		t.Fatal(err)
	}
	if staffSnapshot.Freeze.Applied || string(staffFields["BannerText"]) != `""` || string(staffFields["BannerLevel"]) != `"warning"` {
		t.Errorf("unapplied banner leaked: %s", staffBanner)
	}
	// Existing opening clears manual freeze; restoring it re-applies the cutoff.
	if _, err := uc.SetResultsOpened(ctx, f.eventID, true, f.ownerID); err != nil {
		t.Fatal(err)
	}
	page, err = uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30)
	if err != nil || page.Total != 2 {
		t.Errorf("opened manual post-finish solutions: %+v, %v", page, err)
	}
	assertTaskSolutionsFrozen(t, page, false)
	if _, err := uc.SetResultsOpened(ctx, f.eventID, false, f.ownerID); err != nil {
		t.Fatal(err)
	}
	page, err = uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30)
	if err != nil || page.Total != 0 {
		t.Errorf("restored manual post-finish solutions: %+v, %v", page, err)
	}
	assertTaskSolutionsFrozen(t, page, true)
	// Legacy/automatic settings retain the finish-time unfreeze behavior.
	settings = eventConfigModel.DefaultResultsSettings()
	settings.FreezeEnabled = true
	if _, err := uc.UpdateResultsSettings(ctx, f.eventID, eventConfigModel.VisibilityPublic, settings, f.ownerID); err != nil {
		t.Fatal(err)
	}
	page, err = uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30)
	if err != nil || page.Total != 2 {
		t.Errorf("automatic post-finish solutions: %+v, %v", page, err)
	}
	assertTaskSolutionsFrozen(t, page, false)
}

func assertTaskSolutionsFrozen(t *testing.T, page event.ChallengeSolvesPage, want bool) {
	t.Helper()
	if page.Frozen != want {
		t.Errorf("task solutions Frozen=%t, want %t", page.Frozen, want)
	}
}
