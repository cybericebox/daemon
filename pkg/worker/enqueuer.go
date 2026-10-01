package worker

import (
	"context"

	"github.com/cybericebox/daemon/internal/model"
)

type (
	jobArgs interface {
		Kind() string
	}

	enqueuer struct {
		// factory holds the (late-bound) River client. The enqueuer reads it
		// through the factory at Enqueue time rather than snapshotting it at
		// construction — ucs (and thus NewEnqueuer) is built BEFORE the worker
		// starts (Initialize sets factory.client), so a snapshot taken at
		// construction would capture a nil client and never see the real one.
		factory *workerClient
	}
	// IEnqueuer is the interface for enqueuing jobs into the River queue.
	// It is used by the use-case layer to enqueue jobs without depending on the River implementation.
	IEnqueuer interface {
		Enqueue(ctx context.Context, args jobArgs) error
	}

	// noopEnqueuer is a test-only enqueuer that discards all jobs.
	noopEnqueuer struct{}
)

// NewEnqueuer returns a no-op IEnqueuer suitable for unit tests where the
// enqueue code path is never exercised (ProcessNotification never enqueues).
func NewEnqueuer() IEnqueuer {
	return &noopEnqueuer{}
}

func (n *noopEnqueuer) Enqueue(_ context.Context, _ jobArgs) error {
	return nil
}

func (e *enqueuer) Enqueue(ctx context.Context, args jobArgs) error {
	if e.factory.client == nil {
		return model.ErrPlatform.WithMessage("River client is not initialized").Err()
	}
	_, err := e.factory.client.Insert(ctx, args, nil)
	return err
}
