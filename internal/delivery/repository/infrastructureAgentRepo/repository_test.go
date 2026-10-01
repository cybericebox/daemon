package infrastructureAgentRepo

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

type queryStub struct {
	rows    []postgres.InfrastructureAgent
	created postgres.CreateInfrastructureAgentParams
}

func (q *queryStub) ListInfrastructureAgents(context.Context) ([]postgres.InfrastructureAgent, error) {
	return q.rows, nil
}
func (q *queryStub) GetInfrastructureAgent(context.Context, uuid.UUID) (postgres.InfrastructureAgent, error) {
	return postgres.InfrastructureAgent{}, nil
}
func (q *queryStub) CreateInfrastructureAgent(_ context.Context, p postgres.CreateInfrastructureAgentParams) (postgres.InfrastructureAgent, error) {
	q.created = p
	return postgres.InfrastructureAgent{ID: p.ID, Source: p.Source}, nil
}
func (q *queryStub) UpdateInfrastructureAgent(context.Context, postgres.UpdateInfrastructureAgentParams) (int64, error) {
	return 1, nil
}
func (q *queryStub) DeleteInfrastructureAgent(context.Context, uuid.UUID) (int64, error) {
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

func TestListRecordsHidesArchivedAndMapsTheRow(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	live := postgres.InfrastructureAgent{
		ID: uuid.Must(uuid.NewV7()), Name: "eu", Source: infraModel.AgentSourceEnv, Endpoint: "eu:443", Tenant: "platform", Enabled: true, Priority: 7,
		CapacityCpuMillicores: pgtype.Int8{Int64: 8000, Valid: true}, CapacitySeenAt: pgtype.Timestamptz{Time: now, Valid: true},
		RetiredAccessKeys: []byte(`[{"key_id":"k-old","retired_at":"2026-10-01T11:00:00Z"}]`), CaPem: "ca",
	}
	gone := postgres.InfrastructureAgent{ID: uuid.Must(uuid.NewV7()), ArchivedAt: pgtype.Timestamptz{Time: now, Valid: true}}
	broken := postgres.InfrastructureAgent{ID: uuid.Must(uuid.NewV7()), RetiredAccessKeys: []byte(`not json`)}
	repo := New(&queryStub{rows: []postgres.InfrastructureAgent{live, gone, broken}})

	records, err := repo.ListRecords(context.Background())
	if err != nil || len(records) != 2 {
		t.Fatalf("records = %d, %v; archived agents must be hidden", len(records), err)
	}
	got := records[0]
	if got.Source != infraModel.AgentSourceEnv || got.Priority != 7 || !got.HasCA || got.CapacityCPUMillicores == nil || *got.CapacityCPUMillicores != 8000 || got.CapacityMemoryBytes != nil ||
		got.CapacitySeenAt == nil || len(got.RetiredAccessKeys) != 1 || got.RetiredAccessKeys[0].KeyID != "k-old" {
		t.Fatalf("record = %+v", got)
	}
	if len(records[1].RetiredAccessKeys) != 0 {
		t.Fatal("a malformed retired list must read as empty, not hide the agent")
	}
	if all, _ := repo.ListAllRecords(context.Background()); len(all) != 3 {
		t.Fatalf("every agent is listed with archived: %d", len(all))
	}
}

func TestCreateKeepsHowTheAgentWasAdded(t *testing.T) {
	q := &queryStub{}
	a := infraModel.AgentRecord{AgentRegistration: infraModel.AgentRegistration{ID: uuid.Must(uuid.NewV7()), Source: infraModel.AgentSourceEnv, Name: "default"}}
	if _, err := New(q).Create(context.Background(), a); err != nil {
		t.Fatal(err)
	}
	if q.created.Source != infraModel.AgentSourceEnv || q.created.Key != a.ID.String() {
		t.Fatalf("created = %+v", q.created)
	}
}
