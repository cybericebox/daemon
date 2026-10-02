package event_test

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	challengeAttemptModel "github.com/cybericebox/daemon/internal/model/challengeAttempt"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/model/rbac"
	event "github.com/cybericebox/daemon/internal/useCase/event"
)

// frozenConfig freezes 90 minutes before the finish; startedEvent finishes an
// hour after now, so the freeze started 30 minutes ago.
func frozenConfig(eventID uuid.UUID, now time.Time, rows int32) postgres.EventConfig {
	return postgres.EventConfig{EventID: eventID, ScoreboardVisibility: int16(eventConfigModel.VisibilityPublic), CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true},
		ResultsFreezeEnabled: true, ResultsFreezeMinutes: 90, ResultsLiveFreeze: true, ResultsChartEnabled: true, ResultsChartTeams: 1,
		ResultsRowsLimit: pgtype.Int4{Int32: rows, Valid: true}}
}

func TestGetResultsSnapshotFreezesOtherTeamsButNotOwnProgress(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, userID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	leader, second, own := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	started := startedEvent(eventID, now)
	frozenAt := started.FinishAt.Time.Add(-90 * time.Minute)
	cutoff := pgtype.Timestamptz{Time: frozenAt, Valid: true}
	ownParam := uuid.NullUUID{UUID: own, Valid: true}

	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 1), nil)
	q.EXPECT().GetEventManager(gomock.Any(), gomock.Any()).Return(postgres.EventManager{}, pgx.ErrNoRows)
	q.EXPECT().GetEventParticipant(gomock.Any(), gomock.Any()).Return(postgres.EventParticipant{EventID: eventID, UserID: userID, Status: 2, TeamID: ownParam, CreatedAt: now}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 3, UpdatedAt: now}, nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID, Cutoff: cutoff}).Return([]postgres.ListEventScoreboardRow{
		{TeamID: leader, TeamName: "Leader", Points: 300, Solved: 2}, {TeamID: second, TeamName: "Second", Points: 200, Solved: 1}, {TeamID: own, TeamName: "Own", Points: 100, Solved: 1},
	}, nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID, Cutoff: cutoff, IncludeTeam: ownParam}).Return([]postgres.ListEventScoreboardRow{
		{TeamID: leader, TeamName: "Leader", Points: 300, Solved: 2}, {TeamID: own, TeamName: "Own", Points: 400, Solved: 3}, {TeamID: second, TeamName: "Second", Points: 200, Solved: 1},
	}, nil)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID, Cutoff: cutoff}).Return([]postgres.ListEventScoreTimelineRow{
		{EventTeamID: leader, Points: 300, SolvedAt: frozenAt.Add(-time.Hour)}, {EventTeamID: second, Points: 200, SolvedAt: frozenAt.Add(-50 * time.Minute)}, {EventTeamID: own, Points: 100, SolvedAt: frozenAt.Add(-40 * time.Minute)},
	}, nil)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID, Cutoff: cutoff, IncludeTeam: ownParam}).Return([]postgres.ListEventScoreTimelineRow{
		{EventTeamID: own, Points: 100, SolvedAt: frozenAt.Add(-40 * time.Minute)}, {EventTeamID: own, Points: 300, SolvedAt: frozenAt.Add(10 * time.Minute)},
	}, nil)

	got, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{UserID: &userID, Role: rbac.RoleUser}, false)
	if err != nil {
		t.Fatalf("GetResultsSnapshot: %v", err)
	}
	if !got.Freeze.Active || !got.Freeze.Applied || got.Freeze.FrozenAt == nil || !got.Freeze.FrozenAt.Equal(frozenAt) {
		t.Fatalf("freeze = %+v", got.Freeze)
	}
	// Rows limit 1: the leader plus the own row appended below it.
	if got.TotalTeams != 3 || len(got.Scoreboard) != 2 || got.Scoreboard[0].TeamID != leader || got.Scoreboard[1].TeamID != own {
		t.Fatalf("rows = %+v", got.Scoreboard)
	}
	if ownRow := got.Scoreboard[1]; ownRow.Rank != 3 || ownRow.Points != 400 || ownRow.Solved != 3 {
		t.Fatalf("own row must keep its frozen rank with live progress: %+v", ownRow)
	}
	// Chart teams 1: the leader and the own team (with its later solve).
	if len(got.Timeline) != 3 || got.Timeline[2].EventTeamID != own || got.Timeline[2].Points != 300 {
		t.Fatalf("timeline = %+v", got.Timeline)
	}
	for _, item := range got.Timeline {
		if item.EventTeamID == second {
			t.Fatalf("a team outside the chart must not be in the timeline: %+v", got.Timeline)
		}
	}
}

