package event

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/labBindingRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

type slowStatusInfra struct {
	Infrastructure
	delay          time.Duration
	inFlight, peak atomic.Int32
}

func (s *slowStatusInfra) LabStatus(context.Context, string, string) (exerciseModel.LabDeployStatus, error) {
	n := s.inFlight.Add(1)
	for {
		peak := s.peak.Load()
		if n <= peak || s.peak.CompareAndSwap(peak, n) {
			break
		}
	}
	time.Sleep(s.delay)
	s.inFlight.Add(-1)
	return exerciseModel.LabDeployStatus{Ready: true}, nil
}

// lt1029: the backend learned about 130 ready Labs one after another, ~10 a minute. The statuses are read in
// parallel with a bound.
func TestFetchLabStatusesReadsInParallelWithABound(t *testing.T) {
	infra := &slowStatusInfra{delay: 50 * time.Millisecond}
	u := &EventUseCase{infra: infra}
	deployed := time.Now()
	var pending []labBindingRepo.PendingLab
	for range 64 {
		pending = append(pending, labBindingRepo.PendingLab{Binding: labBindingModel.Binding{ID: uuid.Must(uuid.NewV7()), LabGroupName: "g", LabName: "l", DeployedAt: &deployed}})
	}
	pending = append(pending, labBindingRepo.PendingLab{Binding: labBindingModel.Binding{ID: uuid.Must(uuid.NewV7())}}) // not deployed: not asked
	started := time.Now()
	got := u.fetchLabStatuses(context.Background(), pending)
	elapsed := time.Since(started)
	require.Len(t, got, 64)
	require.Less(t, elapsed, 64*50*time.Millisecond/4, "sequential reads would take 3.2s")
	require.LessOrEqual(t, int(infra.peak.Load()), labStatusConcurrency)
	require.Greater(t, int(infra.peak.Load()), 1)
}
