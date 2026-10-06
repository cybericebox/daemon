// Package infrastructureAgentRepo persists the agent registry: every agent is enrolled (added in the
// admin, or bootstrapped from the deployment config with an enrollment token) and keeps its connection
// material in the database: the certificate in plain, the private keys as ciphertext.
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
	GetInfrastructureAgent(context.Context, uuid.UUID) (postgres.InfrastructureAgent, error)
	CreateInfrastructureAgent(context.Context, postgres.CreateInfrastructureAgentParams) (postgres.InfrastructureAgent, error)
	UpdateInfrastructureAgent(context.Context, postgres.UpdateInfrastructureAgentParams) (int64, error)
	DeleteInfrastructureAgent(context.Context, uuid.UUID) (int64, error)
	SetInfrastructureAgentCertificate(context.Context, postgres.SetInfrastructureAgentCertificateParams) (int64, error)
	SetInfrastructureAgentAccessKey(context.Context, postgres.SetInfrastructureAgentAccessKeyParams) (int64, error)
	SetInfrastructureAgentRetiredKeys(context.Context, postgres.SetInfrastructureAgentRetiredKeysParams) (int64, error)
	SetInfrastructureAgentCapacity(context.Context, postgres.SetInfrastructureAgentCapacityParams) (int64, error)
	SetInfrastructureAgentMaxDevice(context.Context, postgres.SetInfrastructureAgentMaxDeviceParams) (int64, error)
	SetInfrastructureAgentMaintenance(context.Context, postgres.SetInfrastructureAgentMaintenanceParams) (int64, error)
	SetInfrastructureAgentFeatures(context.Context, postgres.SetInfrastructureAgentFeaturesParams) (int64, error)
	ArchiveInfrastructureAgent(context.Context, postgres.ArchiveInfrastructureAgentParams) (int64, error)
	ReplaceInfrastructureAgentCredentials(context.Context, postgres.ReplaceInfrastructureAgentCredentialsParams) (int64, error)
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
	if row.FeaturesAt.Valid && len(row.Features) > 0 {
		var f infraModel.AgentFeatures
		// A malformed report reads as none: features are a cache of what the agent said, refreshed on its next report.
		if json.Unmarshal(row.Features, &f) == nil {
			t := row.FeaturesAt.Time
			reg.Features, reg.FeaturesAt = &f, &t
		}
	}
	// A malformed list reads as not reported: the agent's next report rewrites it.
	if row.MaxDeviceCpuMillicores.Valid && row.MaxDeviceMemoryBytes.Valid {
		reg.MaxDevice = &infraModel.AgentDevice{CPUMillicores: row.MaxDeviceCpuMillicores.Int64, MemoryBytes: row.MaxDeviceMemoryBytes.Int64}
	}
	if len(row.MaintenanceWindows) > 0 && json.Unmarshal(row.MaintenanceWindows, &reg.Maintenance) == nil {
		reg.MaintenanceReported = true
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

// Create stores a newly enrolled agent (a.Source says how it was added); its key is its id. The error of an endpoint that is
// already added is the repository's unique violation.
func (r *Repository) Create(ctx context.Context, a infraModel.AgentRecord) (infraModel.AgentRecord, error) {
	row, err := r.q.CreateInfrastructureAgent(ctx, postgres.CreateInfrastructureAgentParams{
		ID: a.ID, Key: a.ID.String(), Source: a.Source, Name: a.Name, Endpoint: a.Endpoint, Tenant: a.Tenant,
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

// Update changes the label, order, switch and server CA of an agent.
func (r *Repository) Update(ctx context.Context, a infraModel.AgentRecord) (bool, error) {
	n, err := r.q.UpdateInfrastructureAgent(ctx, postgres.UpdateInfrastructureAgentParams{
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

// Delete removes an agent row; false when there is none.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q.DeleteInfrastructureAgent(ctx, id)
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

// SetMaxDevice records the largest device the agent last reported it can place; nil clears it.
func (r *Repository) SetMaxDevice(ctx context.Context, id uuid.UUID, d *infraModel.AgentDevice) error {
	params := postgres.SetInfrastructureAgentMaxDeviceParams{ID: id}
	if d != nil {
		params.MaxDeviceCpuMillicores, params.MaxDeviceMemoryBytes = int8Of(&d.CPUMillicores), int8Of(&d.MemoryBytes)
	}
	_, err := r.q.SetInfrastructureAgentMaxDevice(ctx, params)
	return err
}

// SetMaintenance records the maintenance windows the agent last reported.
func (r *Repository) SetMaintenance(ctx context.Context, id uuid.UUID, windows []infraModel.AgentMaintenanceWindow) error {
	if windows == nil {
		windows = []infraModel.AgentMaintenanceWindow{}
	}
	encoded, err := json.Marshal(windows)
	if err != nil {
		return err
	}
	_, err = r.q.SetInfrastructureAgentMaintenance(ctx, postgres.SetInfrastructureAgentMaintenanceParams{ID: id, MaintenanceWindows: encoded})
	return err
}

// SetFeatures records what the agent last reported the tenant can use, and when it was read.
func (r *Repository) SetFeatures(ctx context.Context, id uuid.UUID, features infraModel.AgentFeatures, seenAt time.Time) error {
	encoded, err := json.Marshal(features)
	if err != nil {
		return err
	}
	_, err = r.q.SetInfrastructureAgentFeatures(ctx, postgres.SetInfrastructureAgentFeaturesParams{
		ID: id, Features: encoded, SeenAt: pgtype.Timestamptz{Time: seenAt, Valid: true},
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
