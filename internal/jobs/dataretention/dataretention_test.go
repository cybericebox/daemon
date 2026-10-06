package dataretentionJob

import (
	"context"
	"testing"
)

type recordingUseCase struct{ calls int }

func (u *recordingUseCase) EnforceDataRetention(context.Context) error {
	u.calls++
	return nil
}

func TestWorkerRunsDataRetention(t *testing.T) {
	uc := &recordingUseCase{}
	if err := NewWorker(uc).Work(context.Background(), nil); err != nil {
		t.Fatalf("Work: %v", err)
	}
	if uc.calls != 1 {
		t.Fatalf("EnforceDataRetention calls = %d, want 1", uc.calls)
	}
}
