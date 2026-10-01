package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/infrastructureAgentRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/labPlacementRepo"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func newAdminAgent(name string, priority int, now time.Time) infraModel.AgentRecord {
	id := uuid.Must(uuid.NewV7())
	return infraModel.AgentRecord{
		AgentRegistration: infraModel.AgentRegistration{ID: id, Name: name, Endpoint: name + ".example.test:443", Enabled: true, Priority: priority, CreatedAt: now, UpdatedAt: now, Tenant: "platform", AccessKeyID: "k-" + name},
		CertPEM:           "cert-" + name, KeyCiphertext: "key-" + name, AccessPrivateKeyCiphertext: "access-" + name, AccessPublicKey: "pub-" + name, CAPEM: "",
	}
}

func TestInfrastructureAgents_EnrolledAgentsKeepKeysUntilRenewedAndEnvAgentIsUntouchable(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := infrastructureAgentRepo.New(db.Queries)
	now := time.Now().UTC().Truncate(time.Microsecond)

	if _, err := repo.ReconcileConfiguredPrimary(ctx, now); err != nil {
		t.Fatal(err)
	}
	a := newAdminAgent("alpha", 20, now)
	notAfter := now.Add(90 * 24 * time.Hour)
	a.CertNotAfter = &notAfter
	created, err := repo.CreateAdmin(ctx, a)
	if err != nil || created.Source != infraModel.AgentSourceAdmin || created.Key != a.ID.String() || created.Priority != 20 || !created.Enabled || created.HasCA ||
		created.Tenant != "platform" || created.AccessKeyID != "k-alpha" || created.CertNotAfter == nil || !created.CertNotAfter.Equal(notAfter) || len(created.RetiredAccessKeys) != 0 {
		t.Fatalf("created = %+v, %v", created, err)
	}
	// The same endpoint cannot be enrolled twice; another agent with the same tenant can.
	dup := newAdminAgent("alpha", 30, now)
	if _, err = repo.CreateAdmin(ctx, dup); err == nil {
		t.Fatal("the same endpoint twice must fail")
	}
	other := newAdminAgent("beta", 40, now)
	if _, err = repo.CreateAdmin(ctx, other); err != nil {
		t.Fatalf("the tenant is not unique: %v", err)
	}

	// An update changes the label, order, switch and server CA only.
	upd := a
	upd.Name, upd.Priority, upd.Enabled, upd.CAPEM, upd.UpdatedAt = "alpha2", 5, false, "ca-pem", now.Add(time.Minute)
	if ok, updErr := repo.UpdateAdmin(ctx, upd); updErr != nil || !ok {
		t.Fatalf("update = %v, %v", ok, updErr)
	}
	got, err := repo.Get(ctx, a.ID)
	if err != nil || got.Name != "alpha2" || got.Priority != 5 || got.Enabled || !got.HasCA || got.CertPEM != "cert-alpha" || got.KeyCiphertext != "key-alpha" || got.Endpoint != a.Endpoint {
		t.Fatalf("after update = %+v, %v", got, err)
	}

	// A renewed certificate replaces the certificate and the client key together.
	renewedAt := notAfter.Add(30 * 24 * time.Hour)
	if ok, setErr := repo.SetCertificate(ctx, a.ID, "cert-new", "key-new", "platform", renewedAt, now.Add(2*time.Minute)); setErr != nil || !ok {
		t.Fatalf("set certificate = %v, %v", ok, setErr)
	}
	if got, _ = repo.Get(ctx, a.ID); got.CertPEM != "cert-new" || got.KeyCiphertext != "key-new" || got.CertNotAfter == nil || !got.CertNotAfter.Equal(renewedAt) || got.AccessKeyID != "k-alpha" {
		t.Fatalf("renewed = %+v", got)
	}

	// A rotated access key retires the old one; the retired list round-trips and shrinks.
	retired := []infraModel.RetiredKey{{KeyID: "k-alpha", RetiredAt: now}}
	if ok, setErr := repo.SetAccessKey(ctx, a.ID, "k-2", "access-2", "pub-2", retired, now); setErr != nil || !ok {
		t.Fatalf("set access key = %v, %v", ok, setErr)
	}
	if got, _ = repo.Get(ctx, a.ID); got.AccessKeyID != "k-2" || got.AccessPrivateKeyCiphertext != "access-2" || got.AccessPublicKey != "pub-2" ||
		len(got.RetiredAccessKeys) != 1 || got.RetiredAccessKeys[0].KeyID != "k-alpha" || !got.RetiredAccessKeys[0].RetiredAt.Equal(now) {
		t.Fatalf("rotated = %+v", got)
	}
	if ok, setErr := repo.SetRetiredKeys(ctx, a.ID, nil, now); setErr != nil || !ok {
		t.Fatalf("set retired = %v, %v", ok, setErr)
	}
	if got, _ = repo.Get(ctx, a.ID); len(got.RetiredAccessKeys) != 0 {
		t.Fatalf("retired = %+v", got.RetiredAccessKeys)
	}

	// The environment agent can be neither changed nor deleted through the admin queries.
	records, err := repo.ListRecords(ctx)
	if err != nil || len(records) != 3 {
		t.Fatalf("records = %d, %v", len(records), err)
	}
	var env infraModel.AgentRecord
	for _, r := range records {
		if r.Source == infraModel.AgentSourceEnv {
			env = r
		}
	}
	if env.Key != infraModel.ConfiguredPrimaryAgentKey || !env.Enabled || env.Priority != 100 || env.Tenant != "" {
		t.Fatalf("env agent = %+v", env)
	}
	env.Name = "hijack"
	if ok, _ := repo.UpdateAdmin(ctx, env); ok {
		t.Fatal("the env agent must not be updated by the admin path")
	}
	if ok, _ := repo.SetCertificate(ctx, env.ID, "x", "y", "z", renewedAt, now); ok {
		t.Fatal("the env agent must not get a certificate through the admin path")
	}
	if ok, _ := repo.DeleteAdmin(ctx, env.ID); ok {
		t.Fatal("the env agent must not be deleted by the admin path")
	}
	if ok, err := repo.DeleteAdmin(ctx, a.ID); err != nil || !ok {
		t.Fatalf("delete admin = %v, %v", ok, err)
	}
}