func TestGetResultsSnapshotShowsModeratorsLiveDataDuringFreeze(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 1000), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 1, UpdatedAt: now}, nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID}).Return(nil, nil)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID}).Return(nil, nil)

	got, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{Role: rbac.RoleSuperAdmin}, false)
	if err != nil || !got.Freeze.Active || got.Freeze.Applied {
		t.Fatalf("moderator snapshot = %+v, %v", got.Freeze, err)
	}
}

func TestGetResultsSnapshotLiveScreenFollowsLiveFreeze(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	cfg := frozenConfig(eventID, now, 1)
	cfg.ResultsLiveFreeze = false
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(cfg, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 1, UpdatedAt: now}, nil)
	teams := []postgres.ListEventScoreboardRow{{TeamID: uuid.Must(uuid.NewV7())}, {TeamID: uuid.Must(uuid.NewV7())}}
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID}).Return(teams, nil)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID}).Return(nil, nil)

	got, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{Role: rbac.RoleSuperAdmin}, true)
	if err != nil || got.Freeze.Applied || len(got.Scoreboard) != 2 {
		t.Fatalf("live screen = %+v rows %d, %v", got.Freeze, len(got.Scoreboard), err)
	}
}

// The live screen is opened by a moderator, yet it shows the audience view:
// with LiveFreeze on the freeze applies to it.
func TestGetResultsSnapshotLiveScreenFreezesForModerator(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	started := startedEvent(eventID, now)
	cutoff := pgtype.Timestamptz{Time: started.FinishAt.Time.Add(-90 * time.Minute), Valid: true}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 1), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 1, UpdatedAt: now}, nil)
	q.EXPECT().ListEventScoreboard(gomock.Any(), postgres.ListEventScoreboardParams{EventID: eventID, Cutoff: cutoff}).Return(nil, nil)
	q.EXPECT().ListEventScoreTimeline(gomock.Any(), postgres.ListEventScoreTimelineParams{EventID: eventID, Cutoff: cutoff}).Return(nil, nil)

	got, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{Role: rbac.RoleSuperAdmin}, true)
	if err != nil || !got.Freeze.Applied {
		t.Fatalf("live screen = %+v, %v", got.Freeze, err)
	}
}

func TestGetResultsSnapshotLiveScreenIsForManagersOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 1), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	// The shared event-wide read includes the revision, even for a denied reader.
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{}, nil)

	_, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{}, true)
	if !errors.Is(err, eventConfigModel.ErrResultsLiveScreenManagersOnly.Err()) {
		t.Fatalf("anonymous live screen error = %v", err)
	}
}

