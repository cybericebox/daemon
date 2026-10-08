package lablifecycleJob

import (
	"context"
	"errors"
	"testing"
)

type lifecycleUC struct {
	err error
	ctx context.Context
}

func (u *lifecycleUC) ReconcilePendingLabLifecycles(ctx context.Context) error {
	u.ctx = ctx
	return u.err
}
func TestWorkerPreservesContextAndFailure(t *testing.T) {
	sentinel := errors.New("agent unsupported")
	uc := &lifecycleUC{err: sentinel}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := NewWorker(uc).Work(ctx, nil); !errors.Is(err, sentinel) || uc.ctx != ctx {
		t.Fatal(err, uc.ctx)
	}
}
