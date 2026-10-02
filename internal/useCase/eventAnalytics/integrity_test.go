package eventAnalytics_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventAnalyticsRepo"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	labTraffic "github.com/cybericebox/daemon/internal/model/labTraffic"
	"github.com/cybericebox/daemon/internal/model/rbac"
	"github.com/cybericebox/daemon/internal/useCase/eventAnalytics"
)

// reviewStore keeps the organizers' notes in memory.
type reviewStore struct {
	*sectionsStore
	solves  map[uuid.UUID]bool
	reviews map[uuid.UUID]eventAnalyticsRepo.SolveReview
	// dismissals apply to the catalog task (exercise, task) of the fixture's solves.
	dismissals     []eventAnalyticsRepo.IntegrityDismissal
	exercise, task uuid.UUID
}

func (s *reviewStore) SolveReviews(context.Context, uuid.UUID) ([]eventAnalyticsRepo.SolveReview, error) {
	out := make([]eventAnalyticsRepo.SolveReview, 0, len(s.reviews))
	for _, r := range s.reviews {
		out = append(out, r)
	}
	return out, nil
}
func (s *reviewStore) SaveSolveReview(_ context.Context, _, tc, by uuid.UUID, note string, at time.Time) (bool, error) {
	if !s.solves[tc] {
		return false, nil
	}
	s.reviews[tc] = eventAnalyticsRepo.SolveReview{TeamChallengeID: tc, Note: note, ReviewedBy: by, ReviewedByName: "Owner", ReviewedAt: at}
	return true, nil
}
func (s *reviewStore) DeleteSolveReview(_ context.Context, _, tc uuid.UUID) (bool, error) {
	_, ok := s.reviews[tc]
	delete(s.reviews, tc)
	return ok, nil
}

func (s *reviewStore) Dismissals(context.Context, uuid.UUID) ([]eventAnalyticsRepo.IntegrityDismissal, error) {
	return s.dismissals, nil
}
func (s *reviewStore) SaveDismissal(_ context.Context, _, tc uuid.UUID, scope eventAnalyticsModel.DismissScope, kind eventAnalyticsModel.IntegrityKind, key, note string, _, id uuid.UUID, at time.Time) (bool, error) {
	if !s.solves[tc] {
		return false, nil
	}
	s.dismissals = append(s.dismissals, eventAnalyticsRepo.IntegrityDismissal{ID: id, Scope: scope, ExerciseID: s.exercise, TaskID: s.task, Kind: kind, Key: key, Note: note, CreatedAt: at})
	return true, nil
}
func (s *reviewStore) DeleteDismissal(_ context.Context, _, id uuid.UUID) (bool, error) {
	for i, d := range s.dismissals {
		if d.ID == id {
			s.dismissals = append(s.dismissals[:i], s.dismissals[i+1:]...)
			return true, nil
		}
	}
	return false, nil
}

type integrityFixture struct {
	uc         *eventAnalytics.EventAnalyticsUseCase
	store      *reviewStore
	eventID    uuid.UUID
	noAccess   uuid.UUID // team challenge of a solve without any access
	tooFast    uuid.UUID
	teamA      uuid.UUID
	taskWeb    uuid.UUID
	taskCrypto uuid.UUID
}

