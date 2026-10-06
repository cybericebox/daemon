package eventAnalytics_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	"github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// taskStore serves the «Завдання» and «Прогрес» reads; the rest of Store is
// nil and panics if a report touches it.
type taskStore struct {
	eventAnalytics.Store
	challenges []eventAnalyticsRepo.Challenge
	stats      []eventAnalyticsRepo.TaskStats
	failed     []eventAnalyticsRepo.FailedTeam
	hints      []eventAnalyticsRepo.HintEffectRow
	wrong      []eventAnalyticsRepo.WrongAnswer
	teams      []eventAnalyticsRepo.RankedTeam
	scores     []eventAnalyticsRepo.ScoreEvent
	scoreIDs   []uuid.UUID
	cells      []eventAnalyticsRepo.MatrixCell
	heat       []eventAnalyticsRepo.HeatCell
	activity   []eventAnalyticsRepo.TeamActivity
	activityAt time.Time
	statCalls  int
}

func (s *taskStore) Challenges(context.Context, uuid.UUID) ([]eventAnalyticsRepo.Challenge, error) {
	return s.challenges, nil
}
func (s *taskStore) Challenge(_ context.Context, _, id uuid.UUID) (eventAnalyticsRepo.Challenge, bool, error) {
	for _, c := range s.challenges {
		if c.ID == id {
			return c, true, nil
		}
	}
	return eventAnalyticsRepo.Challenge{}, false, nil
}
func (s *taskStore) TaskStats(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.TaskStats, error) {
	s.statCalls++
	return s.stats, nil
}
func (s *taskStore) TaskSeries(context.Context, uuid.UUID, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.SeriesPoint, error) {
	return []eventAnalyticsRepo.SeriesPoint{{At: time.Date(2026, 9, 29, 11, 55, 0, 0, time.UTC), Attempts: 2, Solves: 1}}, nil
}
func (s *taskStore) TaskFailedTeams(context.Context, uuid.UUID, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.FailedTeam, error) {
	return s.failed, nil
}
func (s *taskStore) TaskWrongAnswers(context.Context, uuid.UUID, uuid.UUID, eventAnalyticsModel.Period, int32) ([]eventAnalyticsRepo.WrongAnswer, error) {
	return s.wrong, nil
}
func (s *taskStore) TaskHintEffect(context.Context, uuid.UUID, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.HintEffectRow, error) {
	return s.hints, nil
}
func (s *taskStore) RankedTeams(context.Context, uuid.UUID) ([]eventAnalyticsRepo.RankedTeam, error) {
	return s.teams, nil
}
func (s *taskStore) ScoreEvents(_ context.Context, _ uuid.UUID, ids []uuid.UUID) ([]eventAnalyticsRepo.ScoreEvent, error) {
	s.scoreIDs = ids
	return s.scores, nil
}
func (s *taskStore) Matrix(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.MatrixCell, error) {
	return s.cells, nil
}
func (s *taskStore) Heatmap(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.HeatCell, error) {
	return s.heat, nil
}
func (s *taskStore) TeamActivity(_ context.Context, _ uuid.UUID, asOf time.Time) ([]eventAnalyticsRepo.TeamActivity, error) {
	s.activityAt = asOf
	return s.activity, nil
}
func (s *taskStore) RollupState(context.Context, uuid.UUID) (eventAnalyticsRepo.RollupState, error) {
	return eventAnalyticsRepo.RollupState{}, nil
}

func newTaskUC(store *taskStore, event eventModel.Event, clock *time.Time) *eventAnalytics.EventAnalyticsUseCase {
	return eventAnalytics.New(eventAnalytics.Dependencies{
		Store: store, Events: fakeEvents{event}, Configs: fakeConfigs{eventConfigModel.EventConfig{}},
		Now: func() time.Time { return *clock },
	})
}

func ptr[T any](v T) *T { return &v }

func id(n byte) uuid.UUID { return uuid.UUID{15: n} }