func TestLabGroupPlacements_ClaimIsFirstWinsAndCountsPerAgent(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	agents := infrastructureAgentRepo.New(db.Queries)
	placements := labPlacementRepo.New(db.Queries)
	now := time.Now().UTC()
	a, err := agents.CreateAdmin(ctx, newAdminAgent("a", 1, now))
	if err != nil {
		t.Fatal(err)
	}
	b, err := agents.CreateAdmin(ctx, newAdminAgent("b", 2, now))
	if err != nil {
		t.Fatal(err)
	}

	if _, found, getErr := placements.Get(ctx, "tu-1"); getErr != nil || found {
		t.Fatalf("unplaced group: found=%v err=%v", found, getErr)
	}
	winner, err := placements.Claim(ctx, "tu-1", a.ID)
	if err != nil || winner != a.ID {
		t.Fatalf("first claim = %v, %v", winner, err)
	}
	// A racing second claim for another agent loses and learns the winner.
	if winner, err = placements.Claim(ctx, "tu-1", b.ID); err != nil || winner != a.ID {
		t.Fatalf("second claim = %v, %v, want the first agent", winner, err)
	}
	if _, err = placements.Claim(ctx, "tu-2", b.ID); err != nil {
		t.Fatal(err)
	}
	if got, found, _ := placements.Get(ctx, "tu-1"); !found || got != a.ID {
		t.Fatalf("get = %v %v", got, found)
	}
	counts, err := placements.CountByAgent(ctx)
	if err != nil || counts[a.ID] != 1 || counts[b.ID] != 1 {
		t.Fatalf("counts = %v, %v", counts, err)
	}
	if err = placements.Release(ctx, "tu-1"); err != nil {
		t.Fatal(err)
	}
	if _, found, _ := placements.Get(ctx, "tu-1"); found {
		t.Fatal("a released group is not placed")
	}
	// An agent that still holds a group cannot be deleted (the foreign key protects it).
	if _, err = agents.DeleteAdmin(ctx, b.ID); err == nil {
		t.Fatal("deleting an agent that holds a group must fail")
	}
}