func TestReplayLiveResultsSkipsOtherTeamsSolvesAfterFreeze(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	started := startedEvent(eventID, now)
	frozenAt := started.FinishAt.Time.Add(-90 * time.Minute)
	team := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return([]postgres.ListEventScoreboardRow{{TeamID: team, TeamName: "Blue"}}, nil).AnyTimes()
	change := func(revision int64, at time.Time) postgres.EventResultChange {
		payload, _ := json.Marshal(map[string]any{"TeamID": team, "EventChallengeID": uuid.Must(uuid.NewV7()), "SolvedAt": at})
		return postgres.EventResultChange{EventID: eventID, Revision: revision, Kind: "team_challenge_solved", Payload: payload, CreatedAt: at}
	}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 10), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 6, UpdatedAt: now}, nil)
	q.EXPECT().GetEarliestEventResultChangeRevision(gomock.Any(), eventID).Return(int64(5), nil)
	q.EXPECT().ListEventResultChangesAfter(gomock.Any(), gomock.Any()).Return([]postgres.EventResultChange{change(5, frozenAt.Add(-time.Minute)), change(6, frozenAt.Add(time.Minute))}, nil)

	replay, err := uc.ReplayLiveResults(context.Background(), eventID, event.ResultsAccess{}, 4, false)
	if err != nil || len(replay.Changes) != 1 || replay.Changes[0].Revision != 5 || replay.LastRevision != 6 || replay.FreezeKey == "" {
		t.Fatalf("replay = %+v, %v", replay, err)
	}
}

// L19: the withdrawal of another team's solve during the freeze is news about that team, like the solve was.
func TestReplayLiveResultsSkipsOtherTeamsAnnulmentsAfterFreeze(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	started := startedEvent(eventID, now)
	frozenAt := started.FinishAt.Time.Add(-90 * time.Minute)
	team := uuid.Must(uuid.NewV7())
	q.EXPECT().ListEventScoreboard(gomock.Any(), gomock.Any()).Return([]postgres.ListEventScoreboardRow{{TeamID: team, TeamName: "Blue"}}, nil).AnyTimes()
	unsolved := func(revision int64, at time.Time) postgres.EventResultChange {
		payload, _ := json.Marshal(map[string]any{"TeamID": team, "EventChallengeID": uuid.Must(uuid.NewV7()), "SolvedAt": nil})
		return postgres.EventResultChange{EventID: eventID, Revision: revision, Kind: "team_challenge_unsolved", Payload: payload, CreatedAt: at}
	}
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 10), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(started, nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 6, UpdatedAt: now}, nil)
	q.EXPECT().GetEarliestEventResultChangeRevision(gomock.Any(), eventID).Return(int64(5), nil)
	q.EXPECT().ListEventResultChangesAfter(gomock.Any(), gomock.Any()).Return([]postgres.EventResultChange{unsolved(5, frozenAt.Add(-time.Minute)), unsolved(6, frozenAt.Add(time.Minute))}, nil)

	replay, err := uc.ReplayLiveResults(context.Background(), eventID, event.ResultsAccess{}, 4, false)
	if err != nil || len(replay.Changes) != 1 || replay.Changes[0].Revision != 5 {
		t.Fatalf("only the annulment before the freeze may be replayed, got %+v, %v", replay, err)
	}
}