func TestGetEventAnalyticsTasks_CalibratesAndSumsGroups(t *testing.T) {
	clock := now
	event := runningEvent()
	web, misc := id(100), id(101)
	store := &taskStore{
		challenges: []eventAnalyticsRepo.Challenge{
			{ID: id(1), Name: "Easy web", Difficulty: "easy", GroupID: web, GroupName: "Web"},
			{ID: id(2), Name: "Hard web", Difficulty: "hard", GroupID: web, GroupName: "Web"},
			{ID: id(3), Name: "Misc", Difficulty: "medium"},
			{ID: id(4), Name: "Rare", Difficulty: "medium", GroupID: misc, GroupName: "Misc"},
		},
		stats: []eventAnalyticsRepo.TaskStats{
			{ChallengeID: id(1), Attempts: 20, TeamsTried: 10, Solves: 2, MedianSinceStart: ptr(int64(600)), FirstBloodTeam: "Blue"},
			{ChallengeID: id(2), Attempts: 30, TeamsTried: 10, Solves: 9},
			{ChallengeID: id(3), Attempts: 6, TeamsTried: 2, Solves: 1},
		},
	}
	v, err := newTaskUC(store, event, &clock).GetEventAnalyticsTasks(context.Background(), event.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Tasks) != 4 {
		t.Fatalf("tasks: %+v", v.Tasks)
	}
	verdicts := []string{v.Tasks[0].Calibration.Verdict, v.Tasks[1].Calibration.Verdict, v.Tasks[2].Calibration.Verdict, v.Tasks[3].Calibration.Verdict}
	want := []string{eventAnalyticsModel.CalibrationTooHard, eventAnalyticsModel.CalibrationTooEasy, eventAnalyticsModel.CalibrationInsufficient, eventAnalyticsModel.CalibrationInsufficient}
	for i := range want {
		if verdicts[i] != want[i] {
			t.Fatalf("verdicts = %v, want %v", verdicts, want)
		}
	}
	if v.Tasks[0].SolveRate != 0.2 || v.Tasks[0].MedianSinceStart == nil || *v.Tasks[0].MedianSinceStart != 600 || v.Tasks[3].SolveRate != 0 {
		t.Fatalf("row: %+v", v.Tasks[0])
	}
	// Web (2 tasks, 20 team-task pairs, 11 solves), then no group, then Misc.
	if len(v.Groups) != 3 || v.Groups[0].GroupName != "Web" || v.Groups[0].Tasks != 2 || v.Groups[0].TeamsTried != 20 || v.Groups[0].Solves != 11 || v.Groups[0].SolveRate != 0.55 {
		t.Fatalf("groups: %+v", v.Groups)
	}
}