func TestInfrastructureAgents_RecordedCapacityArchiveAndReconnect(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := infrastructureAgentRepo.New(db.Queries)
	now := time.Now().UTC().Truncate(time.Microsecond)
	a := newAdminAgent("alpha", 1, now)
	if _, err := repo.CreateAdmin(ctx, a); err != nil {
		t.Fatal(err)
	}
	if got, _ := repo.Get(ctx, a.ID); got.CapacitySeenAt != nil || got.CapacityCPUMillicores != nil {
		t.Fatalf("a never seen agent has no capacity: %+v", got.AgentRegistration)
	}
	cpu := int64(8000)
	if err := repo.SetCapacity(ctx, a.ID, &cpu, nil, now); err != nil {
		t.Fatal(err)
	}
	got, _ := repo.Get(ctx, a.ID)
	if got.CapacitySeenAt == nil || !got.CapacitySeenAt.Equal(now) || got.CapacityCPUMillicores == nil || *got.CapacityCPUMillicores != 8000 || got.CapacityMemoryBytes != nil {
		t.Fatalf("capacity = %+v (a null value with a read time means no limit)", got.AgentRegistration)
	}

	// Reconnect replaces every key and clears the retired ones, keeping record, name and priority.
	re := a
	re.CertPEM, re.KeyCiphertext, re.AccessKeyID, re.AccessPrivateKeyCiphertext, re.AccessPublicKey, re.Tenant, re.UpdatedAt = "cert2", "key2", "k-2", "access2", "pub2", "platform", now.Add(time.Minute)
	nb := now.Add(30 * 24 * time.Hour)
	re.CertNotAfter = &nb
	if _, err := repo.SetAccessKey(ctx, a.ID, "k-old", "x", "y", []infraModel.RetiredKey{{KeyID: "k-older", RetiredAt: now}}, now); err != nil {
		t.Fatal(err)
	}
	if ok, err := repo.ReplaceCredentials(ctx, re); err != nil || !ok {
		t.Fatalf("replace = %v, %v", ok, err)
	}
	if got, _ = repo.Get(ctx, a.ID); got.CertPEM != "cert2" || got.AccessKeyID != "k-2" || len(got.RetiredAccessKeys) != 0 || got.Name != "alpha" || got.Priority != 1 {
		t.Fatalf("reconnected = %+v", got)
	}

	// Archive wipes the connection, renames, hides the record and frees the endpoint.
	if ok, err := repo.Archive(ctx, a.ID, infraModel.ArchivedName("alpha", now), now.Add(time.Hour)); err != nil || !ok {
		t.Fatalf("archive = %v, %v", ok, err)
	}
	got, _ = repo.Get(ctx, a.ID)
	if got.ArchivedAt == nil || got.Endpoint != "" || got.CertPEM != "" || got.KeyCiphertext != "" || got.AccessPrivateKeyCiphertext != "" || got.Tenant != "" || got.Enabled || !strings.HasPrefix(got.Name, "alpha (archived ") || got.CertNotAfter != nil {
		t.Fatalf("archived = %+v", got)
	}
	if ok, _ := repo.Archive(ctx, a.ID, "again", now); ok {
		t.Fatal("an archived agent is archived once")
	}
	if ok, _ := repo.ReplaceCredentials(ctx, re); ok {
		t.Fatal("an archived agent cannot be reconnected")
	}
	if records, _ := repo.ListRecords(ctx); len(records) != 0 {
		t.Fatalf("archived agents are hidden: %+v", records)
	}
	if records, _ := repo.ListAllRecords(ctx); len(records) != 1 {
		t.Fatalf("archived agents are kept for history: %d", len(records))
	}
	// The same endpoint can be added again after the delete, and twice among live agents it cannot.
	if _, err := repo.CreateAdmin(ctx, newAdminAgent("alpha", 2, now)); err != nil {
		t.Fatalf("the endpoint is free after the archive: %v", err)
	}
	if _, err := repo.CreateAdmin(ctx, newAdminAgent("alpha", 3, now)); err == nil {
		t.Fatal("a live endpoint stays unique")
	}
}
