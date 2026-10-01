package labaccesssyncJob

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/riverqueue/river"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type recordingUseCase struct {
	called bool
	err    error
}

func (u *recordingUseCase) ReconcilePendingLabAccess(context.Context) error {
	u.called = true
	return u.err
}

func TestWorkerReconcilesPendingLaboratoryAccess(t *testing.T) {
	uc := &recordingUseCase{}
	worker := NewWorker(uc)
	if err := worker.Work(context.Background(), &river.Job[jobsModel.LabAccessSyncArgs]{}); err != nil || !uc.called {
		t.Fatalf("Work err=%v called=%v", err, uc.called)
	}
}

func TestWorkerSnoozesWhileGroupTerminating(t *testing.T) {
	uc := &recordingUseCase{err: fmt.Errorf("sync: %w", &infraModel.TerminatingError{Message: "terminating", RetryAfter: 7 * time.Second})}
	err := NewWorker(uc).Work(context.Background(), &river.Job[jobsModel.LabAccessSyncArgs]{})
	snooze, ok := errors.AsType[*river.JobSnoozeError](err)
	if !ok || snooze.Duration != 7*time.Second {
		t.Fatalf("expected snooze of 7s, got %v", err)
	}
}

func TestWorkerStillFailsOnOtherErrors(t *testing.T) {
	boom := errors.New("boom")
	err := NewWorker(&recordingUseCase{err: boom}).Work(context.Background(), &river.Job[jobsModel.LabAccessSyncArgs]{})
	if !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}
