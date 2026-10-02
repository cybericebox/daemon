package errorJournalRepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/riverqueue/river/riverdriver/riverpgxv5"
	"github.com/riverqueue/river/rivermigrate"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/errorJournalRepo"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/internal/testhelpers"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
)

var t0 = time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)

func newRepo(t *testing.T) (*errorJournalRepo.Repository, *testhelpers.TestDB) {
	t.Helper()
	db := testhelpers.SetupTestDB(t)
	return errorJournalRepo.New(db.Queries, db.Pool), db
}

func record(t *testing.T, r *errorJournalRepo.Repository, fp string, at time.Time, keep int) errorJournalUseCase.RecordResult {
	t.Helper()
	status := 500
	res, err := r.Record(context.Background(), errorJournalUseCase.RecordInput{
		Fingerprint: fp, Kind: errorJournal.KindHTTP5xx, Source: "/api/x", Title: "boom " + fp, At: at, Keep: keep,
		Sample: errorJournal.Sample{ID: uuid.Must(uuid.NewV7()), OccurredAt: at, Message: "m", Method: "GET", Route: "/api/x", HTTPStatus: &status, Details: map[string]string{"a": "b"}},
	})
	require.NoError(t, err)
	return res
}

func TestRecordGroupsByFingerprintAndKeepsNewestSamples(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()

	first := record(t, r, "fp1", t0, 3)
	assert.True(t, first.Inserted)
	for i := 1; i <= 5; i++ {
		record(t, r, "fp1", t0.Add(time.Duration(i)*time.Minute), 3)
	}
	record(t, r, "fp2", t0, 3)

	groups, total, err := r.ListGroups(ctx, errorJournalUseCase.GroupFilter{Limit: 10})
	require.NoError(t, err)
	require.EqualValues(t, 2, total)
	var g errorJournal.Group
	for _, x := range groups {
		if x.Fingerprint == "fp1" {
			g = x
		}
	}
	assert.EqualValues(t, 6, g.Occurrences)
	assert.Equal(t, t0, g.FirstSeenAt.UTC())
	assert.Equal(t, t0.Add(5*time.Minute), g.LastSeenAt.UTC())

	samples, err := r.ListSamples(ctx, g.ID, 10)
	require.NoError(t, err)
	require.Len(t, samples, 3, "only the newest samples stay")
	assert.Equal(t, t0.Add(5*time.Minute), samples[0].OccurredAt.UTC())
	assert.Equal(t, map[string]string{"a": "b"}, samples[0].Details)
	require.NotNil(t, samples[0].HTTPStatus)
	assert.Equal(t, 500, *samples[0].HTTPStatus)
}

func TestResolvedGroupReopensOnTheNextOccurrence(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	rec := record(t, r, "fp", t0, 3)
	g, err := r.SetGroupStatus(ctx, rec.Group.ID, errorJournal.StatusResolved, t0)
	require.NoError(t, err)
	require.NotNil(t, g.ResolvedAt)

	again := record(t, r, "fp", t0.Add(time.Hour), 3)
	assert.True(t, again.Reopened)
	assert.False(t, again.Inserted)
	assert.Equal(t, errorJournal.StatusOpen, again.Group.Status)
	assert.Nil(t, again.Group.ResolvedAt)

	third := record(t, r, "fp", t0.Add(2*time.Hour), 3)
	assert.False(t, third.Reopened)
}

func TestIgnoredStaysIgnoredAndMissingGroupIsNotFound(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	rec := record(t, r, "fp", t0, 3)
	_, err := r.SetGroupStatus(ctx, rec.Group.ID, errorJournal.StatusIgnored, t0)
	require.NoError(t, err)
	assert.Equal(t, errorJournal.StatusIgnored, record(t, r, "fp", t0.Add(time.Minute), 3).Group.Status)

	_, err = r.GetGroup(ctx, uuid.Must(uuid.NewV7()))
	assert.ErrorIs(t, err, errorJournal.ErrGroupNotFound.Err())
	_, err = r.SetGroupStatus(ctx, uuid.Must(uuid.NewV7()), errorJournal.StatusOpen, t0)
	assert.ErrorIs(t, err, errorJournal.ErrGroupNotFound.Err())
}

