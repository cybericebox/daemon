package event_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	eventChallengeModel "github.com/cybericebox/daemon/internal/model/eventChallenge"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	participantModel "github.com/cybericebox/daemon/internal/model/participant"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/event"
)

// TestParticipantBoard_PrerequisitesSolvesFilesAndRoster drives the W3 board
// reads against real SQL: lock by prerequisites, solve counts and solver
// names over the scoreboard population, batch file sizes and the roster.
func TestParticipantBoard_PrerequisitesSolvesFilesAndRoster(t *testing.T) {
	f := newStandFixture(t)
	ctx := context.Background()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := f.db.Pool.Exec(ctx, query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	exec(`UPDATE events SET infrastructure_allowed = false WHERE id = $1`, f.eventID)
	exec(`UPDATE event_configs SET scoreboard_visibility = $2, allow_pseudonyms = true WHERE event_id = $1`, f.eventID, int16(eventConfigModel.VisibilityPublic))
	f.shiftLifecycle(t, -10*time.Minute, 2*time.Hour)
	uc := event.NewEventUseCase(event.Dependencies{
		Repo: f.db.Queries, UoW: postgres.NewUnitOfWorker[event.IRepository](postgres.NewUoWFactory(f.db.Pool)),
		SignalPublishers: event.NewOutboxSignalPublisherFactory(time.Now),
	})
	if err := uc.ReconcileEventStands(ctx); err != nil {
		t.Fatal(err)
	}

	// Blue: captain plus a pseudonymous member.
	var captainID uuid.UUID
	if err := f.db.Pool.QueryRow(ctx, `SELECT captain_id FROM event_teams WHERE id = $1`, f.blueID).Scan(&captainID); err != nil {
		t.Fatal(err)
	}
	member, err := userRepo.New(f.db.Queries).Create(ctx, userModel.NewIncompleteUser(uuid.Must(uuid.NewV7()), "member@board.test", time.Now()))
	if err != nil {
		t.Fatal(err)
	}
	exec(`INSERT INTO event_participants (event_id, user_id, status, created_at, team_id, team_role)
		VALUES ($1, $2, $3, now(), $4, 0), ($1, $5, $3, now(), $4, 1)`, f.eventID, captainID, int16(participantModel.StatusApproved), f.blueID, member.ID)
	exec(`UPDATE event_participants SET pseudonym = 'Neo' WHERE event_id = $1 AND user_id = $2`, f.eventID, member.ID)

	// The static challenge requires the (never opened) infrastructure one.
	exec(`INSERT INTO event_challenge_prerequisites (challenge_id, prerequisite_challenge_id) VALUES ($1, $2)`, f.staticChallenge, f.infraOne)
	board, err := uc.ListOwnChallenges(ctx, f.eventID, captainID)
	if err != nil || len(board) != 1 {
		t.Fatalf("board = %+v, %v", board, err)
	}
	locked := board[0]
	if !locked.Locked || len(locked.Prerequisites) != 1 || locked.Prerequisites[0].EventChallengeID != f.infraOne ||
		locked.Prerequisites[0].Name != "Find the flag" || locked.Prerequisites[0].Solved || len(locked.Files) != 0 ||
		strings.Contains(string(locked.Snapshot), "description") || locked.SolveCount == nil || *locked.SolveCount != 0 {
		t.Fatalf("locked challenge = %+v (%s)", locked, locked.Snapshot)
	}
	exec(`DELETE FROM event_challenge_prerequisites WHERE challenge_id = $1`, f.staticChallenge)

	// A file attached to Blue's snapshot, plus one without a media row.
	fileID, missingID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	if _, err = f.db.Queries.CreateFile(ctx, postgres.CreateFileParams{ID: fileID, Name: "brief.pdf", ContentType: "application/pdf", SizeBytes: 12345, ContentHash: strings.Repeat("a", 64), CreatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	exec(`UPDATE team_challenges SET snapshot = jsonb_set(snapshot, '{attachments}', jsonb_build_array(
			jsonb_build_object('file_id', $3::text, 'name', 'brief.pdf'), jsonb_build_object('file_id', $4::text, 'name', 'gone.bin')))
		WHERE event_team_id = $1 AND event_challenge_id = $2`, f.blueID, f.staticChallenge, fileID.String(), missingID.String())

	// Red solved it.
	exec(`INSERT INTO team_challenge_solves (team_challenge_id, solved_at)
		SELECT id, now() FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, f.redID, f.staticChallenge)
	board, err = uc.ListOwnChallenges(ctx, f.eventID, captainID)
	if err != nil || len(board) != 1 {
		t.Fatalf("board = %+v, %v", board, err)
	}
	open := board[0]
	if open.Locked || len(open.Files) != 2 || open.Files[0].Size != 12345 || open.Files[1].Size != 0 || open.SolveCount == nil || *open.SolveCount != 1 || open.Infrastructure {
		t.Fatalf("open challenge = %+v", open)
	}
	// Hints: the task decides; the event can only hide them for every task.
	exec(`UPDATE event_challenges SET hints_enabled = true, hints = '[{"id":"01900000-0000-7000-8000-00000000abcd","level":"nudge","text":"x"}]'::jsonb WHERE id = $1`, f.staticChallenge)
	board, err = uc.ListOwnChallenges(ctx, f.eventID, captainID)
	if err != nil || !board[0].HintsEnabled || len(board[0].Hints) != 1 {
		t.Fatalf("task hints = %+v, %v", board, err)
	}
	exec(`UPDATE event_configs SET hints_disabled = true WHERE event_id = $1`, f.eventID)
	board, err = uc.ListOwnChallenges(ctx, f.eventID, captainID)
	if err != nil || board[0].HintsEnabled || len(board[0].Hints) != 0 {
		t.Fatalf("event-disabled hints = %+v, %v", board, err)
	}
	exec(`UPDATE event_configs SET hints_disabled = false WHERE event_id = $1`, f.eventID)
	page, err := uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30)
	solves := page.Items
	if err != nil || len(solves) != 1 || solves[0].TeamName != "Red" || solves[0].Own || !solves[0].FirstBlood || page.Total != 1 || page.HasMore {
		t.Fatalf("solves = %+v, %v", solves, err)
	}
	if _, err = uc.ListChallengeSolves(ctx, f.eventID, captainID, f.infraOne, uuid.Nil, 30); !errors.Is(err, eventChallengeModel.ErrEventChallengeNotFound.Err()) {
		t.Fatalf("a challenge off the caller's board: %v", err)
	}

	// A hidden team leaves the population; the caller's own team never does.
	exec(`UPDATE event_teams SET hidden = true WHERE id = $1`, f.redID)
	exec(`UPDATE event_teams SET hidden = true WHERE id = $1`, f.blueID)
	exec(`INSERT INTO team_challenge_solves (team_challenge_id, solved_at)
		SELECT id, now() FROM team_challenges WHERE event_team_id = $1 AND event_challenge_id = $2`, f.blueID, f.staticChallenge)
	board, err = uc.ListOwnChallenges(ctx, f.eventID, captainID)
	if err != nil || board[0].SolveCount == nil || *board[0].SolveCount != 1 {
		t.Fatalf("hidden population = %+v, %v", board, err)
	}
	page, err = uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30)
	solves = page.Items
	if err != nil || len(solves) != 1 || solves[0].FirstBlood || !solves[0].Own || solves[0].TeamName != "Blue" {
		t.Fatalf("own solve = %+v, %v", solves, err)
	}

	// Hidden results: no counts, and the solves route is denied.
	exec(`UPDATE event_configs SET scoreboard_visibility = $2 WHERE event_id = $1`, f.eventID, int16(eventConfigModel.VisibilityHidden))
	board, err = uc.ListOwnChallenges(ctx, f.eventID, captainID)
	if err != nil || board[0].SolveCount != nil {
		t.Fatalf("hidden results = %+v, %v", board, err)
	}
	if _, err = uc.ListChallengeSolves(ctx, f.eventID, captainID, f.staticChallenge, uuid.Nil, 30); !errors.Is(err, eventConfigModel.ErrResultsHidden.Err()) {
		t.Fatalf("solves with hidden results: %v", err)
	}

	roster, err := uc.ListOwnTeamMembers(ctx, f.eventID, member.ID)
	if err != nil || len(roster) != 2 || roster[0].UserID != captainID || roster[0].Role != participantModel.TeamRoleCaptain ||
		roster[1].DisplayName != "Neo" || !roster[1].Own || roster[0].Own {
		t.Fatalf("roster = %+v, %v", roster, err)
	}
}
