package eventLabRepo_test

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventLabRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventLabModel "github.com/cybericebox/daemon/internal/model/eventLab"
	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"
	"testing"
	"time"
)

func TestUpdateWritesWholeAggregateWithRevisionGuard(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	repo := eventLabRepo.New(q)
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	l := eventLabModel.Lab{ID: uuid.Must(uuid.NewV7()), Revision: 2, ObservedRevision: 1, AgentUID: "uid", AgentGeneration: 3, OperationID: uuid.Must(uuid.NewV7()), DesiredState: "Stopped", ActualState: "StopFailed", CloseReason: "solved", ClosedAt: &now, SnapshotMode: "required", SnapshotState: "Failed", RetentionUntil: &now, ProtectedUntil: &now, ActualStoppedAt: &now, ObservedAt: &now, Materialized: true, RuntimeReady: false, Allocation: eventLabModel.Allocation{RuntimeState: "Allocated", StorageState: "Retained", ConfiguredRequests: eventLabModel.Compute{CPUMillicores: 50}}, FailureCode: "capture_failed", FailureMessage: "quota", AccessFenced: true, AccessFencedAt: &now, AccessFenceVPNBootID: "boot", NextAttemptAt: now, UpdatedAt: now}
	q.EXPECT().UpdateEventTeamLab(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdateEventTeamLabParams) (int64, error) {
		if p.ID != l.ID || p.ExpectedRevision != 1 || p.DesiredRevision != 2 || p.ObservedRevision != 1 || p.AgentUid != "uid" || p.AgentGeneration != 3 || p.OperationID != l.OperationID || p.DesiredState != "Stopped" || p.ActualState != "StopFailed" || p.CloseReason.String != "solved" || p.SnapshotMode != "required" || p.SnapshotState != "Failed" || !p.Materialized || p.RuntimeReady || p.FailureCode != "capture_failed" || p.FailureMessage != "quota" || !p.AccessFenced || p.AccessFenceVpnBootID != "boot" || !p.LogicalClosedAt.Valid || !p.RetentionUntil.Valid || !p.ProtectedUntil.Valid || !p.ActualStoppedAt.Valid || !p.ObservedAt.Valid || !p.AccessFencedAt.Valid || !p.NextAttemptAt.Equal(now) || !p.UpdatedAt.Equal(now) {
			t.Fatalf("incomplete aggregate write: %+v", p)
		}
		var allocation eventLabModel.Allocation
		if err := json.Unmarshal(p.Allocation, &allocation); err != nil || allocation.ConfiguredRequests.CPUMillicores != 50 || allocation.RuntimeState != "Allocated" {
			t.Fatal(allocation, err)
		}
		return 1, nil
	})
	if ok, err := repo.Update(context.Background(), l, 1); !ok || err != nil {
		t.Fatal(ok, err)
	}
}
func TestRepositoryReadsHistoricalRowsWithoutDomainValidation(t *testing.T) {
	l, err := eventLabRepo.ToDomain(postgres.EventTeamLab{Allocation: []byte(`{}`), Generation: -1, SnapshotMode: "old-mode", DesiredRevision: 0})
	if err != nil || l.Generation != -1 || l.SnapshotMode != "old-mode" {
		t.Fatal(l, err)
	}
}

func TestRepositoryCorruptHistoricalAllocationLoadsAsUnknown(t *testing.T) {
	l, err := eventLabRepo.ToDomain(postgres.EventTeamLab{Allocation: []byte(`{"RuntimeState":2,"ReleasedAt":"bad"}`)})
	if err != nil || l.Allocation.RuntimeState != "Unknown" || l.Allocation.ReleasedAt != nil {
		t.Fatal(l, err)
	}
}
