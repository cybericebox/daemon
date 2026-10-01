package signalUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

const processorTestType signalModel.Type = "participant.open_registration.completed"

type processorTestPayload struct {
	ScopeEventID uuid.UUID `json:"scope_event_id"`
}

func (p *processorTestPayload) Routing() signalModel.Routing {
	return signalModel.Routing{ScopeEventID: p.ScopeEventID}
}

type recordingHook struct {
	name  string
	fails bool
	calls int
}

func (h *recordingHook) Name() string { return h.name }
func (h *recordingHook) Handle(context.Context, signalModel.Signal) error {
	h.calls++
	if h.fails {
		return errors.New("planned hook failure")
	}
	return nil
}

type processorStore struct {
	signals      []signalModel.Signal
	claimedHooks map[string]bool
	completed    map[string]bool
	retried      map[string]bool
	completedSig bool
	retriedSig   bool
}

func newProcessorStore(signal signalModel.Signal) *processorStore {
	return &processorStore{
		signals:      []signalModel.Signal{signal},
		claimedHooks: map[string]bool{},
		completed:    map[string]bool{},
		retried:      map[string]bool{},
	}
}

func (s *processorStore) ClaimSignals(context.Context, time.Time, int) ([]signalModel.Signal, error) {
	return s.signals, nil
}
func (s *processorStore) EnsureHookExecution(context.Context, uuid.UUID, string, time.Time) error {
	return nil
}
func (s *processorStore) ClaimHookExecution(_ context.Context, _ uuid.UUID, name string, _ time.Time) (bool, error) {
	if s.completed[name] || s.claimedHooks[name] {
		return false, nil
	}
	s.claimedHooks[name] = true
	return true, nil
}
func (s *processorStore) CompleteHookExecution(_ context.Context, _ uuid.UUID, name string, _ time.Time) error {
	s.completed[name] = true
	return nil
}
func (s *processorStore) RetryHookExecution(_ context.Context, _ uuid.UUID, name string, _ time.Time, _ string) error {
	s.retried[name] = true
	s.claimedHooks[name] = false
	return nil
}
func (s *processorStore) CountIncompleteHookExecutions(context.Context, uuid.UUID) (int64, error) {
	for name := range s.claimedHooks {
		if !s.completed[name] {
			return 1, nil
		}
	}
	return 0, nil
}
func (s *processorStore) CompleteSignal(context.Context, uuid.UUID, time.Time) error {
	s.completedSig = true
	return nil
}
func (s *processorStore) RetrySignal(context.Context, uuid.UUID, time.Time, string) error {
	s.retriedSig = true
	return nil
}

func TestProcessorDoesNotRerunCompletedHookWhenSiblingRetries(t *testing.T) {
	t.Parallel()

	payload, err := json.Marshal(&processorTestPayload{ScopeEventID: uuid.Must(uuid.NewV7())})
	require.NoError(t, err)
	signal := signalModel.Signal{ID: uuid.Must(uuid.NewV7()), Type: processorTestType, OccurredAt: time.Now(), Payload: payload}
	store := newProcessorStore(signal)
	registry := signalModel.NewRegistry()
	registry.Register(processorTestType, func() signalModel.Payload { return &processorTestPayload{} })
	hooks := signalModel.NewHookRegistry()
	completed := &recordingHook{name: "completed"}
	failing := &recordingHook{name: "failing", fails: true}
	hooks.RegisterExact(processorTestType, completed)
	hooks.RegisterWildcard(failing)

	processor := NewProcessor(store, registry, hooks, func() time.Time { return time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC) })
	require.NoError(t, processor.ProcessPending(context.Background()))
	require.Equal(t, 1, completed.calls)
	require.Equal(t, 1, failing.calls)
	require.True(t, store.completed[completed.name])
	require.True(t, store.retried[failing.name])
	require.True(t, store.retriedSig)

	require.NoError(t, processor.ProcessPending(context.Background()))
	require.Equal(t, 1, completed.calls, "completed hook must not run again")
	require.Equal(t, 2, failing.calls)
}
