package eventanalyticsJob

import (
	"context"
	"testing"
)

type recordingUseCase struct{ calls int }

func (u *recordingUseCase) RefreshEventAnalytics(context.Context) error {
	u.calls++
	return nil
}

func TestWorkerRunsTheRollupPass(t *testing.T) {
	uc := &recordingUseCase{}
	if err := NewWorker(uc).Work(context.Background(), nil); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if uc.calls != 1 {
		t.Fatalf("RefreshEventAnalytics calls = %d, want 1", uc.calls)
	}
}
