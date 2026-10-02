package notifyJob

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/riverqueue/river"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/model/jobs"
	"github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/pkg/tools"
)

// fakeProcessor is a hand-rolled test double for the notify worker's IUseCase port.
// It records the last ProcessInput it received and returns a configurable error.
type fakeProcessor struct {
	called bool
	last   dispatchModel.ProcessInput
	err    error

	payload   dispatchModel.Payload
	loadErr   error
	loaded    bool
	discarded bool
}

func (f *fakeProcessor) LoadNotificationPayload(context.Context, uuid.UUID) (dispatchModel.Payload, error) {
	f.loaded = true
	return f.payload, f.loadErr
}

func (f *fakeProcessor) DiscardNotificationPayload(context.Context, uuid.UUID) error {
	f.discarded = true
	return nil
}

func (f *fakeProcessor) ProcessNotification(_ context.Context, in dispatchModel.ProcessInput) error {
	f.called = true
	f.last = in
	return f.err
}

func TestNotifyWorker_Work_EmptyVarsGuard(t *testing.T) {
	id1 := tools.NewUUIDv7()
	id2 := tools.NewUUIDv7()

	fake := &fakeProcessor{}
	w := &notifyWorker{uc: fake}

	job := &river.Job[jobsModel.NotifyArgs]{
		Args: jobsModel.NotifyArgs{
			DispatchID: id1,
			UserID:     id2,
			Type:       "flag_accepted",
			Vars:       nil, // empty — guard branch
		},
	}

	err := w.Work(context.Background(), job)
	require.NoError(t, err)

	require.True(t, fake.called, "processor.Process must be called even with nil Vars")
	assert.Equal(t, id1, fake.last.DispatchID)
	assert.Equal(t, id2, fake.last.UserID)
	assert.Equal(t, "flag_accepted", fake.last.Type)
	assert.NotNil(t, fake.last.Vars, "Vars must be a non-nil empty map, not nil")
	assert.Len(t, fake.last.Vars, 0)
}

func TestNotifyWorker_Work_PopulatedVarsDecode(t *testing.T) {
	id1 := tools.NewUUIDv7()
	id2 := tools.NewUUIDv7()

	fake := &fakeProcessor{}
	w := &notifyWorker{uc: fake}

	job := &river.Job[jobsModel.NotifyArgs]{
		Args: jobsModel.NotifyArgs{
			DispatchID: id1,
			UserID:     id2,
			Type:       "flag_accepted",
			Vars:       json.RawMessage(`{"Challenge":"SQLi","Points":100}`),
		},
	}

	err := w.Work(context.Background(), job)
	require.NoError(t, err)

	require.True(t, fake.called)
	assert.Equal(t, "SQLi", fake.last.Vars["Challenge"])
	// JSON numbers decode to float64 — document the round-trip semantics explicitly.
	assert.Equal(t, float64(100), fake.last.Vars["Points"])
}

func TestNotifyWorker_Work_MalformedVarsReturnsError(t *testing.T) {
	id1 := tools.NewUUIDv7()
	id2 := tools.NewUUIDv7()

	fake := &fakeProcessor{err: errors.New("should not be called")}
	w := &notifyWorker{uc: fake}

	job := &river.Job[jobsModel.NotifyArgs]{
		Args: jobsModel.NotifyArgs{
			DispatchID: id1,
			UserID:     id2,
			Type:       "flag_accepted",
			Vars:       json.RawMessage(`{bad json`),
		},
	}

	err := w.Work(context.Background(), job)
	require.Error(t, err, "Work must return an error for malformed JSON")
	assert.False(t, fake.called, "processor.Process must NOT be called when JSON decode fails")
}

func TestNotifyWorker_Work_DeferralSnoozesTheJob(t *testing.T) {
	fake := &fakeProcessor{err: &dispatchModel.DeferredError{Message: dispatchModel.DeferredQuotaMessage, RetryAfter: 10 * time.Minute}}
	w := &notifyWorker{uc: fake}

	err := w.Work(context.Background(), &river.Job[jobsModel.NotifyArgs]{Args: jobsModel.NotifyArgs{Type: "flag_accepted"}})

	var snooze *river.JobSnoozeError
	require.ErrorAs(t, err, &snooze)
	assert.Equal(t, 10*time.Minute, snooze.Duration)
}

func TestNotifyWorker_Work_ReadsTheSealedPayload(t *testing.T) {
	fake := &fakeProcessor{payload: dispatchModel.Payload{Vars: json.RawMessage(`{"Name":"Ira"}`)}}
	w := &notifyWorker{uc: fake}

	err := w.Work(context.Background(), &river.Job[jobsModel.NotifyArgs]{Args: jobsModel.NotifyArgs{DispatchID: tools.NewUUIDv7(), Type: "x"}})

	require.NoError(t, err)
	assert.Equal(t, "Ira", fake.last.Vars["Name"])
	assert.True(t, fake.discarded, "a sent notification drops its payload")
}

func TestNotifyWorker_Work_LegacyJobRunsFromItsArguments(t *testing.T) {
	fake := &fakeProcessor{}
	w := &notifyWorker{uc: fake}

	err := w.Work(context.Background(), &river.Job[jobsModel.NotifyArgs]{Args: jobsModel.NotifyArgs{Type: "x", Vars: json.RawMessage(`{"A":"b"}`)}})

	require.NoError(t, err)
	assert.False(t, fake.loaded, "a legacy job never reads a stored payload")
	assert.Equal(t, "b", fake.last.Vars["A"])
}

func TestNotifyWorker_Work_MissingPayloadCancelsOnce(t *testing.T) {
	fake := &fakeProcessor{loadErr: dispatchModel.ErrPayloadGone}
	w := &notifyWorker{uc: fake}

	err := w.Work(context.Background(), &river.Job[jobsModel.NotifyArgs]{Args: jobsModel.NotifyArgs{Type: "x"}})

	var cancel *river.JobCancelError
	require.ErrorAs(t, err, &cancel)
	assert.False(t, fake.called)
}
