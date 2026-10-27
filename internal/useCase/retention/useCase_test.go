package retention_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/retentionRepo"

	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	retentionModel "github.com/cybericebox/daemon/internal/model/retention"
	"github.com/cybericebox/daemon/internal/useCase/retention"
)

var now = time.Date(2026, 9, 29, 3, 0, 0, 0, time.UTC)

type fakeStore struct {
	erasures        []retentionRepo.LabTelemetryErasure
	settled         []uuid.UUID
	deletedErasures []uuid.UUID
	scrubbed        []uuid.UUID
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
func (s *fakeStore) PurgeRolledUpEventLabObservations(
	_ context.Context, before time.Time, _ int32,
) (int64, error) {
	return s.purge("lab_rolled_up", before)
}
func (s *fakeStore) LabTelemetryErasures(
	_ context.Context, after retentionRepo.LabTelemetryErasure, _ int32,
) ([]retentionRepo.LabTelemetryErasure, error) {
	var out []retentionRepo.LabTelemetryErasure
	for _, e := range s.erasures {
		if e.QueuedAt.After(after.QueuedAt) || (e.QueuedAt.Equal(after.QueuedAt) && e.UserID.String() > after.UserID.String()) {
			out = append(out, e)
		}
	}
	return out, nil
}
func (s *fakeStore) SettledLabTelemetryErasures(context.Context, time.Time, time.Time) ([]uuid.UUID, error) {
	return s.settled, nil
}
func (s *fakeStore) LabTelemetryErasuresQueuedBefore(context.Context, time.Time) (int64, error) {
	return 0, nil
}
func (s *fakeStore) DeleteLabTelemetryErasures(_ context.Context, ids []uuid.UUID) error {
	s.deletedErasures = append(s.deletedErasures, ids...)
	return nil
}
func (s *fakeStore) QueueLabTelemetryErasure(_ context.Context, userID uuid.UUID) (retentionRepo.LabTelemetryErasure, error) {
	return retentionRepo.LabTelemetryErasure{UserID: userID, ClientName: "p-x", Pseudonym: "deleted-x", QueuedAt: now}, nil
}
func (s *fakeStore) ScrubRawLabObservations(_ context.Context, e retentionRepo.LabTelemetryErasure, _ int32) (int64, error) {
	s.scrubbed = append(s.scrubbed, e.UserID)
	return 0, nil
}
func (s *fakeStore) ScrubLabMonitoringCurrent(context.Context, retentionRepo.LabTelemetryErasure) (int64, error) {
	return 0, nil
}
func (s *fakeStore) RenameErasedLabClient(context.Context, retentionRepo.LabTelemetryErasure) error {
	return nil
}
func (s *fakeStore) LabArchiveChunksForScrub(context.Context, uuid.UUID, uuid.UUID, int32) ([]retentionRepo.LabArchiveChunk, error) {
	return nil, nil
}
func (s *fakeStore) SaveScrubbedLabArchiveChunk(context.Context, uuid.UUID, []byte) error { return nil }
func (s *fakeStore) PurgeEventLabTelemetry(_ context.Context, before, participantsBefore time.Time, _ int32) (int64, error) {
	if _, err := s.purge("lab_telemetry_participants", participantsBefore); err != nil {
		return 0, err
	}
	return s.purge("lab_telemetry", before)
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
func (s *fakeStore) PurgeExpiredTemporalCodes(_ context.Context, before time.Time, _ int32) (int64, error) {
	return s.purge("temporal_codes", before)
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

// policySource is the platform settings source: the policy in force, or a failure to read it.
type policySource struct {
	policy retentionModel.Policy
	err    error
	calls  int
}

func (s *policySource) RetentionPolicy(context.Context) (retentionModel.Policy, error) {
	s.calls++
	return s.policy, s.err
}

func newUC(store *fakeStore, notifier *fakeNotifier, remover *fakeRemover, batch int32) *retention.RetentionUseCase {
	policy := retentionModel.DefaultPolicy()
	policy.BatchSize = batch
	return newUCWith(store, notifier, remover, &policySource{policy: policy})
}

func newUCWith(store *fakeStore, notifier *fakeNotifier, remover *fakeRemover, policies *policySource) *retention.RetentionUseCase {
	if store.cutoffs == nil {
		store.cutoffs = map[string]time.Time{}
	}
	return retention.New(retention.Dependencies{
		Store: store, Notifier: notifier, Accounts: remover, Policies: policies,
		SignInURL: "https://id.example.test/sign-in",
		Now:       func() time.Time { return now },
	})
}

// A period saved in the platform settings applies on the next pass: the policy is read once per pass.
func TestEnforceDataRetention_UsesThePolicyInForce(t *testing.T) {
	policy := retentionModel.DefaultPolicy()
	policy.DeliveryLog = 3 * 24 * time.Hour
	source := &policySource{policy: policy}
	store := &fakeStore{purged: map[string][]int64{}}
	uc := newUCWith(store, &fakeNotifier{}, &fakeRemover{}, source)

	if err := uc.EnforceDataRetention(context.Background()); err != nil {
		t.Fatalf("EnforceDataRetention: %v", err)
	}
	if want := now.Add(-3 * 24 * time.Hour); !store.cutoffs["dispatches"].Equal(want) {
		t.Fatalf("dispatches cutoff: got %s, want %s", store.cutoffs["dispatches"], want)
	}
	if source.calls != 1 {
		t.Fatalf("the policy is read once per pass, read %d times", source.calls)
	}

	policy.DeliveryLog = 5 * 24 * time.Hour
	source.policy = policy
	if err := uc.EnforceDataRetention(context.Background()); err != nil {
		t.Fatalf("second pass: %v", err)
	}
	if want := now.Add(-5 * 24 * time.Hour); !store.cutoffs["dispatches"].Equal(want) {
		t.Fatalf("the next pass takes the new period: got %s, want %s", store.cutoffs["dispatches"], want)
	}
}

// Without the policy no pass runs: nothing is purged, warned or deleted.
func TestRetention_PolicyErrorPurgesNothing(t *testing.T) {
	store := &fakeStore{
		purged:   map[string][]int64{},
		toWarn:   [][]retentionModel.InactiveAccount{{{UserID: uuid.Must(uuid.NewV7())}}},
		toDelete: [][]retentionModel.WarnedAccount{{{UserID: uuid.Must(uuid.NewV7())}}},
	}
	notifier, remover := &fakeNotifier{}, &fakeRemover{}
	uc := newUCWith(store, notifier, remover, &policySource{err: errors.New("db down")})

	if err := uc.EnforceDataRetention(context.Background()); err == nil {
		t.Fatal("a pass without the policy must fail")
	}
	if err := uc.EnforceAccountInactivity(context.Background()); err == nil {
		t.Fatal("an inactivity pass without the policy must fail")
	}
	if len(store.cutoffs) != 0 || store.clearCall != 0 || len(notifier.to) != 0 || len(remover.removed) != 0 {
		t.Fatalf("nothing may run: cutoffs=%v cleared=%d sent=%v removed=%v", store.cutoffs, store.clearCall, notifier.to, remover.removed)
	}
}

// The inactivity pass takes its periods and batch size from the policy in force.
func TestEnforceAccountInactivity_UsesThePolicyInForce(t *testing.T) {
	policy := retentionModel.DefaultPolicy()
	policy.InactiveAccount, policy.InactivityGrace = 200*24*time.Hour, 10*24*time.Hour
	store := &fakeStore{}
	notifier := &fakeNotifier{}
	store.toWarn = [][]retentionModel.InactiveAccount{{{UserID: uuid.Must(uuid.NewV7()), FirstName: "Ann"}}}
	uc := newUCWith(store, notifier, &fakeRemover{}, &policySource{policy: policy})

	if err := uc.EnforceAccountInactivity(context.Background()); err != nil {
		t.Fatalf("EnforceAccountInactivity: %v", err)
	}
	want := policy.Cutoffs(now)
	if !store.cutoffs["inactive_since"].Equal(want.InactiveSince) || !store.cutoffs["warned_before"].Equal(want.WarnedBefore) {
		t.Fatalf("inactivity cutoffs: %+v", store.cutoffs)
	}
	if got := notifier.sent[0].DeletionDate; got != policy.DeletionDate(now).Format("02.01.2006") {
		t.Fatalf("deletion date: %s", got)
	}
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
		"sessions": want.SessionsExpiredBefore, "lab": want.LabParticipantsBefore, "lab_telemetry_participants": want.LabParticipantsBefore, "lab_rolled_up": want.LabRolledUpBefore, "capacity": want.LabObservedBefore, "lab_telemetry": want.LabObservedBefore,
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
	for _, name := range []string{"sessions", "lab_rolled_up", "lab_telemetry", "lab_telemetry_participants", "capacity", "dispatches", "signals", "analytics", "answers", "answer_files", "deleted", "invitations", "pending"} {
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

// The erasure queue is read whole, page by page, not only its first batch; the entries chosen as settled before
// the pass are closed after it.
func TestEnforceDataRetention_ScrubsTheWholeErasureQueueAndClosesSettledEntries(t *testing.T) {
	var all []retentionRepo.LabTelemetryErasure
	for i := 0; i < 7; i++ {
		all = append(all, retentionRepo.LabTelemetryErasure{
			UserID: uuid.Must(uuid.NewV7()), ClientName: "p-a", Pseudonym: "deleted-a", EventIDs: []uuid.UUID{uuid.Must(uuid.NewV7())}, QueuedAt: now.Add(time.Duration(i) * time.Minute),
		})
	}
	store := &fakeStore{erasures: all, settled: []uuid.UUID{all[0].UserID}}
	policy := retentionModel.DefaultPolicy()
	policy.BatchSize = 3
	uc := retention.New(retention.Dependencies{Store: store, Policies: &policySource{policy: policy}, Now: func() time.Time { return now }})
	if err := uc.EnforceDataRetention(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.scrubbed) != 7 {
		t.Fatalf("scrubbed %d of 7 entries: the queue was not read whole", len(store.scrubbed))
	}
	if len(store.deletedErasures) != 1 || store.deletedErasures[0] != all[0].UserID {
		t.Fatalf("closed entries = %v", store.deletedErasures)
	}
}

func TestEraseLabTelemetryOf_ScrubsTheQueuedPerson(t *testing.T) {
	store := &fakeStore{}
	uc := retention.New(retention.Dependencies{Store: store, Now: func() time.Time { return now }})
	user := uuid.Must(uuid.NewV7())
	entry, _, err := uc.EraseLabTelemetryOf(context.Background(), user)
	if err != nil || entry.UserID != user || len(store.scrubbed) != 0 {
		t.Fatalf("entry=%+v scrubbed=%v err=%v", entry, store.scrubbed, err)
	}
}
