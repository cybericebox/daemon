package retention_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
	"github.com/cybericebox/daemon/internal/useCase/retention"
)

var now = time.Date(2026, 9, 29, 3, 0, 0, 0, time.UTC)

type fakeStore struct {
	// batches returned by each purge, consumed in order; then 0.
	purged    map[string][]int64
	failing   string
	cutoffs   map[string]time.Time
	toWarn    [][]retentionModel.InactiveAccount
	marked    []uuid.UUID
	toDelete  [][]retentionModel.WarnedAccount
	cleared   int64
	clearCall int
}

func (s *fakeStore) purge(name string, cutoff time.Time) (int64, error) {
	if s.cutoffs == nil {
		s.cutoffs = map[string]time.Time{}
	}
	s.cutoffs[name] = cutoff
	if name == s.failing {
		return 0, errors.New(name + " failed")
	}
	batches := s.purged[name]
	if len(batches) == 0 {
		return 0, nil
	}
	s.purged[name] = batches[1:]
	return batches[0], nil
}

func (s *fakeStore) PurgeExpiredSessions(_ context.Context, before time.Time, _ int32) (int64, error) {
	return s.purge("sessions", before)
}
func (s *fakeStore) PurgeEventLabObservations(_ context.Context, before time.Time, _ int32) (int64, error) {
	return s.purge("lab", before)
}
func (s *fakeStore) PurgePlatformLabCapacityObservations(_ context.Context, before time.Time, _ int32) (int64, error) {
	return s.purge("capacity", before)
}
func (s *fakeStore) PurgeNotificationDispatches(_ context.Context, before time.Time, _ int32) (int64, error) {
	return s.purge("dispatches", before)
}
func (s *fakeStore) PurgeFinishedSignals(_ context.Context, before time.Time, _ int32) (int64, error) {
	return s.purge("signals", before)
}
func (s *fakeStore) PurgeEventAnalytics(_ context.Context, before time.Time, _ int32) (int64, error) {
	return s.purge("analytics", before)
}
func (s *fakeStore) PurgeEventFormAnswers(_ context.Context, before time.Time, _ int32) (int64, error) {
	return s.purge("answers", before)
}
func (s *fakeStore) PurgeEventAnswerFiles(_ context.Context, before, unused time.Time, _ int32) (int64, error) {
	if s.cutoffs == nil {
		s.cutoffs = map[string]time.Time{}
	}
	s.cutoffs["unused_answer_files"] = unused
	return s.purge("answer_files", before)
}
func (s *fakeStore) PurgeDeletedAccounts(_ context.Context, at time.Time, _ int32) (int64, error) {
	return s.purge("deleted", at)
}
func (s *fakeStore) PurgeExpiredEventInvitations(_ context.Context, before time.Time, _ int32) (int64, error) {
	return s.purge("invitations", before)
}
func (s *fakeStore) PurgeUnconfirmedAccounts(_ context.Context, before, _ time.Time, _ int32) (int64, error) {
	return s.purge("pending", before)
}
func (s *fakeStore) ClearReturnedInactivityWarnings(context.Context) (int64, error) {
	s.clearCall++
	return s.cleared, nil
}
func (s *fakeStore) ListInactiveAccountsToWarn(_ context.Context, since time.Time, _ int32) ([]retentionModel.InactiveAccount, error) {
	s.cutoffs["inactive_since"] = since
	if len(s.toWarn) == 0 {
		return nil, nil
	}
	page := s.toWarn[0]
	s.toWarn = s.toWarn[1:]
	return page, nil
}
func (s *fakeStore) MarkInactivityWarned(_ context.Context, id uuid.UUID, at time.Time) (bool, error) {
	if !at.Equal(now) {
		return false, errors.New("warning must be recorded at the run's clock")
	}
	s.marked = append(s.marked, id)
	return true, nil
}
func (s *fakeStore) ListInactiveAccountsToDelete(_ context.Context, before time.Time, _ int32) ([]retentionModel.WarnedAccount, error) {
	s.cutoffs["warned_before"] = before
	if len(s.toDelete) == 0 {
		return nil, nil
	}
	page := s.toDelete[0]
	s.toDelete = s.toDelete[1:]
	return page, nil
}

type fakeNotifier struct {
	sent []notificationPayloads.AccountInactivityWarningPayload
	to   []uuid.UUID
	err  error
}

func (n *fakeNotifier) Notify(_ context.Context, userID uuid.UUID, p notificationTypes.NotificationPayload, _ ...dispatchModel.NotifyOption) error {
	if n.err != nil {
		return n.err
	}
	n.to = append(n.to, userID)
	n.sent = append(n.sent, p.(notificationPayloads.AccountInactivityWarningPayload))
	return nil
}

type fakeRemover struct {
	active  map[uuid.UUID]bool
	removed []uuid.UUID
}

func (r *fakeRemover) DeleteInactiveAccount(_ context.Context, id uuid.UUID, _ time.Time) (bool, error) {
	if r.active[id] {
		return false, nil
	}
	r.removed = append(r.removed, id)
	return true, nil
}

func newUC(store *fakeStore, notifier *fakeNotifier, remover *fakeRemover, batch int32) *retention.RetentionUseCase {
	policy := retentionModel.DefaultPolicy()
	policy.BatchSize = batch
	if store.cutoffs == nil {
		store.cutoffs = map[string]time.Time{}
	}
	return retention.New(retention.Dependencies{
		Store: store, Notifier: notifier, Accounts: remover, Policy: policy,
		SignInURL: "https://id.example.test/sign-in",
		Now:       func() time.Time { return now },
	})
}