func TestListGroupsFilters(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	a := record(t, r, "a", t0, 3)
	_, err := r.Record(ctx, errorJournalUseCase.RecordInput{
		Fingerprint: "b", Kind: errorJournal.KindPanic, Source: "/api/100%", Title: "panic 100% sure", At: t0.Add(24 * time.Hour), Keep: 3,
		Sample: errorJournal.Sample{ID: uuid.Must(uuid.NewV7()), OccurredAt: t0, Message: "p"},
	})
	require.NoError(t, err)
	_, err = r.SetGroupStatus(ctx, a.Group.ID, errorJournal.StatusResolved, t0)
	require.NoError(t, err)

	list := func(f errorJournalUseCase.GroupFilter) []string {
		f.Limit = 10
		groups, total, err := r.ListGroups(ctx, f)
		require.NoError(t, err)
		require.EqualValues(t, len(groups), total)
		var fps []string
		for _, g := range groups {
			fps = append(fps, g.Fingerprint)
		}
		return fps
	}
	assert.Equal(t, []string{"b", "a"}, list(errorJournalUseCase.GroupFilter{}), "newest first")
	assert.Equal(t, []string{"b"}, list(errorJournalUseCase.GroupFilter{Kinds: []errorJournal.Kind{errorJournal.KindPanic}}))
	assert.Equal(t, []string{"a"}, list(errorJournalUseCase.GroupFilter{Status: errorJournal.StatusResolved}))
	from := t0.Add(12 * time.Hour)
	assert.Equal(t, []string{"b"}, list(errorJournalUseCase.GroupFilter{From: &from}))
	to := t0.Add(12 * time.Hour)
	assert.Equal(t, []string{"a"}, list(errorJournalUseCase.GroupFilter{To: &to}))
	assert.Equal(t, []string{"b"}, list(errorJournalUseCase.GroupFilter{Query: "100%"}), "a % in the query is literal")
	assert.Empty(t, list(errorJournalUseCase.GroupFilter{Query: "1%0"}))
}

func TestMarkNotifiedIsClaimedOnceAndReturnsTheSuppressedCount(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	rec := record(t, r, "fp", t0, 3)
	record(t, r, "fp", t0, 3)
	record(t, r, "fp", t0, 3)

	n, claimed, err := r.MarkNotified(ctx, rec.Group.ID, t0, t0.Add(-time.Minute))
	require.NoError(t, err)
	assert.True(t, claimed)
	assert.EqualValues(t, 3, n)

	_, claimed, err = r.MarkNotified(ctx, rec.Group.ID, t0.Add(time.Second), t0.Add(-time.Minute))
	require.NoError(t, err)
	assert.False(t, claimed, "inside the cooldown nobody else can claim")

	record(t, r, "fp", t0, 3)
	n, claimed, err = r.MarkNotified(ctx, rec.Group.ID, t0.Add(20*time.Minute), t0.Add(5*time.Minute))
	require.NoError(t, err)
	assert.True(t, claimed)
	assert.EqualValues(t, 1, n)
}

func TestNotFoundCountersAddUpPerDayAndRoute(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()
	require.NoError(t, r.AddNotFound(ctx, t0, "/api/e/:id", 2))
	require.NoError(t, r.AddNotFound(ctx, t0.Add(3*time.Hour), "/api/e/:id", 3))
	require.NoError(t, r.AddNotFound(ctx, t0, "", 7))
	require.NoError(t, r.AddNotFound(ctx, t0.Add(-48*time.Hour), "", 1))

	days, err := r.ListNotFound(ctx, t0.Add(-24*time.Hour), t0)
	require.NoError(t, err)
	require.Len(t, days, 2)
	assert.Equal(t, errorJournal.NotFoundDay{Day: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC), Route: "", Hits: 7}, days[0])
	assert.EqualValues(t, 5, days[1].Hits)
}

