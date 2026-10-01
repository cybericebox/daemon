// Package infrastructureAgentRepo persists the agent registry: the projection of the
// environment-configured agent (no endpoint, no credentials: those stay deployment configuration)
// and the admin-configured agents with their connection material as ciphertext.
package infrastructureAgentRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

type Queries interface {
	ListInfrastructureAgents(context.Context) ([]postgres.InfrastructureAgent, error)
	DeleteInfrastructureAgentByKey(context.Context, string) (int64, error)
	UpsertInfrastructureAgent(context.Context, postgres.UpsertInfrastructureAgentParams) (postgres.InfrastructureAgent, error)
	GetInfrastructureAgent(context.Context, uuid.UUID) (postgres.InfrastructureAgent, error)
	CreateAdminInfrastructureAgent(context.Context, postgres.CreateAdminInfrastructureAgentParams) (postgres.InfrastructureAgent, error)
	UpdateAdminInfrastructureAgent(context.Context, postgres.UpdateAdminInfrastructureAgentParams) (int64, error)
	DeleteAdminInfrastructureAgent(context.Context, uuid.UUID) (int64, error)
	SetInfrastructureAgentCertificate(context.Context, postgres.SetInfrastructureAgentCertificateParams) (int64, error)
	SetInfrastructureAgentAccessKey(context.Context, postgres.SetInfrastructureAgentAccessKeyParams) (int64, error)
	SetInfrastructureAgentRetiredKeys(context.Context, postgres.SetInfrastructureAgentRetiredKeysParams) (int64, error)
	SetInfrastructureAgentCapacity(context.Context, postgres.SetInfrastructureAgentCapacityParams) (int64, error)
	ArchiveInfrastructureAgent(context.Context, postgres.ArchiveInfrastructureAgentParams) (int64, error)
	ReplaceInfrastructureAgentCredentials(context.Context, postgres.ReplaceInfrastructureAgentCredentialsParams) (int64, error)
}

// RemoveConfiguredPrimary removes the projection when deployment configuration
// no longer supplies an agent. Connection state is config-owned, so a stale
// row must never make a no-agent boot appear configured.
func (r *Repository) RemoveConfiguredPrimary(ctx context.Context) error {
	_, err := r.q.DeleteInfrastructureAgentByKey(ctx, infraModel.ConfiguredPrimaryAgentKey)
	return err
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) List(ctx context.Context) ([]infraModel.AgentRegistration, error) {
	rows, err := r.q.ListInfrastructureAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]infraModel.AgentRegistration, 0, len(rows))
	for _, row := range rows {
		out = append(out, toDomain(row))
	}
	return out, nil
}