func newIntegrityFixture(t *testing.T) integrityFixture {
	t.Helper()
	clock := now.Add(3 * time.Hour)
	event := runningEvent()
	start := event.Lifecycle.StartAt
	teamA, teamB := uuid.UUID{15: 1}, uuid.UUID{15: 2}
	web, crypto := uuid.UUID{15: 9}, uuid.UUID{15: 10}
	tcNoAccess, tcTooFast, tcClean := uuid.UUID{15: 21}, uuid.UUID{15: 22}, uuid.UUID{15: 23}
	exercise, taskWeb := uuid.UUID{15: 40}, uuid.UUID{15: 41}
	open := start.Add(time.Minute)
	fastOpen := start.Add(59 * time.Minute)
	facts := eventAnalyticsModel.IntegrityFacts{
		CollectorStart: &open,
		Solves: []eventAnalyticsModel.IntegritySolve{
			{TeamChallengeID: tcNoAccess, TeamID: teamA, TeamName: "A", ChallengeID: web, ExerciseID: exercise, TaskID: taskWeb, ChallengeName: "Web", Level: "easy", SolvedAt: start.Add(30 * time.Minute)},
			{TeamChallengeID: tcTooFast, TeamID: teamB, TeamName: "B", ChallengeID: web, ExerciseID: exercise, TaskID: taskWeb, ChallengeName: "Web", Level: "hard", SolvedAt: start.Add(time.Hour), FirstOpen: &fastOpen},
			{TeamChallengeID: tcClean, TeamID: teamA, TeamName: "A", ChallengeID: crypto, ChallengeName: "Crypto", Level: "hard", SolvedAt: start.Add(90 * time.Minute), FirstOpen: &open},
		},
	}
	store := &reviewStore{
		sectionsStore: &sectionsStore{fakeStore: &fakeStore{}, facts: facts},
		solves:        map[uuid.UUID]bool{tcNoAccess: true, tcTooFast: true, tcClean: true},
		reviews:       map[uuid.UUID]eventAnalyticsRepo.SolveReview{},
		exercise:      exercise, task: taskWeb,
	}
	return integrityFixture{
		uc: newUCWithStore(store, event, &clock), store: store, eventID: event.ID,
		noAccess: tcNoAccess, tooFast: tcTooFast, teamA: teamA, taskWeb: web, taskCrypto: crypto,
	}
}

func TestGetEventAnalyticsIntegrity_ListsFlaggedSolves(t *testing.T) {
	f := newIntegrityFixture(t)
	ctx := context.Background()
	v, err := f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, eventAnalyticsModel.DefaultIntegrityThresholds(), eventAnalytics.IntegrityFilter{})
	require.NoError(t, err)
	require.Equal(t, 2, v.Total)
	require.Len(t, v.Items, 2)
	require.Equal(t, f.tooFast, v.Items[0].TeamChallengeID, "newest first at equal signal count")
	require.Equal(t, eventAnalyticsModel.IntegrityTooFast, v.Items[0].Signals[0].Kind)
	require.Equal(t, eventAnalyticsModel.IntegrityNoAccess, v.Items[1].Signals[0].Kind)
	require.Equal(t, 1, v.Counts[eventAnalyticsModel.IntegrityNoAccess])
	require.Equal(t, 1, v.Counts[eventAnalyticsModel.IntegrityTooFast])

	t.Run("filters", func(t *testing.T) {
		got, err := f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, eventAnalyticsModel.DefaultIntegrityThresholds(),
			eventAnalytics.IntegrityFilter{Signal: eventAnalyticsModel.IntegrityNoAccess})
		require.NoError(t, err)
		require.Len(t, got.Items, 1)
		require.Equal(t, 1, got.Counts[eventAnalyticsModel.IntegrityTooFast], "chips keep counting the other signals")

		got, err = f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, eventAnalyticsModel.DefaultIntegrityThresholds(),
			eventAnalytics.IntegrityFilter{TeamID: f.teamA})
		require.NoError(t, err)
		require.Len(t, got.Items, 1)
		require.Equal(t, f.noAccess, got.Items[0].TeamChallengeID)

		got, err = f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, eventAnalyticsModel.DefaultIntegrityThresholds(),
			eventAnalytics.IntegrityFilter{ChallengeID: f.taskCrypto})
		require.NoError(t, err)
		require.Empty(t, got.Items)
		require.Equal(t, 0, got.Total)
	})

	t.Run("floors are clamped and part of the report", func(t *testing.T) {
		th := eventAnalyticsModel.DefaultIntegrityThresholds()
		th.Floors["hard"] = 0
		th.Floors["easy"] = 100 * time.Hour
		got, err := f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, th, eventAnalytics.IntegrityFilter{})
		require.NoError(t, err)
		require.Equal(t, time.Hour, got.Thresholds.Floors["easy"])
		require.Equal(t, 1, got.Total, "the hard solve is no longer too fast")
	})
}

