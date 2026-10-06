package labcleanupJob

import (
	"context"
	"testing"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type recordingUseCase struct{ withdrawnCalls, queuedCalls int }

func (u *recordingUseCase) CleanupWithdrawnLaboratories(context.Context) error {
	u.withdrawnCalls++
	return nil
}

func (u *recordingUseCase) CleanupQueuedLabGroups(context.Context) error {
	u.queuedCalls++
	return nil
}

func TestWorkerRunsWithdrawnLaboratoryCleanup(t *testing.T) {
	uc := &recordingUseCase{}
	worker := NewWorker(uc)
	if err := worker.Work(context.Background(), nil); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if uc.withdrawnCalls != 1 || uc.queuedCalls != 1 {
		t.Fatalf("cleanup calls = withdrawn %d, queued %d; want one each", uc.withdrawnCalls, uc.queuedCalls)
	}
	_ = jobsModel.LabCleanupArgs{}
}