// ReconcileConfiguredPrimary records only whether the environment-backed
// primary agent is configured. It never persists its endpoint or TLS settings.
func (r *Repository) ReconcileConfiguredPrimary(ctx context.Context, now time.Time) (infraModel.AgentRegistration, error) {
	row, err := r.q.UpsertInfrastructureAgent(ctx, postgres.UpsertInfrastructureAgentParams{
		ID: uuid.Must(uuid.NewV7()), Key: infraModel.ConfiguredPrimaryAgentKey,
		// Name stays empty: the display label is UI text and belongs to the frontend i18n.
		Name: "", Configured: true,
		CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		return infraModel.AgentRegistration{}, err
	}
	return toDomain(row), nil
}

func toDomain(row postgres.InfrastructureAgent) infraModel.AgentRegistration {
	reg := infraModel.AgentRegistration{
		ID: row.ID, Key: row.Key, Name: row.Name, Configured: row.Configured, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
		Source: row.Source, Endpoint: row.Endpoint, Enabled: row.Enabled, Priority: int(row.Priority), HasCA: row.CaPem != "",
		Tenant: row.Tenant, AccessKeyID: row.AccessKeyID,
	}
	if row.CertNotAfter.Valid {
		t := row.CertNotAfter.Time
		reg.CertNotAfter = &t
	}
	if row.CapacitySeenAt.Valid {
		t := row.CapacitySeenAt.Time
		reg.CapacitySeenAt = &t
	}
	if row.ArchivedAt.Valid {
		t := row.ArchivedAt.Time
		reg.ArchivedAt = &t
	}
	reg.CapacityCPUMillicores = int8Ptr(row.CapacityCpuMillicores)
	reg.CapacityMemoryBytes = int8Ptr(row.CapacityMemoryBytes)
	return reg
}

func toRecord(row postgres.InfrastructureAgent) infraModel.AgentRecord {
	record := infraModel.AgentRecord{
		AgentRegistration: toDomain(row), CertPEM: row.ClientCertPem, KeyCiphertext: row.ClientKeyCiphertext, CAPEM: row.CaPem,
		AccessPrivateKeyCiphertext: row.AccessPrivateKeyCiphertext, AccessPublicKey: row.AccessPublicKey,
	}
	// A malformed list reads as empty: retired keys are bookkeeping, and a bad value must not hide the agent.
	_ = json.Unmarshal(row.RetiredAccessKeys, &record.RetiredAccessKeys)
	return record
}

func retiredJSON(keys []infraModel.RetiredKey) ([]byte, error) {
	if keys == nil {
		keys = []infraModel.RetiredKey{}
	}
	return json.Marshal(keys)
}

func int8Ptr(v pgtype.Int8) *int64 {
	if !v.Valid {
		return nil
	}
	n := v.Int64
	return &n
}

// ListRecords lists the agents in use (not archived) with their stored material.
func (r *Repository) ListRecords(ctx context.Context) ([]infraModel.AgentRecord, error) {
	return r.list(ctx, false)
}

// ListAllRecords lists every agent, the archived ones too.
func (r *Repository) ListAllRecords(ctx context.Context) ([]infraModel.AgentRecord, error) {
	return r.list(ctx, true)
}

func (r *Repository) list(ctx context.Context, withArchived bool) ([]infraModel.AgentRecord, error) {
	rows, err := r.q.ListInfrastructureAgents(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]infraModel.AgentRecord, 0, len(rows))
	for _, row := range rows {
		if row.ArchivedAt.Valid && !withArchived {
			continue
		}
		out = append(out, toRecord(row))
	}
	return out, nil
}

// Get loads one agent with its stored material; the error is the repository's not-found.
func (r *Repository) Get(ctx context.Context, id uuid.UUID) (infraModel.AgentRecord, error) {
	row, err := r.q.GetInfrastructureAgent(ctx, id)
	if err != nil {
		return infraModel.AgentRecord{}, err
	}
	return toRecord(row), nil
}

// CreateAdmin stores a newly enrolled agent; its key is its id. The error of an endpoint that is
// already added is the repository's unique violation.
func (r *Repository) CreateAdmin(ctx context.Context, a infraModel.AgentRecord) (infraModel.AgentRecord, error) {
	row, err := r.q.CreateAdminInfrastructureAgent(ctx, postgres.CreateAdminInfrastructureAgentParams{
		ID: a.ID, Key: a.ID.String(), Name: a.Name, Endpoint: a.Endpoint, Tenant: a.Tenant,
		ClientCertPem: a.CertPEM, ClientKeyCiphertext: a.KeyCiphertext, CertNotAfter: notAfter(a.CertNotAfter), CaPem: a.CAPEM,
		AccessKeyID: a.AccessKeyID, AccessPrivateKeyCiphertext: a.AccessPrivateKeyCiphertext, AccessPublicKey: a.AccessPublicKey,
		Enabled: a.Enabled, Priority: int32(a.Priority), CreatedAt: a.CreatedAt, UpdatedAt: a.UpdatedAt,
	})
	if err != nil {
		return infraModel.AgentRecord{}, err
	}
	return toRecord(row), nil
}