func TestReviewEventSolve(t *testing.T) {
	f := newIntegrityFixture(t)
	ctx := context.Background()
	reviewer := uuid.UUID{15: 77}
	th := eventAnalyticsModel.DefaultIntegrityThresholds()

	require.NoError(t, f.uc.ReviewEventSolve(ctx, f.eventID, f.noAccess, reviewer, "  asked the team, they had the file offline "))
	v, err := f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, th, eventAnalytics.IntegrityFilter{})
	require.NoError(t, err)
	require.Len(t, v.Items, 2, "a reviewed solve stays in the list")
	reviewed := v.Items[1]
	require.NotNil(t, reviewed.Review)
	require.Equal(t, "asked the team, they had the file offline", reviewed.Review.Note)
	require.Equal(t, "Owner", reviewed.Review.ReviewedBy)

	unreviewed, err := f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, th, eventAnalytics.IntegrityFilter{Reviewed: eventAnalytics.ReviewedNotYet})
	require.NoError(t, err)
	require.Len(t, unreviewed.Items, 1)
	only, err := f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, th, eventAnalytics.IntegrityFilter{Reviewed: eventAnalytics.ReviewedOnly})
	require.NoError(t, err)
	require.Len(t, only.Items, 1)

	flags, err := f.uc.GetEventAnalyticsIntegrityFlags(ctx, f.eventID)
	require.NoError(t, err)
	require.Len(t, flags, 1, "the journal marks only unreviewed solves")
	require.Equal(t, f.tooFast, flags[0].TeamChallengeID)

	require.NoError(t, f.uc.UnreviewEventSolve(ctx, f.eventID, f.noAccess))
	require.ErrorIs(t, f.uc.UnreviewEventSolve(ctx, f.eventID, f.noAccess), eventAnalyticsModel.ErrEventAnalyticsSolveNotFound.Err())

	require.ErrorIs(t, f.uc.ReviewEventSolve(ctx, f.eventID, uuid.UUID{15: 99}, reviewer, "x"), eventAnalyticsModel.ErrEventAnalyticsSolveNotFound.Err())
	long := strings.Repeat("я", eventAnalyticsModel.MaxReviewNoteLength+1)
	require.ErrorIs(t, f.uc.ReviewEventSolve(ctx, f.eventID, f.noAccess, reviewer, long), eventAnalyticsModel.ErrEventAnalyticsReviewNoteTooLong.Err())
	require.NoError(t, f.uc.ReviewEventSolve(ctx, f.eventID, f.noAccess, reviewer, strings.Repeat("я", eventAnalyticsModel.MaxReviewNoteLength)))
}

func TestIntegrityReadsAreCachedButReviewsAreFresh(t *testing.T) {
	f := newIntegrityFixture(t)
	ctx := context.Background()
	th := eventAnalyticsModel.DefaultIntegrityThresholds()
	for i := 0; i < 3; i++ {
		_, err := f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, th, eventAnalytics.IntegrityFilter{})
		require.NoError(t, err)
	}
	require.NoError(t, f.uc.ReviewEventSolve(ctx, f.eventID, f.tooFast, uuid.UUID{15: 1}, ""))
	v, err := f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, th, eventAnalytics.IntegrityFilter{Reviewed: eventAnalytics.ReviewedOnly})
	require.NoError(t, err)
	require.Len(t, v.Items, 1)
	require.Equal(t, 1, f.store.factReads, "the facts were read once")
}