func TestEnforceDataRetention_DrainsBatchesWithPolicyCutoffs(t *testing.T) {
	store := &fakeStore{purged: map[string][]int64{
		"sessions":   {2, 2, 1}, // two full batches, then a short one
		"dispatches": {2, 0},
	}}
	uc := newUC(store, &fakeNotifier{}, &fakeRemover{}, 2)

	if err := uc.EnforceDataRetention(context.Background()); err != nil {
		t.Fatalf("EnforceDataRetention: %v", err)
	}
	if len(store.purged["sessions"]) != 0 || len(store.purged["dispatches"]) != 0 {
		t.Fatalf("every full batch must be followed by another: left %+v", store.purged)
	}
	want := retentionModel.DefaultPolicy().Cutoffs(now)
	for name, cutoff := range map[string]time.Time{
		"sessions": want.SessionsExpiredBefore, "lab": want.LabObservedBefore, "capacity": want.LabObservedBefore,
		"dispatches": want.DispatchesCreatedBefore, "signals": want.SignalsCreatedBefore, "analytics": want.AnalyticsEventsEndedBefore, "answers": want.EventsEndedBefore, "deleted": now,
		"invitations": want.InvitationsExpiredBefore, "pending": want.PendingAccountsCreatedBefore,
		"answer_files": want.EventsEndedBefore, "unused_answer_files": want.UnusedAnswerFilesCreatedBefore,
	} {
		if !store.cutoffs[name].Equal(cutoff) {
			t.Errorf("%s cutoff: got %s, want %s", name, store.cutoffs[name], cutoff)
		}
	}
}

func TestEnforceDataRetention_FailingStepDoesNotStopTheOthers(t *testing.T) {
	store := &fakeStore{purged: map[string][]int64{}, failing: "lab"}
	uc := newUC(store, &fakeNotifier{}, &fakeRemover{}, 10)

	if err := uc.EnforceDataRetention(context.Background()); err == nil {
		t.Fatal("a failing purge must fail the run")
	}
	for _, name := range []string{"sessions", "capacity", "dispatches", "signals", "analytics", "answers", "answer_files", "deleted", "invitations", "pending"} {
		if _, ran := store.cutoffs[name]; !ran {
			t.Errorf("%s purge did not run after the failure", name)
		}
	}
}

func TestEnforceAccountInactivity_WarnsThenDeletes(t *testing.T) {
	a, b, c := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	returned := uuid.Must(uuid.NewV7())
	store := &fakeStore{
		cleared: 1,
		toWarn: [][]retentionModel.InactiveAccount{
			{{UserID: a, FirstName: "Ann"}, {UserID: b, FirstName: "Bob"}}, // full page
			{{UserID: c, FirstName: "Cy"}},
		},
		toDelete: [][]retentionModel.WarnedAccount{{{UserID: a, WarnedAt: now.AddDate(0, -2, 0)}, {UserID: returned}}},
	}
	notifier := &fakeNotifier{}
	remover := &fakeRemover{active: map[uuid.UUID]bool{returned: true}}
	uc := newUC(store, notifier, remover, 2)

	if err := uc.EnforceAccountInactivity(context.Background()); err != nil {
		t.Fatalf("EnforceAccountInactivity: %v", err)
	}
	if store.clearCall != 1 {
		t.Fatal("warnings of returned users must be cleared first")
	}
	if len(notifier.to) != 3 || len(store.marked) != 3 || store.marked[2] != c {
		t.Fatalf("every inactive account must be warned then marked: sent=%v marked=%v", notifier.to, store.marked)
	}
	got := notifier.sent[0]
	if got.Name != "Ann" || got.DeletionDate != "29.10.2026" || got.SignInURL != "https://id.example.test/sign-in" {
		t.Fatalf("warning payload: %+v", got)
	}
	want := retentionModel.DefaultPolicy().Cutoffs(now)
	if !store.cutoffs["inactive_since"].Equal(want.InactiveSince) || !store.cutoffs["warned_before"].Equal(want.WarnedBefore) {
		t.Fatalf("inactivity cutoffs: %+v", store.cutoffs)
	}
	if len(remover.removed) != 1 || remover.removed[0] != a {
		t.Fatalf("only the still-inactive account is deleted: %v", remover.removed)
	}
}

// A warning that could not be queued is not recorded, so the account is
// never deleted without one; deletion does not run in that pass either.
func TestEnforceAccountInactivity_NotifyFailureRecordsNothing(t *testing.T) {
	a := uuid.Must(uuid.NewV7())
	store := &fakeStore{
		toWarn:   [][]retentionModel.InactiveAccount{{{UserID: a}}},
		toDelete: [][]retentionModel.WarnedAccount{{{UserID: uuid.Must(uuid.NewV7())}}},
	}
	remover := &fakeRemover{}
	uc := newUC(store, &fakeNotifier{err: errors.New("queue down")}, remover, 10)

	if err := uc.EnforceAccountInactivity(context.Background()); err == nil {
		t.Fatal("a failed warning must fail the run")
	}
	if len(store.marked) != 0 || len(remover.removed) != 0 {
		t.Fatalf("nothing may be recorded or deleted: marked=%v removed=%v", store.marked, remover.removed)
	}
}
