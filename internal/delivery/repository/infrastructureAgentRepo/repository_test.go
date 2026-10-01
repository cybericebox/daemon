package infrastructureAgentRepo

import (
	"context"
	"github.com/gofrs/uuid"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

type queryStub struct {
	deletedKey string
	upserted   postgres.UpsertInfrastructureAgentParams
}

func (q *queryStub) ListInfrastructureAgents(context.Context) ([]postgres.InfrastructureAgent, error) {
	return nil, nil
}
func (q *queryStub) DeleteInfrastructureAgentByKey(_ context.Context, key string) (int64, error) {
	q.deletedKey = key
	return 1, nil
}
func (q *queryStub) UpsertInfrastructureAgent(_ context.Context, p postgres.UpsertInfrastructureAgentParams) (postgres.InfrastructureAgent, error) {
	q.upserted = p
	return postgres.InfrastructureAgent{Key: p.Key, Name: p.Name, Configured: p.Configured}, nil
}

func (q *queryStub) GetInfrastructureAgent(context.Context, uuid.UUID) (postgres.InfrastructureAgent, error) {
	return postgres.InfrastructureAgent{}, nil
}
func (q *queryStub) CreateAdminInfrastructureAgent(context.Context, postgres.CreateAdminInfrastructureAgentParams) (postgres.InfrastructureAgent, error) {
	return postgres.InfrastructureAgent{}, nil
}
func (q *queryStub) UpdateAdminInfrastructureAgent(context.Context, postgres.UpdateAdminInfrastructureAgentParams) (int64, error) {
	return 1, nil
}
func (q *queryStub) DeleteAdminInfrastructureAgent(context.Context, uuid.UUID) (int64, error) {
	return 1, nil
}

func (q *queryStub) SetInfrastructureAgentCertificate(context.Context, postgres.SetInfrastructureAgentCertificateParams) (int64, error) {
	return 1, nil
}
func (q *queryStub) SetInfrastructureAgentAccessKey(context.Context, postgres.SetInfrastructureAgentAccessKeyParams) (int64, error) {
	return 1, nil
}
func (q *queryStub) SetInfrastructureAgentRetiredKeys(context.Context, postgres.SetInfrastructureAgentRetiredKeysParams) (int64, error) {
	return 1, nil
}

func (q *queryStub) SetInfrastructureAgentCapacity(context.Context, postgres.SetInfrastructureAgentCapacityParams) (int64, error) {
	return 1, nil
}
func (q *queryStub) ArchiveInfrastructureAgent(context.Context, postgres.ArchiveInfrastructureAgentParams) (int64, error) {
	return 1, nil
}
func (q *queryStub) ReplaceInfrastructureAgentCredentials(context.Context, postgres.ReplaceInfrastructureAgentCredentialsParams) (int64, error) {
	return 1, nil
}

func TestRemoveConfiguredPrimaryRemovesConfigurationProjection(t *testing.T) {
	q := &queryStub{}
	if err := New(q).RemoveConfiguredPrimary(context.Background()); err != nil {
		t.Fatalf("RemoveConfiguredPrimary() error = %v", err)
	}
	if q.deletedKey != infraModel.ConfiguredPrimaryAgentKey {
		t.Fatalf("deleted key = %q, want %q", q.deletedKey, infraModel.ConfiguredPrimaryAgentKey)
	}
}

func TestReconcileConfiguredPrimaryStoresEmptyName(t *testing.T) {
	q := &queryStub{}
	agent, err := New(q).ReconcileConfiguredPrimary(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("ReconcileConfiguredPrimary() error = %v", err)
	}
	if q.upserted.Key != infraModel.ConfiguredPrimaryAgentKey || q.upserted.Name != "" || !q.upserted.Configured {
		t.Fatalf("upserted = %+v, want configured primary key with empty name", q.upserted)
	}
	if agent.Name != "" {
		t.Fatalf("agent name = %q, want empty", agent.Name)
	}
}