func TestPurgeDeletesWhatIsOlderThanTheCutoff(t *testing.T) {
	r, db := newRepo(t)
	ctx := context.Background()
	record(t, r, "old", t0.Add(-40*24*time.Hour), 3)
	record(t, r, "fresh", t0, 3)
	require.NoError(t, r.AddNotFound(ctx, t0.Add(-40*24*time.Hour), "/a", 1))
	require.NoError(t, r.AddNotFound(ctx, t0, "/a", 1))

	res, err := r.Purge(ctx, t0.Add(-30*24*time.Hour))
	require.NoError(t, err)
	assert.EqualValues(t, 1, res.Groups)
	assert.EqualValues(t, 1, res.NotFound)

	var groups, samples, notFound int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM error_groups), (SELECT count(*) FROM error_samples), (SELECT count(*) FROM error_not_found_daily)`).Scan(&groups, &samples, &notFound))
	assert.Equal(t, []int{1, 1, 1}, []int{groups, samples, notFound}, "the samples of a purged group go with it")
}

func TestSettingsAndChatsKeepTheFailingMark(t *testing.T) {
	r, _ := newRepo(t)
	ctx := context.Background()

	s, err := r.GetSettings(ctx)
	require.NoError(t, err)
	assert.True(t, s.EmailToSuperAdmins, "super admins are the default recipients")
	assert.Empty(t, s.Emails)

	require.NoError(t, r.SaveEmails(ctx, []string{"a@example.org"}, false, t0))
	require.NoError(t, r.ReplaceChats(ctx, []errorJournal.TelegramChat{{ChatID: "1", Label: "ops", CreatedAt: t0}, {ChatID: "2", CreatedAt: t0}}))
	require.NoError(t, r.SetChatFailing(ctx, "1", true, "Forbidden: bot was blocked", t0))

	require.NoError(t, r.ReplaceChats(ctx, []errorJournal.TelegramChat{{ChatID: "1", Label: "renamed", CreatedAt: t0.Add(time.Hour)}, {ChatID: "3", CreatedAt: t0}}))
	s, err = r.GetSettings(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"a@example.org"}, s.Emails)
	assert.False(t, s.EmailToSuperAdmins)
	require.Len(t, s.TelegramChats, 2, "chat 2 was removed, chat 3 added")
	one := s.TelegramChats[0]
	assert.Equal(t, "1", one.ChatID)
	assert.Equal(t, "renamed", one.Label)
	assert.True(t, one.Failing, "a chat that stays keeps its failing mark")
	assert.NotNil(t, one.FailingSince)

	require.NoError(t, r.SetChatFailing(ctx, "1", false, "", t0))
	s, _ = r.GetSettings(ctx)
	assert.False(t, s.TelegramChats[0].Failing)
	assert.Nil(t, s.TelegramChats[0].FailingSince)
}

func TestSuperAdminEmailsListsOnlyActiveSuperAdmins(t *testing.T) {
	r, db := newRepo(t)
	ctx := context.Background()
	for _, u := range []struct{ email, role, status string }{
		{"root@example.org", "super_admin", "active"},
		{"blocked@example.org", "super_admin", "blocked"},
		{"admin@example.org", "admin", "active"},
	} {
		_, err := db.Pool.Exec(ctx, `INSERT INTO users (id, email, role, status, created_at, updated_at) VALUES ($1, $2, $3, $4, now(), now())`,
			uuid.Must(uuid.NewV7()), u.email, u.role, u.status)
		require.NoError(t, err)
	}
	emails, err := r.SuperAdminEmails(ctx)
	require.NoError(t, err)
	assert.Equal(t, []string{"root@example.org"}, emails)
}

func TestQueueStatsSeesJobsThatWaitedTooLong(t *testing.T) {
	r, db := newRepo(t)
	ctx := context.Background()
	migrator, err := rivermigrate.New(riverpgxv5.New(db.Pool), nil)
	require.NoError(t, err)
	_, err = migrator.Migrate(ctx, rivermigrate.DirectionUp, nil)
	require.NoError(t, err)

	now := time.Now().UTC()
	stats, err := r.QueueStats(ctx, now)
	require.NoError(t, err)
	assert.Zero(t, stats.Waiting)

	for _, q := range []struct {
		state string
		at    time.Time
	}{
		{"available", now.Add(-10 * time.Minute)},
		{"available", now.Add(-time.Minute)},
		{"scheduled", now.Add(time.Hour)}, // not due: not waiting
		{"completed", now.Add(-time.Hour)},
	} {
		_, err = db.Pool.Exec(ctx, `INSERT INTO river_job (state, kind, args, scheduled_at, max_attempts, queue, priority, finalized_at) VALUES ($1::river_job_state, 'k', '{}', $2, 3, 'default', 1, CASE WHEN $1::river_job_state = 'completed' THEN now() END)`, q.state, q.at)
		require.NoError(t, err)
	}
	stats, err = r.QueueStats(ctx, now)
	require.NoError(t, err)
	assert.EqualValues(t, 2, stats.Waiting)
	assert.InDelta(t, 600, stats.OldestWait.Seconds(), 2)
}

func TestRecordCountAddsTheOccurrencesOfAnAgentReport(t *testing.T) {
	r, _ := newRepo(t)
	rec := func(count int) errorJournalUseCase.RecordResult {
		res, err := r.Record(context.Background(), errorJournalUseCase.RecordInput{
			Fingerprint: "agent", Kind: errorJournal.KindLabComponent, Source: "k0s/operator", Title: "t", At: t0, Keep: 3, Count: count,
			Sample: errorJournal.Sample{ID: uuid.Must(uuid.NewV7()), OccurredAt: t0, Message: "m"},
		})
		require.NoError(t, err)
		return res
	}
	first := rec(7)
	assert.True(t, first.Inserted)
	assert.EqualValues(t, 7, first.Group.Occurrences)
	second := rec(7)
	assert.False(t, second.Inserted, "a second report of the same size is not a new group")
	assert.EqualValues(t, 14, second.Group.Occurrences)
}