func TestDismissIntegrityPattern(t *testing.T) {
	f := newIntegrityFixture(t)
	ctx := context.Background()
	th := eventAnalyticsModel.DefaultIntegrityThresholds()
	by := uuid.UUID{15: 77}

	// «Не підсвічувати такі випадки» for too_fast on the task: that solve is gone.
	require.NoError(t, f.uc.DismissIntegrityPattern(ctx, f.eventID, f.tooFast, by, eventAnalyticsModel.DismissEvent, eventAnalyticsModel.IntegrityTooFast, "ignored", " fine "))
	v, err := f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, th, eventAnalytics.IntegrityFilter{})
	require.NoError(t, err)
	require.Len(t, v.Items, 1)
	require.Equal(t, eventAnalyticsModel.IntegrityNoAccess, v.Items[0].Signals[0].Kind)
	require.Equal(t, 0, v.Counts[eventAnalyticsModel.IntegrityTooFast], "counts follow the dismissals")

	flags, err := f.uc.GetEventAnalyticsIntegrityFlags(ctx, f.eventID)
	require.NoError(t, err)
	require.Len(t, flags, 1)

	list, err := f.uc.ListIntegrityDismissals(ctx, f.eventID)
	require.NoError(t, err)
	require.Len(t, list, 1)
	require.Equal(t, "fine", list[0].Note)
	require.Empty(t, list[0].Key, "only shared_wrong keeps a key")

	require.NoError(t, f.uc.RemoveIntegrityDismissal(ctx, f.eventID, list[0].ID))
	require.ErrorIs(t, f.uc.RemoveIntegrityDismissal(ctx, f.eventID, list[0].ID), eventAnalyticsModel.ErrEventAnalyticsSolveNotFound.Err())
	v, err = f.uc.GetEventAnalyticsIntegrity(ctx, f.eventID, nil, nil, th, eventAnalytics.IntegrityFilter{})
	require.NoError(t, err)
	require.Len(t, v.Items, 2)

	invalid := eventAnalyticsModel.ErrEventAnalyticsDismissalInvalid.Err()
	require.ErrorIs(t, f.uc.DismissIntegrityPattern(ctx, f.eventID, f.tooFast, by, eventAnalyticsModel.DismissEvent, eventAnalyticsModel.IntegrityBurst, "", ""), invalid)
	require.ErrorIs(t, f.uc.DismissIntegrityPattern(ctx, f.eventID, f.tooFast, by, eventAnalyticsModel.DismissEvent, eventAnalyticsModel.IntegrityCrossFlag, "", ""), invalid)
	require.ErrorIs(t, f.uc.DismissIntegrityPattern(ctx, f.eventID, f.tooFast, by, eventAnalyticsModel.DismissScope("all"), eventAnalyticsModel.IntegrityTooFast, "", ""), invalid)
	require.ErrorIs(t, f.uc.DismissIntegrityPattern(ctx, f.eventID, f.tooFast, by, eventAnalyticsModel.DismissEvent, eventAnalyticsModel.IntegritySharedWrong, "  ", ""), invalid, "a value is needed")
	require.ErrorIs(t, f.uc.DismissIntegrityPattern(ctx, f.eventID, uuid.UUID{15: 99}, by, eventAnalyticsModel.DismissEvent, eventAnalyticsModel.IntegrityTooFast, "", ""), eventAnalyticsModel.ErrEventAnalyticsSolveNotFound.Err())
	adminCtx := rbac.ContextWithCurrentUserSession(ctx, rbac.Claims{UserID: by, Role: rbac.RoleAdmin})
	require.NoError(t, f.uc.DismissIntegrityPattern(adminCtx, f.eventID, f.tooFast, by, eventAnalyticsModel.DismissExercise, eventAnalyticsModel.IntegritySharedWrong, "  ICE{Decoy} ", ""))
	list, err = f.uc.ListIntegrityDismissals(ctx, f.eventID)
	require.NoError(t, err)
	require.Equal(t, "ice{decoy}", list[0].Key, "the value is stored normalized")
	require.Equal(t, eventAnalyticsModel.DismissExercise, list[0].Scope)
}

type fakeLab struct {
	answers map[uuid.UUID]labTraffic.Verdict
	err     bool
	asked   int
}

func (f *fakeLab) Ask(_ context.Context, q labTraffic.Question) (labTraffic.Answer, error) {
	f.asked++
	if f.err {
		return labTraffic.Answer{}, context.DeadlineExceeded
	}
	return labTraffic.Answer{Verdict: f.answers[q.TeamID], Attempted: true}, nil
}