func TestGetManageResultsRanksOnlyVisibleAdmittedTeams(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 10), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 2, UpdatedAt: now}, nil)
	neo, second, challengeID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().ListManageScoreboard(gomock.Any(), eventID).Return([]postgres.ListManageScoreboardRow{
		{TeamID: uuid.Must(uuid.NewV7()), Hidden: true, Admitted: true, PublicName: "Hidden", Points: 900},
		{TeamID: neo, Admitted: true, Individual: true, PublicName: "neo", RealName: "Олена Коваль", Pseudonym: "neo", Points: 500},
		{TeamID: uuid.Must(uuid.NewV7()), Admitted: false, PublicName: "Small", Points: 400},
		{TeamID: second, Admitted: true, PublicName: "Second", Points: 100},
	}, nil)
	q.EXPECT().ListManageScoreSolves(gomock.Any(), eventID).Return([]postgres.ListManageScoreSolvesRow{
		{EventTeamID: neo, EventChallengeID: challengeID, ChallengeName: "Web 1", Points: 300, SolvedAt: now.Add(-time.Hour), FirstBlood: true},
		{EventTeamID: second, EventChallengeID: challengeID, ChallengeName: "Web 1", Points: 100, SolvedAt: now.Add(-30 * time.Minute)},
		{EventTeamID: neo, EventChallengeID: uuid.Must(uuid.NewV7()), ChallengeName: "Crypto 1", Points: 200, SolvedAt: now.Add(-10 * time.Minute)},
	}, nil)
	q.EXPECT().ListManageHintTotals(gomock.Any(), eventID).Return([]postgres.ListManageHintTotalsRow{{EventTeamID: second, Hints: 2, Charged: 50}}, nil)

	got, err := uc.GetManageResults(context.Background(), eventID)
	if err != nil {
		t.Fatalf("GetManageResults: %v", err)
	}
	if got.Counts != (event.ManageResultsCountsView{Ranked: 2, Hidden: 1, NotAdmitted: 1}) || !got.Freeze.Active || got.Freeze.Applied {
		t.Fatalf("counts %+v freeze %+v", got.Counts, got.Freeze)
	}
	if got.Teams[0].Rank != nil || got.Teams[2].Rank != nil || *got.Teams[1].Rank != 1 || *got.Teams[3].Rank != 2 {
		t.Fatalf("ranks = %+v", got.Teams)
	}
	if got.Teams[1].Pseudonym == nil || *got.Teams[1].Pseudonym != "neo" || got.Teams[1].RealName != "Олена Коваль" || got.Teams[3].Pseudonym != nil {
		t.Fatalf("names = %+v", got.Teams)
	}
	neoRow, secondRow := got.Teams[1], got.Teams[3]
	if len(neoRow.Solves) != 2 || !neoRow.Solves[0].FirstBlood || neoRow.Solves[1].ChallengeName != "Crypto 1" || neoRow.Hints != 0 {
		t.Fatalf("neo solves = %+v", neoRow)
	}
	if len(secondRow.Solves) != 1 || secondRow.Solves[0].FirstBlood || secondRow.Hints != 2 || secondRow.HintPoints != 50 {
		t.Fatalf("second solves = %+v", secondRow)
	}
	if got.Teams[0].Solves == nil || len(got.Teams[0].Solves) != 0 {
		t.Fatalf("a team without solves lists none: %+v", got.Teams[0])
	}
}

func TestAnnulSolveRejectsEveryAcceptedAttemptInOneTransaction(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	eventID, teamID, challengeID, by := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	teamChallengeID := uuid.Must(uuid.NewV7())
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	var decided []uuid.UUID
	gomock.InOrder(
		q.EXPECT().LockEventTeamChallenge(gomock.Any(), postgres.LockEventTeamChallengeParams{EventID: eventID, EventTeamID: teamID, EventChallengeID: challengeID}).Return(teamChallengeID, nil),
		q.EXPECT().ListEffectiveCorrectAttemptIDs(gomock.Any(), teamChallengeID).Return([]uuid.UUID{first, second}, nil),
		q.EXPECT().CreateChallengeAttemptDecision(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateChallengeAttemptDecisionParams) (postgres.ChallengeAttemptDecision, error) {
			if p.Decision != int16(challengeAttemptModel.DecisionRejected) || p.Reason != "Спільний прапор" || p.DecidedBy != by {
				t.Fatalf("decision = %+v", p)
			}
			decided = append(decided, p.ChallengeAttemptID)
			return postgres.ChallengeAttemptDecision{}, nil
		}).Times(2),
		q.EXPECT().GetEffectiveTeamChallengeSolvedAt(gomock.Any(), teamChallengeID).Return(postgres.GetEffectiveTeamChallengeSolvedAtRow{}, nil),
		q.EXPECT().DeleteTeamChallengeSolve(gomock.Any(), teamChallengeID).Return(nil),
		q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{Revision: 9}, nil),
		q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CreateEventResultChangeParams) (postgres.EventResultChange, error) {
			if p.Kind != "scoreboard_recalculated" {
				t.Fatalf("change kind = %s", p.Kind)
			}
			return postgres.EventResultChange{EventID: eventID, Revision: 9, Kind: p.Kind}, nil
		}),
	)

	got, err := uc.AnnulSolve(context.Background(), eventID, teamID, challengeID, " Спільний прапор ", by)
	if err != nil || got.Rejected != 2 || !unit.saved || len(decided) != 2 || decided[0] != first || decided[1] != second {
		t.Fatalf("AnnulSolve = %+v, %v, saved %v", got, err, unit.saved)
	}
}