func notAfter(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

// UpdateAdmin changes the label, order, switch and server CA of an admin agent.
func (r *Repository) UpdateAdmin(ctx context.Context, a infraModel.AgentRecord) (bool, error) {
	n, err := r.q.UpdateAdminInfrastructureAgent(ctx, postgres.UpdateAdminInfrastructureAgentParams{
		ID: a.ID, Name: a.Name, CaPem: a.CAPEM, Enabled: a.Enabled, Priority: int32(a.Priority), UpdatedAt: a.UpdatedAt,
	})
	return n > 0, err
}

// SetCertificate stores a renewed client certificate with its new key.
func (r *Repository) SetCertificate(ctx context.Context, id uuid.UUID, certPEM, keyCiphertext, tenant string, notAfterAt time.Time, now time.Time) (bool, error) {
	n, err := r.q.SetInfrastructureAgentCertificate(ctx, postgres.SetInfrastructureAgentCertificateParams{
		ID: id, ClientCertPem: certPEM, ClientKeyCiphertext: keyCiphertext, CertNotAfter: notAfter(&notAfterAt), Tenant: tenant, UpdatedAt: now,
	})
	return n > 0, err
}

// SetAccessKey stores a rotated access key and the keys it retired.
func (r *Repository) SetAccessKey(ctx context.Context, id uuid.UUID, keyID, privateCiphertext, publicPEM string, retired []infraModel.RetiredKey, now time.Time) (bool, error) {
	encoded, err := retiredJSON(retired)
	if err != nil {
		return false, err
	}
	n, err := r.q.SetInfrastructureAgentAccessKey(ctx, postgres.SetInfrastructureAgentAccessKeyParams{
		ID: id, AccessKeyID: keyID, AccessPrivateKeyCiphertext: privateCiphertext, AccessPublicKey: publicPEM, RetiredAccessKeys: encoded, UpdatedAt: now,
	})
	return n > 0, err
}

// SetRetiredKeys stores the retired keys that still wait for removal.
func (r *Repository) SetRetiredKeys(ctx context.Context, id uuid.UUID, retired []infraModel.RetiredKey, now time.Time) (bool, error) {
	encoded, err := retiredJSON(retired)
	if err != nil {
		return false, err
	}
	n, err := r.q.SetInfrastructureAgentRetiredKeys(ctx, postgres.SetInfrastructureAgentRetiredKeysParams{ID: id, RetiredAccessKeys: encoded, UpdatedAt: now})
	return n > 0, err
}

// DeleteAdmin removes an admin agent; false when there is none.
func (r *Repository) DeleteAdmin(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q.DeleteAdminInfrastructureAgent(ctx, id)
	return n > 0, err
}

// SetCapacity records the last known capacity of an agent: the tenant quota, nil = no limit on that
// resource, and when it was read.
func (r *Repository) SetCapacity(ctx context.Context, id uuid.UUID, cpuMillicores, memoryBytes *int64, seenAt time.Time) error {
	_, err := r.q.SetInfrastructureAgentCapacity(ctx, postgres.SetInfrastructureAgentCapacityParams{
		ID: id, CapacityCpuMillicores: int8Of(cpuMillicores), CapacityMemoryBytes: int8Of(memoryBytes), SeenAt: pgtype.Timestamptz{Time: seenAt, Valid: true},
	})
	return err
}

func int8Of(v *int64) pgtype.Int8 {
	if v == nil {
		return pgtype.Int8{}
	}
	return pgtype.Int8{Int64: *v, Valid: true}
}

// Archive soft-deletes an admin agent: its record stays, its keys, endpoint and certificate are wiped
// and it is renamed. False when there is none (or it is archived already).
func (r *Repository) Archive(ctx context.Context, id uuid.UUID, name string, at time.Time) (bool, error) {
	n, err := r.q.ArchiveInfrastructureAgent(ctx, postgres.ArchiveInfrastructureAgentParams{ID: id, Name: name, ArchivedAt: pgtype.Timestamptz{Time: at, Valid: true}})
	return n > 0, err
}

// ReplaceCredentials stores the keys of a reconnected agent (the same cluster enrolled again).
func (r *Repository) ReplaceCredentials(ctx context.Context, a infraModel.AgentRecord) (bool, error) {
	n, err := r.q.ReplaceInfrastructureAgentCredentials(ctx, postgres.ReplaceInfrastructureAgentCredentialsParams{
		ID: a.ID, ClientCertPem: a.CertPEM, ClientKeyCiphertext: a.KeyCiphertext, CertNotAfter: notAfter(a.CertNotAfter), Tenant: a.Tenant, CaPem: a.CAPEM,
		AccessKeyID: a.AccessKeyID, AccessPrivateKeyCiphertext: a.AccessPrivateKeyCiphertext, AccessPublicKey: a.AccessPublicKey, UpdatedAt: a.UpdatedAt,
	})
	return n > 0, err
}