func TestGetEventAnalyticsTasks_IsCachedPerPeriod(t *testing.T) {
	clock := now
	event := runningEvent()
	store := &taskStore{challenges: []eventAnalyticsRepo.Challenge{{ID: id(1), Name: "A"}}}
	uc := newTaskUC(store, event, &clock)
	for range 3 {
		if _, err := uc.GetEventAnalyticsTasks(context.Background(), event.ID, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	if store.statCalls != 1 {
		t.Fatalf("stats loaded %d times, want 1", store.statCalls)
	}
	from := event.Lifecycle.StartAt.Add(5 * time.Minute)
	if _, err := uc.GetEventAnalyticsTasks(context.Background(), event.ID, &from, nil); err != nil || store.statCalls != 2 {
		t.Fatalf("another period must load again: calls=%d err=%v", store.statCalls, err)
	}
}

func TestGetEventAnalyticsTaskDetail(t *testing.T) {
	clock := now
	event := runningEvent()
	store := &taskStore{
		challenges: []eventAnalyticsRepo.Challenge{{ID: id(1), Name: "Web 1", Difficulty: "medium"}},
		failed:     []eventAnalyticsRepo.FailedTeam{{TeamID: id(9), TeamName: "Red", Attempts: 7}},
		hints: []eventAnalyticsRepo.HintEffectRow{
			{TeamID: id(1), Hinted: true, Solved: true, SinceHint: ptr(int64(100))},
			{TeamID: id(2), Hinted: true, Solved: true, SinceHint: ptr(int64(300))},
			{TeamID: id(3), Hinted: true},
			{TeamID: id(4), Solved: true, SinceStart: ptr(int64(60))},
			{TeamID: id(5)},
		},
	}
	uc := newTaskUC(store, event, &clock)

	v, err := uc.GetEventAnalyticsTaskDetail(context.Background(), event.ID, id(1), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if v.Task.Name != "Web 1" || len(v.FailedTeams) != 1 || v.FailedTeams[0].TeamName != "Red" {
		t.Fatalf("detail: %+v", v)
	}
	// 11:50 → 12:05: three dense buckets, the stored one in the middle.
	if len(v.Series) != 3 || v.Series[1].Solves != 1 {
		t.Fatalf("series: %+v", v.Series)
	}
	with, without := v.HintEffect.With, v.HintEffect.Without
	if with.Teams != 3 || with.Solved != 2 || with.MedianSeconds == nil || *with.MedianSeconds != 200 {
		t.Fatalf("with hints: %+v", with)
	}
	if without.Teams != 2 || without.Solved != 1 || without.SolveRate != 0.5 || without.MedianSeconds == nil || *without.MedianSeconds != 60 {
		t.Fatalf("without hints: %+v", without)
	}

	if _, err := uc.GetEventAnalyticsTaskDetail(context.Background(), event.ID, id(77), nil, nil); err == nil {
		t.Fatal("an unknown task must be not found")
	}
}

func TestGetEventAnalyticsTaskWrongAnswers(t *testing.T) {
	clock := now
	event := runningEvent()
	store := &taskStore{
		challenges: []eventAnalyticsRepo.Challenge{{ID: id(1), Name: "Web 1"}},
		wrong:      []eventAnalyticsRepo.WrongAnswer{{Answer: "ICE{a}", Attempts: 5, Teams: 3}},
	}
	uc := newTaskUC(store, event, &clock)
	v, err := uc.GetEventAnalyticsTaskWrongAnswers(context.Background(), event.ID, id(1), nil, nil)
	if err != nil || len(v.Answers) != 1 || v.Answers[0].Answer != "ICE{a}" || v.Answers[0].Teams != 3 {
		t.Fatalf("answers: %+v err=%v", v, err)
	}
	if _, err := uc.GetEventAnalyticsTaskWrongAnswers(context.Background(), event.ID, id(2), nil, nil); err == nil {
		t.Fatal("an unknown task must be not found")
	}
}

func TestGetEventAnalyticsProgressScores_TopAndChosenTeams(t *testing.T) {
	clock := now
	event := runningEvent()
	start := event.Lifecycle.StartAt
	store := &taskStore{
		teams: []eventAnalyticsRepo.RankedTeam{
			{ID: id(1), Name: "Blue", Points: 300, Admitted: true},
			{ID: id(2), Name: "Ghost", Points: 250, Admitted: true, Hidden: true},
			{ID: id(3), Name: "Red", Points: 200, Admitted: true},
			{ID: id(4), Name: "Green", Points: 100, Admitted: true},
		},
		scores: []eventAnalyticsRepo.ScoreEvent{
			{TeamID: id(1), At: start.Add(-time.Hour), Points: 50},
			{TeamID: id(1), At: start.Add(5 * time.Minute), Points: 100},
			{TeamID: id(1), At: start.Add(6 * time.Minute), Points: -10},
			{TeamID: id(4), At: start.Add(7 * time.Minute), Points: 100},
		},
	}
	uc := newTaskUC(store, event, &clock)

	// top 1 is Blue; Green is chosen; hidden Ghost never counts toward top.
	v, err := uc.GetEventAnalyticsProgressScores(context.Background(), event.ID, nil, nil, 1, []uuid.UUID{id(4), id(99)})
	if err != nil {
		t.Fatal(err)
	}
	if len(store.scoreIDs) != 2 || store.scoreIDs[0] != id(1) || store.scoreIDs[1] != id(4) {
		t.Fatalf("scored teams: %v", store.scoreIDs)
	}
	if len(v.Teams) != 4 || v.Teams[0].Rank != 1 || v.Teams[1].Rank != 0 || v.Teams[2].Rank != 2 || !v.Teams[3].Selected || v.Teams[2].Selected {
		t.Fatalf("teams: %+v", v.Teams)
	}
	blue := v.Series[0]
	// baseline 50 at the period start, then 150, then 140, then the end (now).
	if blue.Name != "Blue" || len(blue.Points) != 4 || blue.Points[0].Score != 50 || blue.Points[1].Score != 150 || blue.Points[2].Score != 140 || blue.Points[3].Score != 140 {
		t.Fatalf("blue: %+v", blue.Points)
	}
	if !blue.Points[3].At.Equal(now) {
		t.Fatalf("the running line ends now, got %s", blue.Points[3].At)
	}
}

func TestGetEventAnalyticsProgressMatrix_KeepsRankedTeamsAndKnownTasks(t *testing.T) {
	clock := now
	event := runningEvent()
	solved := now.Add(-time.Minute)
	store := &taskStore{
		challenges: []eventAnalyticsRepo.Challenge{{ID: id(1), Name: "A"}, {ID: id(2), Name: "B"}},
		teams:      []eventAnalyticsRepo.RankedTeam{{ID: id(1), Name: "Blue", Admitted: true}, {ID: id(2), Name: "Ghost", Admitted: true, Hidden: true}},
		cells: []eventAnalyticsRepo.MatrixCell{
			{TeamID: id(1), ChallengeID: id(1), Attempts: 3, SolvedAt: &solved},
			{TeamID: id(1), ChallengeID: id(2), Attempts: 2},
			{TeamID: id(2), ChallengeID: id(1), Attempts: 9},
			{TeamID: id(1), ChallengeID: id(50), Attempts: 1},
		},
	}
	v, err := newTaskUC(store, event, &clock).GetEventAnalyticsProgressMatrix(context.Background(), event.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Teams) != 1 || len(v.Tasks) != 2 || len(v.Cells) != 2 || v.Cells[0].SolvedAt == nil || v.Cells[1].SolvedAt != nil {
		t.Fatalf("matrix: %+v", v)
	}
}

func TestGetEventAnalyticsProgressHeatmap_HoursAndScale(t *testing.T) {
	clock := now
	event := runningEvent()
	store := &taskStore{
		teams: []eventAnalyticsRepo.RankedTeam{{ID: id(1), Name: "Blue", Admitted: true}},
		heat: []eventAnalyticsRepo.HeatCell{
			{HourAt: time.Date(2026, 9, 29, 11, 0, 0, 0, time.UTC), TeamID: id(1), Attempts: 4, Opens: 2, Solves: 1},
			{HourAt: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), TeamID: id(1), Attempts: 1},
			{HourAt: time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC), TeamID: id(8), Attempts: 100},
		},
	}
	v, err := newTaskUC(store, event, &clock).GetEventAnalyticsProgressHeatmap(context.Background(), event.ID, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	// 11:50 → 12:05 touches the 11:00 and 12:00 hours.
	if len(v.Hours) != 2 || len(v.Cells) != 2 || v.MaxActivity != 7 {
		t.Fatalf("heatmap: %+v", v)
	}
}

func TestGetEventAnalyticsProgressInactive(t *testing.T) {
	event := runningEvent() // starts 11:50, finishes 16:00
	clock := time.Date(2026, 9, 29, 14, 0, 0, 0, time.UTC)
	recent, old := clock.Add(-10*time.Minute), clock.Add(-90*time.Minute)
	store := &taskStore{
		teams: []eventAnalyticsRepo.RankedTeam{{ID: id(1), Points: 100}, {ID: id(2)}, {ID: id(3)}, {ID: id(4)}},
		activity: []eventAnalyticsRepo.TeamActivity{
			{TeamID: id(1), TeamName: "Active", LastActivityAt: &recent},
			{TeamID: id(2), TeamName: "Stale", LastActivityAt: &old},
			{TeamID: id(3), TeamName: "Never"},
			{TeamID: id(4), TeamName: "Before start", LastActivityAt: ptr(event.Lifecycle.StartAt.Add(-time.Hour))},
		},
	}
	uc := newTaskUC(store, event, &clock)

	v, err := uc.GetEventAnalyticsProgressInactive(context.Background(), event.ID, 30)
	if err != nil {
		t.Fatal(err)
	}
	// Idle counts from the start (130 min) for a team with no activity or only
	// activity before the start; the recent team is not idle.
	if !v.Running || len(v.Teams) != 3 || v.Teams[0].Name != "Never" || v.Teams[0].IdleMinutes != 130 || v.Teams[0].LastActivityAt != nil ||
		v.Teams[1].Name != "Before start" || v.Teams[1].IdleMinutes != 130 || v.Teams[2].Name != "Stale" || v.Teams[2].IdleMinutes != 90 {
		t.Fatalf("inactive: %+v", v)
	}
	if !store.activityAt.Equal(clock) {
		t.Fatalf("measured at %s", store.activityAt)
	}

	// Below the minimum threshold the minimum applies.
	if v, _ = uc.GetEventAnalyticsProgressInactive(context.Background(), event.ID, 1); v.Minutes != eventAnalytics.MinInactiveMinutes {
		t.Fatalf("threshold: %d", v.Minutes)
	}

	// After the finish the moment is the finish.
	late := time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)
	uc = newTaskUC(store, event, &late)
	if v, err = uc.GetEventAnalyticsProgressInactive(context.Background(), event.ID, 30); err != nil || !v.AsOf.Equal(*event.Lifecycle.FinishAt) {
		t.Fatalf("asOf = %s err=%v", v.AsOf, err)
	}

	// Before the start nobody is idle.
	early := event.Lifecycle.StartAt.Add(-time.Hour)
	uc = newTaskUC(store, event, &early)
	if v, err = uc.GetEventAnalyticsProgressInactive(context.Background(), event.ID, 30); err != nil || v.Running || len(v.Teams) != 0 {
		t.Fatalf("before the start: %+v err=%v", v, err)
	}
}

// fakeStore satisfies TaskStore with empty answers; the reports above use
// taskStore.
func (s *fakeStore) Challenges(context.Context, uuid.UUID) ([]eventAnalyticsRepo.Challenge, error) {
	return nil, nil
}
func (s *fakeStore) Challenge(context.Context, uuid.UUID, uuid.UUID) (eventAnalyticsRepo.Challenge, bool, error) {
	return eventAnalyticsRepo.Challenge{}, false, nil
}
func (s *fakeStore) TaskStats(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.TaskStats, error) {
	return nil, nil
}
func (s *fakeStore) TaskSeries(context.Context, uuid.UUID, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.SeriesPoint, error) {
	return nil, nil
}
func (s *fakeStore) TaskFailedTeams(context.Context, uuid.UUID, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.FailedTeam, error) {
	return nil, nil
}
func (s *fakeStore) TaskWrongAnswers(context.Context, uuid.UUID, uuid.UUID, eventAnalyticsModel.Period, int32) ([]eventAnalyticsRepo.WrongAnswer, error) {
	return nil, nil
}
func (s *fakeStore) TaskHintEffect(context.Context, uuid.UUID, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.HintEffectRow, error) {
	return nil, nil
}
func (s *fakeStore) RankedTeams(context.Context, uuid.UUID) ([]eventAnalyticsRepo.RankedTeam, error) {
	return nil, nil
}
func (s *fakeStore) ScoreEvents(context.Context, uuid.UUID, []uuid.UUID) ([]eventAnalyticsRepo.ScoreEvent, error) {
	return nil, nil
}
func (s *fakeStore) Matrix(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.MatrixCell, error) {
	return nil, nil
}
func (s *fakeStore) Heatmap(context.Context, uuid.UUID, eventAnalyticsModel.Period) ([]eventAnalyticsRepo.HeatCell, error) {
	return nil, nil
}
func (s *fakeStore) TeamActivity(context.Context, uuid.UUID, time.Time) ([]eventAnalyticsRepo.TeamActivity, error) {
	return nil, nil
}