func TestAnnulSolveWithoutAcceptedAttemptIsAConflict(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	unit := &testUoW{}
	uc := event.NewEventUseCase(event.Dependencies{Repo: q, UoW: testUnitOfWorker{repo: q, unit: unit}})
	q.EXPECT().LockEventTeamChallenge(gomock.Any(), gomock.Any()).Return(uuid.Nil, pgx.ErrNoRows)

	_, err := uc.AnnulSolve(context.Background(), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), "reason", uuid.Must(uuid.NewV7()))
	if !errors.Is(err, challengeAttemptModel.ErrNothingToAnnul.Err()) || unit.saved {
		t.Fatalf("err = %v, saved %v", err, unit.saved)
	}
}

func TestUpdateResultsSettingsReloadsLiveClients(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID, by := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, CreatedAt: now, UpdatedAt: pgtype.Timestamptz{Time: now, Valid: true}}, nil)
	q.EXPECT().UpdateEventConfig(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateEventConfigParams) (int64, error) {
		if !p.ResultsFreezeEnabled || p.ResultsFreezeMinutes != 30 || p.ResultsChartTeams != 5 || !p.ResultsRowsLimit.Valid || p.ResultsRowsLimit.Int32 != 20 ||
			p.ScoreboardVisibility != int16(eventConfigModel.VisibilityPublic) {
			t.Fatalf("written = %+v", p)
		}
		return 1, nil
	})
	q.EXPECT().AdvanceEventResultRevision(gomock.Any(), gomock.Any()).Return(postgres.AdvanceEventResultRevisionRow{Revision: 1}, nil)
	q.EXPECT().CreateEventResultChange(gomock.Any(), gomock.Any()).Return(postgres.EventResultChange{}, nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(startedEvent(eventID, now), nil)

	rows := int32(20)
	got, err := uc.UpdateResultsSettings(context.Background(), eventID, eventConfigModel.VisibilityPublic,
		eventConfigModel.ResultsSettings{FreezeEnabled: true, FreezeMinutes: 30, LiveFreeze: true, ChartEnabled: true, ChartTeams: 5, RowsLimit: &rows}, by)
	if err != nil || got.FreezeMinutes != 30 || got.Freeze.FrozenAt == nil {
		t.Fatalf("UpdateResultsSettings = %+v, %v", got, err)
	}
}

// L19: the results route is public and takes the event id from the URL: an
// unpublished event's teams and scores are not for the public.
func TestGetResultsSnapshotOfAnUnpublishedEventIsNotFound(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := newFormGateMock(ctrl)
	uc := event.NewEventUseCase(event.Dependencies{Repo: q})
	eventID := uuid.Must(uuid.NewV7())
	now := time.Now().UTC()
	notPublished := startedEvent(eventID, now)
	notPublished.LifecycleConfigured = false
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(frozenConfig(eventID, now, 10), nil)
	q.EXPECT().GetEventByID(gomock.Any(), eventID).Return(notPublished, nil)
	q.EXPECT().GetEventResultRevision(gomock.Any(), eventID).Return(postgres.GetEventResultRevisionRow{Revision: 3, UpdatedAt: now}, nil)

	_, err := uc.GetResultsSnapshot(context.Background(), eventID, event.ResultsAccess{}, false)
	if !errors.Is(err, eventModel.ErrEventNotFound.Err()) {
		t.Fatalf("an anonymous read of an unpublished event must be not-found, got %v", err)
	}
}
