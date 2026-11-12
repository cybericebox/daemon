package labCheckRepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/labCheckRepo"
	labCheckModel "github.com/cybericebox/daemon/internal/model/labCheck"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

var t0 = time.Date(2026, 11, 11, 12, 0, 0, 0, time.UTC)

func at(d time.Duration) *time.Time { v := t0.Add(d); return &v }

func TestResultsKeepTheFirstPassAndMoveTheChangeOnlyWithTheState(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := labCheckRepo.New(db.Pool)
	ctx := context.Background()
	base := labCheckModel.Result{Group: "g", Lab: "l", CheckID: "ospf:1"}

	// Not passed, then passed (the first pass), then not passed again, then passed again.
	steps := []struct {
		state labCheckModel.State
		first *time.Time
		when  time.Duration
		value string
	}{
		{labCheckModel.StateNotPassed, nil, 0, ""},
		{labCheckModel.StatePassed, at(10 * time.Second), 10 * time.Second, "a"},
		{labCheckModel.StatePassed, at(10 * time.Second), 10 * time.Second, "b"}, // same state, only the value moved
		{labCheckModel.StateNotPassed, at(10 * time.Second), 20 * time.Second, "c"},
		{labCheckModel.StatePassed, at(40 * time.Second), 40 * time.Second, "d"}, // the laboratory's first-pass time never moves back
	}
	for _, step := range steps {
		res := base
		res.State, res.FirstPassedAt, res.ChangedAt, res.Value, res.Detail = step.state, step.first, t0.Add(step.when), step.value, "detail "+step.value
		res.ObservedAt = t0.Add(step.when)
		require.NoError(t, repo.Record(ctx, res, t0.Add(step.when)))
	}
	got, err := repo.List(ctx, "g", "l")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, labCheckModel.StatePassed, got[0].State)
	require.Equal(t, t0.Add(10*time.Second), got[0].FirstPassedAt.UTC(), "the first pass stays")
	require.Equal(t, t0.Add(40*time.Second), got[0].ChangedAt.UTC(), "the change time follows the state")
	require.Equal(t, "d", got[0].Value)

	// An error keeps the state and refreshes only the detail for staff; a check without a result is not created.
	require.NoError(t, repo.NoteDetail(ctx, "g", "l", "ospf:1", "device not ready", t0.Add(time.Minute)))
	require.NoError(t, repo.NoteDetail(ctx, "g", "l", "other:1", "x", t0.Add(time.Minute)))
	got, err = repo.List(ctx, "g", "l")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, labCheckModel.StatePassed, got[0].State)
	require.Equal(t, "device not ready", got[0].Detail)

	// Another lab is apart; a lab that went away is dropped.
	other, err := repo.List(ctx, "g", "other")
	require.NoError(t, err)
	require.Empty(t, other)
	require.NoError(t, repo.DropLab(ctx, "g", "l"))
	got, err = repo.List(ctx, "g", "l")
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestManualRunsAreLimitedPerGroupAndTask(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	repo := labCheckRepo.New(db.Pool)
	ctx := context.Background()
	window := time.Minute

	for i := range 3 {
		ok, _, err := repo.AskRun(ctx, "team-a", "ospf", window, 3, t0.Add(time.Duration(i)*time.Second))
		require.NoError(t, err)
		require.True(t, ok, "ask %d", i)
	}
	ok, retry, err := repo.AskRun(ctx, "team-a", "ospf", window, 3, t0.Add(10*time.Second))
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, 50*time.Second, retry, "the oldest ask leaves the window in 50 s")

	// Another task and another team are not held by it.
	for _, c := range [][2]string{{"team-a", "other"}, {"team-b", "ospf"}} {
		ok, _, err = repo.AskRun(ctx, c[0], c[1], window, 3, t0.Add(10*time.Second))
		require.NoError(t, err)
		require.True(t, ok)
	}
	// After the window the asks are free again.
	ok, _, err = repo.AskRun(ctx, "team-a", "ospf", window, 3, t0.Add(time.Minute+time.Second))
	require.NoError(t, err)
	require.True(t, ok)

	require.NoError(t, repo.Purge(ctx, t0.Add(time.Hour)))
	ok, _, err = repo.AskRun(ctx, "team-a", "ospf", window, 3, t0.Add(time.Hour+time.Minute))
	require.NoError(t, err)
	require.True(t, ok)
}
