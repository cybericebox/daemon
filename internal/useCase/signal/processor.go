package signalUseCase

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

const (
	processorBatchSize = 100
	hookRetryDelay     = 5 * time.Second
)

// ExecutionStore persists claiming and completion independently for each
// signal/hook pair. Its implementations must be safe when multiple process
// instances claim work concurrently.
type ExecutionStore interface {
	ClaimSignals(context.Context, time.Time, int) ([]signalModel.Signal, error)
	EnsureHookExecution(context.Context, uuid.UUID, string, time.Time) error
	ClaimHookExecution(context.Context, uuid.UUID, string, time.Time) (bool, error)
	CompleteHookExecution(context.Context, uuid.UUID, string, time.Time) error
	RetryHookExecution(context.Context, uuid.UUID, string, time.Time, string) error
	CountIncompleteHookExecutions(context.Context, uuid.UUID) (int64, error)
	CompleteSignal(context.Context, uuid.UUID, time.Time) error
	RetrySignal(context.Context, uuid.UUID, time.Time, string) error
}

// Processor turns claimed outbox rows into independently tracked hook runs.
// A hook failure is recorded and rescheduled without rerunning completed
// siblings; only storage failures fail the periodic worker invocation.
type Processor struct {
	store    ExecutionStore
	payloads *signalModel.Registry
	hooks    *signalModel.HookRegistry
	now      func() time.Time
}

func NewProcessor(
	store ExecutionStore,
	payloads *signalModel.Registry,
	hooks *signalModel.HookRegistry,
	now func() time.Time,
) *Processor {
	return &Processor{store: store, payloads: payloads, hooks: hooks, now: now}
}

func (p *Processor) ProcessPending(ctx context.Context) error {
	now := p.now().UTC()
	signals, err := p.store.ClaimSignals(ctx, now, processorBatchSize)
	if err != nil {
		return fmt.Errorf("signal: claim outbox: %w", err)
	}
	for _, signal := range signals {
		if err := p.processSignal(ctx, signal, now); err != nil {
			return err
		}
	}
	return nil
}

func (p *Processor) processSignal(ctx context.Context, signal signalModel.Signal, now time.Time) error {
	// Decoding here validates the durable payload before any hook receives it.
	// Hooks still receive the raw immutable envelope so each remains decoupled
	// from other hooks' payload requirements.
	if _, err := p.payloads.Decode(signal.Type, signal.Payload); err != nil {
		return p.retrySignal(ctx, signal.ID, now, fmt.Sprintf("invalid signal payload: %v", err))
	}

	for _, hook := range p.hooks.HooksFor(signal.Type) {
		if err := p.store.EnsureHookExecution(ctx, signal.ID, hook.Name(), now); err != nil {
			return fmt.Errorf("signal: ensure hook execution %q: %w", hook.Name(), err)
		}
		claimed, err := p.store.ClaimHookExecution(ctx, signal.ID, hook.Name(), now)
		if err != nil {
			return fmt.Errorf("signal: claim hook execution %q: %w", hook.Name(), err)
		}
		if !claimed {
			continue
		}
		if err := hook.Handle(ctx, signal); err != nil {
			if retryErr := p.store.RetryHookExecution(ctx, signal.ID, hook.Name(), now.Add(hookRetryDelay), err.Error()); retryErr != nil {
				return fmt.Errorf("signal: retry hook execution %q: %w", hook.Name(), retryErr)
			}
			return p.retrySignal(ctx, signal.ID, now, fmt.Sprintf("hook %q failed", hook.Name()))
		}
		if err := p.store.CompleteHookExecution(ctx, signal.ID, hook.Name(), now); err != nil {
			return fmt.Errorf("signal: complete hook execution %q: %w", hook.Name(), err)
		}
	}

	incomplete, err := p.store.CountIncompleteHookExecutions(ctx, signal.ID)
	if err != nil {
		return fmt.Errorf("signal: count incomplete hooks: %w", err)
	}
	if incomplete > 0 {
		return p.retrySignal(ctx, signal.ID, now, "hook executions remain pending")
	}
	if err := p.store.CompleteSignal(ctx, signal.ID, now); err != nil {
		return fmt.Errorf("signal: complete outbox row: %w", err)
	}
	return nil
}

func (p *Processor) retrySignal(ctx context.Context, id uuid.UUID, now time.Time, reason string) error {
	if err := p.store.RetrySignal(ctx, id, now.Add(hookRetryDelay), reason); err != nil {
		return fmt.Errorf("signal: retry outbox row: %w", err)
	}
	return nil
}