func TestLabTrafficDecidesNoLab(t *testing.T) {
	ctx := context.Background()
	th := eventAnalyticsModel.DefaultIntegrityThresholds()
	run := func(lab *fakeLab) []eventAnalyticsModel.IntegrityKind {
		clock := now.Add(3 * time.Hour)
		event := runningEvent()
		start := event.Lifecycle.StartAt
		open := start.Add(time.Minute)
		team := uuid.UUID{15: 1}
		solve := eventAnalyticsModel.IntegritySolve{
			TeamChallengeID: uuid.UUID{15: 21}, TeamID: team, TeamName: "A", ChallengeID: uuid.UUID{15: 9}, ChallengeName: "Web",
			Level: "easy", SolvedAt: start.Add(30 * time.Minute), FirstOpen: &open, HasLab: true,
		}
		store := &reviewStore{
			sectionsStore: &sectionsStore{fakeStore: &fakeStore{}, facts: eventAnalyticsModel.IntegrityFacts{Solves: []eventAnalyticsModel.IntegritySolve{solve}}},
			solves:        map[uuid.UUID]bool{}, reviews: map[uuid.UUID]eventAnalyticsRepo.SolveReview{},
		}
		uc := eventAnalytics.New(eventAnalytics.Dependencies{
			Store: store, Events: fakeEvents{event}, Configs: fakeConfigs{}, Memberships: fakeMemberships{},
			LabTraffic: lab, Now: func() time.Time { return clock },
		})
		v, err := uc.GetEventAnalyticsIntegrity(ctx, event.ID, nil, nil, th, eventAnalytics.IntegrityFilter{})
		require.NoError(t, err)
		var kinds []eventAnalyticsModel.IntegrityKind
		for _, item := range v.Items {
			for _, s := range item.Signals {
				kinds = append(kinds, s.Kind)
			}
		}
		return kinds
	}
	team := uuid.UUID{15: 1}
	require.Equal(t, []eventAnalyticsModel.IntegrityKind{eventAnalyticsModel.IntegrityNoLab}, run(&fakeLab{answers: map[uuid.UUID]labTraffic.Verdict{team: labTraffic.Untouched}}))
	require.Empty(t, run(&fakeLab{answers: map[uuid.UUID]labTraffic.Verdict{team: labTraffic.Touched}}))
	// Unknown or failed answers fall back on the VPN sessions (none here): still flagged.
	require.Len(t, run(&fakeLab{answers: map[uuid.UUID]labTraffic.Verdict{team: labTraffic.Unknown}}), 1)
	require.Len(t, run(&fakeLab{err: true}), 1)
}

// M5: an exercise-scope dismissal silences the signal in every event that uses
// the catalog exercise. One event's manager must not create, read the author of,
// or remove it; platform admins can.
func TestExerciseScopeDismissalsAreForPlatformAdmins(t *testing.T) {
	f := newIntegrityFixture(t)
	manager := uuid.UUID{15: 5}
	managerCtx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: manager, Role: rbac.RoleUser})
	adminCtx := rbac.ContextWithCurrentUserSession(context.Background(), rbac.Claims{UserID: uuid.UUID{15: 6}, Role: rbac.RoleAdmin})
	denied := authModel.ErrInsufficientPermission.Err()

	// create: the manager may dismiss for THIS event only; without any identity not at all.
	require.ErrorIs(t, f.uc.DismissIntegrityPattern(managerCtx, f.eventID, f.tooFast, manager, eventAnalyticsModel.DismissExercise, eventAnalyticsModel.IntegrityTooFast, "", ""), denied)
	require.ErrorIs(t, f.uc.DismissIntegrityPattern(context.Background(), f.eventID, f.tooFast, manager, eventAnalyticsModel.DismissExercise, eventAnalyticsModel.IntegrityTooFast, "", ""), denied)
	require.NoError(t, f.uc.DismissIntegrityPattern(managerCtx, f.eventID, f.tooFast, manager, eventAnalyticsModel.DismissEvent, eventAnalyticsModel.IntegrityTooFast, "", "mine"))

	// an admin's exercise-scope dismissal, as seen from the event
	require.NoError(t, f.uc.DismissIntegrityPattern(adminCtx, f.eventID, f.tooFast, uuid.UUID{15: 6}, eventAnalyticsModel.DismissExercise, eventAnalyticsModel.IntegritySharedWrong, "decoy", "secret reasoning"))
	f.store.dismissals[len(f.store.dismissals)-1].CreatedByName = "Alice Admin"

	list, err := f.uc.ListIntegrityDismissals(managerCtx, f.eventID)
	require.NoError(t, err)
	var exerciseID uuid.UUID
	for _, d := range list {
		if d.Scope == eventAnalyticsModel.DismissExercise {
			exerciseID = d.ID
			require.Empty(t, d.CreatedBy, "another event's author is not shown")
			require.Empty(t, d.Note)
		}
	}
	require.NotEqual(t, uuid.Nil, exerciseID)
	adminList, err := f.uc.ListIntegrityDismissals(adminCtx, f.eventID)
	require.NoError(t, err)
	for _, d := range adminList {
		if d.Scope == eventAnalyticsModel.DismissExercise {
			require.Equal(t, "Alice Admin", d.CreatedBy)
			require.Equal(t, "secret reasoning", d.Note)
		}
	}

	// remove: the manager cannot take back what is not only theirs
	require.ErrorIs(t, f.uc.RemoveIntegrityDismissal(managerCtx, f.eventID, exerciseID), denied)
	require.NoError(t, f.uc.RemoveIntegrityDismissal(adminCtx, f.eventID, exerciseID))
}
