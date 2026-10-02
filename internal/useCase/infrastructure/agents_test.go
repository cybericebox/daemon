package infrastructure

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/cybericebox/daemon/internal/delivery/infrastructure/agentfleet"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/pkg/agentcrypto"
)

type memAgents struct {
	records        map[uuid.UUID]infraModel.AgentRecord
	order          []uuid.UUID
	capacityWrites int
}

func newMemAgents() *memAgents { return &memAgents{records: map[uuid.UUID]infraModel.AgentRecord{}} }

func (m *memAgents) ListRecords(context.Context) ([]infraModel.AgentRecord, error) {
	var out []infraModel.AgentRecord
	for _, id := range m.order {
		if m.records[id].ArchivedAt == nil {
			out = append(out, m.records[id])
		}
	}
	return out, nil
}
func (m *memAgents) ListAllRecords(ctx context.Context) ([]infraModel.AgentRecord, error) {
	return m.ListRecordsAll(), nil
}
func (m *memAgents) ListRecordsAll() []infraModel.AgentRecord {
	var out []infraModel.AgentRecord
	for _, id := range m.order {
		out = append(out, m.records[id])
	}
	return out
}
func (m *memAgents) SetCapacity(_ context.Context, id uuid.UUID, cpu, memory *int64, seen time.Time) error {
	r := m.records[id]
	r.CapacityCPUMillicores, r.CapacityMemoryBytes, r.CapacitySeenAt = cpu, memory, &seen
	m.records[id] = r
	m.capacityWrites++
	return nil
}
func (m *memAgents) SetFeatures(_ context.Context, id uuid.UUID, f infraModel.AgentFeatures, seen time.Time) error {
	r := m.records[id]
	r.Features, r.FeaturesAt = &f, &seen
	m.records[id] = r
	return nil
}
func (m *memAgents) Archive(_ context.Context, id uuid.UUID, name string, at time.Time) (bool, error) {
	r, ok := m.records[id]
	if !ok || r.ArchivedAt != nil {
		return false, nil
	}
	r.Name, r.Endpoint, r.Tenant, r.CertPEM, r.KeyCiphertext, r.AccessKeyID, r.AccessPrivateKeyCiphertext, r.AccessPublicKey, r.Enabled, r.ArchivedAt = name, "", "", "", "", "", "", "", false, &at
	r.RetiredAccessKeys = nil
	m.records[id] = r
	return true, nil
}
func (m *memAgents) ReplaceCredentials(_ context.Context, a infraModel.AgentRecord) (bool, error) {
	r, ok := m.records[a.ID]
	if !ok || r.ArchivedAt != nil {
		return false, nil
	}
	r.CertPEM, r.KeyCiphertext, r.CertNotAfter, r.Tenant, r.CAPEM, r.AccessKeyID, r.AccessPrivateKeyCiphertext, r.AccessPublicKey, r.RetiredAccessKeys = a.CertPEM, a.KeyCiphertext, a.CertNotAfter, a.Tenant, a.CAPEM, a.AccessKeyID, a.AccessPrivateKeyCiphertext, a.AccessPublicKey, nil
	m.records[a.ID] = r
	return true, nil
}
func (m *memAgents) Get(_ context.Context, id uuid.UUID) (infraModel.AgentRecord, error) {
	r, ok := m.records[id]
	if !ok {
		return infraModel.AgentRecord{}, pgx.ErrNoRows
	}
	return r, nil
}
func (m *memAgents) Create(_ context.Context, a infraModel.AgentRecord) (infraModel.AgentRecord, error) {
	for _, r := range m.records {
		if r.ArchivedAt == nil && r.Endpoint != "" && r.Endpoint == a.Endpoint {
			return infraModel.AgentRecord{}, &pgconn.PgError{Code: "23505", Detail: "Key (endpoint)=(" + a.Endpoint + ") already exists."}
		}
	}
	m.records[a.ID] = a
	m.order = append(m.order, a.ID)
	return a, nil
}
func (m *memAgents) Update(_ context.Context, a infraModel.AgentRecord) (bool, error) {
	old, ok := m.records[a.ID]
	if !ok {
		return false, nil
	}
	old.Name, old.CAPEM, old.Enabled, old.Priority, old.UpdatedAt = a.Name, a.CAPEM, a.Enabled, a.Priority, a.UpdatedAt
	m.records[a.ID] = old
	return true, nil
}
func (m *memAgents) SetCertificate(_ context.Context, id uuid.UUID, certPEM, keyCT, tenant string, notAfter, now time.Time) (bool, error) {
	r, ok := m.records[id]
	if !ok {
		return false, nil
	}
	r.CertPEM, r.KeyCiphertext, r.Tenant, r.CertNotAfter, r.UpdatedAt = certPEM, keyCT, tenant, &notAfter, now
	m.records[id] = r
	return true, nil
}
func (m *memAgents) SetAccessKey(_ context.Context, id uuid.UUID, keyID, privCT, pub string, retired []infraModel.RetiredKey, now time.Time) (bool, error) {
	r, ok := m.records[id]
	if !ok {
		return false, nil
	}
	r.AccessKeyID, r.AccessPrivateKeyCiphertext, r.AccessPublicKey, r.RetiredAccessKeys, r.UpdatedAt = keyID, privCT, pub, retired, now
	m.records[id] = r
	return true, nil
}
func (m *memAgents) SetRetiredKeys(_ context.Context, id uuid.UUID, retired []infraModel.RetiredKey, now time.Time) (bool, error) {
	r, ok := m.records[id]
	if !ok {
		return false, nil
	}
	r.RetiredAccessKeys, r.UpdatedAt = retired, now
	m.records[id] = r
	return true, nil
}
func (m *memAgents) Delete(_ context.Context, id uuid.UUID) (bool, error) {
	if _, ok := m.records[id]; !ok {
		return false, nil
	}
	delete(m.records, id)
	return true, nil
}

type fixedCounts map[uuid.UUID]int64

func (f fixedCounts) CountByAgent(context.Context) (map[uuid.UUID]int64, error) { return f, nil }

// boundSealer is a reversible stand-in cipher that binds the value to its context.
type boundSealer struct{}

func (boundSealer) EncryptWithContext(p, c []byte) (string, error) {
	return string(c) + "|" + string(p), nil
}
func (boundSealer) DecryptWithContext(e string, c []byte) ([]byte, error) {
	prefix := string(c) + "|"
	if !strings.HasPrefix(e, prefix) {
		return nil, errors.New("bound to another row")
	}
	return []byte(strings.TrimPrefix(e, prefix)), nil
}

type fakeFleet struct {
	reloads int
	healthy map[uuid.UUID]bool
}

func (f *fakeFleet) Reload(context.Context) error { f.reloads++; return nil }
func (f *fakeFleet) Probe(_ context.Context, id uuid.UUID) agentfleet.AgentProbe {
	if f.healthy[id] {
		return agentfleet.AgentProbe{Connected: true, Healthy: true}
	}
	return agentfleet.AgentProbe{}
}

// fakeRemote plays the agent: it signs a certificate with CN = tenant for the request's key.
type fakeRemote struct {
	t        *testing.T
	tenant   string
	notAfter time.Time
	// notBefore is the start of the signed certificate; zero = a minute ago.
	notBefore   time.Time
	denyToken   bool
	enrolled    struct{ endpoint, token, kid, pub string }
	renewTenant string
	rotated     []string
	removed     []string
	removeErr   error
}

func (r *fakeRemote) sign(csrPEM, tenant string) string {
	r.t.Helper()
	block, _ := pem.Decode([]byte(csrPEM))
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil || csr.CheckSignature() != nil {
		r.t.Fatalf("invalid request: %v", err)
	}
	_, caKey, _ := ed25519.GenerateKey(rand.Reader)
	ca := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "agent-ca"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: r.notAfter.Add(time.Hour), IsCA: true, BasicConstraintsValid: true}
	notBefore := r.notBefore
	if notBefore.IsZero() {
		notBefore = time.Now().Add(-time.Minute)
	}
	tpl := &x509.Certificate{SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: tenant}, NotBefore: notBefore, NotAfter: r.notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tpl, ca, csr.PublicKey, caKey)
	if err != nil {
		r.t.Fatal(err)
	}
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}))
}

func (r *fakeRemote) Enroll(_ context.Context, endpoint string, _ []byte, token, csr, pub, kid string) (string, error) {
	if r.denyToken {
		return "", ErrEnrollmentDenied
	}
	r.enrolled.endpoint, r.enrolled.token, r.enrolled.kid, r.enrolled.pub = endpoint, token, kid, pub
	return r.sign(csr, r.tenant), nil
}
func (r *fakeRemote) RenewCertificate(_ context.Context, _ uuid.UUID, csr string) (string, error) {
	tenant := r.renewTenant
	if tenant == "" {
		tenant = r.tenant
	}
	return r.sign(csr, tenant), nil
}
func (r *fakeRemote) RotateAccessKey(_ context.Context, _ uuid.UUID, keyID, _ string) error {
	r.rotated = append(r.rotated, keyID)
	return nil
}
func (r *fakeRemote) RemoveAccessKey(_ context.Context, _ uuid.UUID, keyID string) error {
	r.removed = append(r.removed, keyID)
	return r.removeErr
}

type agentsFixture struct {
	uc     *AgentsUseCase
	store  *memAgents
	fleet  *fakeFleet
	remote *fakeRemote
	counts fixedCounts
	now    time.Time
}

func newAgentsFixture(t *testing.T, sealer AgentSealer) *agentsFixture {
	t.Helper()
	f := &agentsFixture{store: newMemAgents(), fleet: &fakeFleet{healthy: map[uuid.UUID]bool{}}, counts: fixedCounts{}, now: fixedNow}
	f.remote = &fakeRemote{t: t, tenant: "platform", notAfter: f.now.Add(90 * 24 * time.Hour)}
	f.uc = NewAgentsUseCase(AgentsDependencies{Store: f.store, Placements: f.counts, Sealer: sealer, Fleet: f.fleet, Remote: f.remote, Now: func() time.Time { return f.now }})
	return f
}

func enrollForm(name string, priority int) infraModel.AgentEnrollment {
	return infraModel.AgentEnrollment{Name: name, Endpoint: name + ".example.com:443", Token: "one-time-token", Enabled: true, Priority: priority}
}

func TestEnrollAgentGeneratesKeysTakesTheTenantFromTheCertificateAndSealsThePrivateKeys(t *testing.T) {
	f := newAgentsFixture(t, boundSealer{})
	view, err := f.uc.EnrollAgent(context.Background(), enrollForm("eu", 10))
	if err != nil {
		t.Fatal(err)
	}
	stored := f.store.records[view.ID]
	if stored.Source != infraModel.AgentSourceAdmin || stored.Tenant != "platform" || stored.AccessKeyID != f.remote.enrolled.kid || stored.AccessKeyID == "" {
		t.Fatalf("stored = %+v", stored.AgentRegistration)
	}
	if f.remote.enrolled.token != "one-time-token" || f.remote.enrolled.endpoint != "eu.example.com:443" || stored.AccessPublicKey != f.remote.enrolled.pub {
		t.Fatalf("remote saw %+v", f.remote.enrolled)
	}
	// The private keys are sealed and bound to the agent; the certificate is public.
	if !strings.HasPrefix(stored.KeyCiphertext, "infra.agent:"+view.ID.String()+":key|") || !strings.HasPrefix(stored.AccessPrivateKeyCiphertext, "infra.agent:"+view.ID.String()+":access|") {
		t.Fatalf("ciphertexts not bound: %q %q", stored.KeyCiphertext, stored.AccessPrivateKeyCiphertext)
	}
	plain, _ := boundSealer{}.DecryptWithContext(stored.KeyCiphertext, infraModel.AgentSecretContext(view.ID, "key"))
	if err = agentcrypto.MatchesKey(stored.CertPEM, string(plain)); err != nil {
		t.Fatalf("the stored certificate must belong to the stored key: %v", err)
	}
	if stored.CertNotAfter == nil || !stored.CertNotAfter.Equal(f.remote.notAfter.UTC().Truncate(time.Second)) {
		t.Fatalf("not after = %v", stored.CertNotAfter)
	}
	if strings.Contains(stored.CertPEM+stored.KeyCiphertext+stored.AccessPrivateKeyCiphertext, "one-time-token") {
		t.Fatal("the enrollment token must never be stored")
	}
	if f.fleet.reloads != 1 {
		t.Fatalf("reloads = %d", f.fleet.reloads)
	}
}

func TestEnrollAgentFailures(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	f.remote.denyToken = true
	if _, err := f.uc.EnrollAgent(ctx, enrollForm("a", 1)); !errors.Is(err, infraModel.ErrAgentEnrollmentRejected.Err()) {
		t.Fatalf("denied token = %v", err)
	}
	if len(f.store.records) != 0 {
		t.Fatal("nothing is stored for a rejected enrollment")
	}
	f.remote.denyToken = false
	if _, err := f.uc.EnrollAgent(ctx, enrollForm("a", 1)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.uc.EnrollAgent(ctx, enrollForm("a", 2)); !errors.Is(err, infraModel.ErrAgentExists.Err()) {
		t.Fatalf("the same endpoint twice = %v", err)
	}
	bad := enrollForm("b", 1)
	bad.Token = ""
	if _, err := f.uc.EnrollAgent(ctx, bad); !errors.Is(err, infraModel.ErrAgentInvalid.Err()) {
		t.Fatalf("no token = %v", err)
	}
	none := newAgentsFixture(t, nil)
	if _, err := none.uc.EnrollAgent(ctx, enrollForm("c", 1)); !errors.Is(err, infraModel.ErrAgentSecretsUnavailable.Err()) {
		t.Fatalf("without the secrets key = %v", err)
	}
}

func TestRenewCertificateKeepsTheTenantAndRefusesAnotherOne(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	view, err := f.uc.EnrollAgent(ctx, enrollForm("eu", 1))
	if err != nil {
		t.Fatal(err)
	}
	before := f.store.records[view.ID]
	f.remote.notAfter = f.now.Add(200 * 24 * time.Hour)
	if _, err = f.uc.RenewAgentCertificate(ctx, view.ID); err != nil {
		t.Fatal(err)
	}
	after := f.store.records[view.ID]
	if after.CertPEM == before.CertPEM || after.KeyCiphertext == before.KeyCiphertext || after.Tenant != "platform" || after.CertNotAfter.Before(*before.CertNotAfter) {
		t.Fatalf("renewed = %+v", after.AgentRegistration)
	}
	f.remote.renewTenant = "other"
	if _, err = f.uc.RenewAgentCertificate(ctx, view.ID); !errors.Is(err, infraModel.ErrAgentTenantChanged.Err()) {
		t.Fatalf("a changed CN = %v", err)
	}
	if f.store.records[view.ID].CertPEM != after.CertPEM {
		t.Fatal("a refused certificate must not replace the stored one")
	}
}

func TestRotateAccessKeyRetiresTheOldKeyAndMaintenanceRemovesItLater(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	view, err := f.uc.EnrollAgent(ctx, enrollForm("eu", 1))
	if err != nil {
		t.Fatal(err)
	}
	oldKey := f.store.records[view.ID].AccessKeyID
	if _, err = f.uc.RotateAgentAccessKey(ctx, view.ID); err != nil {
		t.Fatal(err)
	}
	rotated := f.store.records[view.ID]
	if rotated.AccessKeyID == oldKey || len(f.remote.rotated) != 1 || f.remote.rotated[0] != rotated.AccessKeyID {
		t.Fatalf("rotated = %+v remote %v", rotated.AccessKeyID, f.remote.rotated)
	}
	if len(rotated.RetiredAccessKeys) != 1 || rotated.RetiredAccessKeys[0].KeyID != oldKey {
		t.Fatalf("retired = %+v", rotated.RetiredAccessKeys)
	}

	// Too early: the old key still verifies tokens in flight.
	if err = f.uc.MaintainAgents(ctx); err != nil || len(f.remote.removed) != 0 {
		t.Fatalf("maintain too early: %v removed %v", err, f.remote.removed)
	}
	f.now = f.now.Add(MinAccessKeyRetention + time.Minute)
	if err = f.uc.MaintainAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if len(f.remote.removed) != 1 || f.remote.removed[0] != oldKey || len(f.store.records[view.ID].RetiredAccessKeys) != 0 {
		t.Fatalf("removed %v retired %+v", f.remote.removed, f.store.records[view.ID].RetiredAccessKeys)
	}
	// A failing removal keeps the key for the next pass.
	if _, err = f.uc.RotateAgentAccessKey(ctx, view.ID); err != nil {
		t.Fatal(err)
	}
	f.now = f.now.Add(MinAccessKeyRetention + time.Minute)
	f.remote.removeErr = errors.New("agent down")
	if err = f.uc.MaintainAgents(ctx); err == nil {
		t.Fatal("a failed removal is reported")
	}
	if len(f.store.records[view.ID].RetiredAccessKeys) != 1 {
		t.Fatalf("the key must wait: %+v", f.store.records[view.ID].RetiredAccessKeys)
	}
}

func TestMaintenanceRenewsCertificatesThatEndSoon(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	// 25 of its 30 days are gone: past two thirds of its lifetime from the start.
	f.remote.notBefore, f.remote.notAfter = f.now.Add(-25*24*time.Hour), f.now.Add(5*24*time.Hour)
	view, err := f.uc.EnrollAgent(ctx, enrollForm("eu", 1))
	if err != nil {
		t.Fatal(err)
	}
	f.remote.notBefore, f.remote.notAfter = time.Time{}, f.now.Add(120*24*time.Hour)
	reloads := f.fleet.reloads
	if err = f.uc.MaintainAgents(ctx); err != nil {
		t.Fatal(err)
	}
	if got := f.store.records[view.ID].CertNotAfter; got == nil || got.Before(f.now.Add(100*24*time.Hour)) {
		t.Fatalf("not after = %v, want renewed", got)
	}
	if f.fleet.reloads != reloads+1 {
		t.Fatal("the fleet must reconnect with the new certificate")
	}
	// A healthy certificate is left alone.
	before := f.store.records[view.ID].CertPEM
	if err = f.uc.MaintainAgents(ctx); err != nil || f.store.records[view.ID].CertPEM != before {
		t.Fatalf("a valid certificate was renewed: %v", err)
	}
}

func TestUpdateAgentChangesOnlyLabelOrderSwitchAndCA(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	view, err := f.uc.EnrollAgent(ctx, enrollForm("eu", 10))
	if err != nil {
		t.Fatal(err)
	}
	before := f.store.records[view.ID]
	if _, err = f.uc.UpdateAgent(ctx, view.ID, infraModel.AgentUpdate{Name: "eu-renamed", Priority: 3, CAPEM: "ca"}); err != nil {
		t.Fatal(err)
	}
	after := f.store.records[view.ID]
	if after.Name != "eu-renamed" || after.Priority != 3 || after.Enabled || after.CAPEM != "ca" {
		t.Fatalf("after = %+v", after.AgentRegistration)
	}
	if after.Endpoint != before.Endpoint || after.CertPEM != before.CertPEM || after.KeyCiphertext != before.KeyCiphertext || after.AccessKeyID != before.AccessKeyID {
		t.Fatal("the endpoint, certificate and keys must not change")
	}
	if _, err = f.uc.UpdateAgent(ctx, uuid.Must(uuid.NewV7()), infraModel.AgentUpdate{Name: "x"}); !errors.Is(err, infraModel.ErrAgentNotFound.Err()) {
		t.Fatalf("unknown agent: %v", err)
	}
}

func TestListAgentsOrdersByPriorityAndShowsHowEachWasAdded(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	if _, err := f.uc.EnrollAgent(ctx, enrollForm("b", 50)); err != nil {
		t.Fatal(err)
	}
	bootstrapped, err := f.uc.BootstrapAgent(ctx, enrollForm("a", 10))
	if err != nil || !bootstrapped {
		t.Fatalf("bootstrap = %v, %v", bootstrapped, err)
	}
	var first, second uuid.UUID
	for id, r := range f.store.records {
		if r.Name == "a" {
			first = id
		} else {
			second = id
		}
	}
	f.fleet.healthy[first] = true
	f.counts[first] = 4
	view, err := f.uc.ListAgents(ctx, false)
	if err != nil || len(view.Items) != 2 {
		t.Fatalf("list = %+v, %v", view, err)
	}
	if view.Items[0].ID != first || view.Items[0].Source != infraModel.AgentSourceEnv || !view.Items[0].InUse || !view.Items[0].Probe.Healthy || view.Items[0].Groups != 4 || view.Items[0].Tenant != "platform" {
		t.Fatalf("first = %+v", view.Items[0])
	}
	if view.Items[1].ID != second || view.Items[1].Source != infraModel.AgentSourceAdmin {
		t.Fatalf("second = %+v", view.Items[1])
	}
}

func TestBootstrapAgentEnrollsOnceAndThenIgnoresTheToken(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	in := enrollForm("default", 100)
	if enrolled, err := f.uc.BootstrapAgent(ctx, infraModel.AgentEnrollment{}); err != nil || enrolled {
		t.Fatalf("no endpoint is nothing to do: %v %v", enrolled, err)
	}
	enrolled, err := f.uc.BootstrapAgent(ctx, in)
	if err != nil || !enrolled || len(f.store.records) != 1 || f.fleet.reloads != 1 {
		t.Fatalf("first start: %v %v records=%d reloads=%d", enrolled, err, len(f.store.records), f.fleet.reloads)
	}
	var stored infraModel.AgentRecord
	for _, r := range f.store.records {
		stored = r
	}
	if stored.Source != infraModel.AgentSourceEnv || stored.Tenant != "platform" || stored.KeyCiphertext == "" || stored.AccessPrivateKeyCiphertext == "" || stored.Endpoint != in.Endpoint {
		t.Fatalf("stored = %+v", stored.AgentRegistration)
	}
	// The next start: the agent exists, the (spent) token is not used again.
	f.remote.denyToken = true
	enrolled, err = f.uc.BootstrapAgent(ctx, in)
	if err != nil || enrolled || len(f.store.records) != 1 {
		t.Fatalf("second start: %v %v records=%d", enrolled, err, len(f.store.records))
	}
	// A deleted (archived) agent is gone: the endpoint is enrolled again when the config still names it.
	f.remote.denyToken = false
	if err = f.uc.DeleteAgent(ctx, stored.ID, false); err != nil {
		t.Fatal(err)
	}
	if enrolled, err = f.uc.BootstrapAgent(ctx, in); err != nil || !enrolled || len(f.store.records) != 2 {
		t.Fatalf("after a delete: %v %v records=%d", enrolled, err, len(f.store.records))
	}
}

func TestBootstrapAgentFailureIsReportedNotFatal(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	f.remote.denyToken = true
	if enrolled, err := f.uc.BootstrapAgent(ctx, enrollForm("default", 100)); enrolled || !errors.Is(err, infraModel.ErrAgentEnrollmentRejected.Err()) || len(f.store.records) != 0 {
		t.Fatalf("a used token: %v %v", enrolled, err)
	}
	noToken := enrollForm("default", 100)
	noToken.Token = ""
	if _, err := f.uc.BootstrapAgent(ctx, noToken); !errors.Is(err, infraModel.ErrAgentInvalid.Err()) {
		t.Fatalf("no token: %v", err)
	}
}

type fakeImpacts struct{ items []ReservationImpact }

func (f fakeImpacts) FutureImpact(context.Context, uuid.UUID, time.Time) ([]ReservationImpact, error) {
	return f.items, nil
}

func TestDeleteAgentIsBlockedByRunningGroupsAndConfirmedForFutureReservations(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	created, err := f.uc.EnrollAgent(ctx, enrollForm("a", 1))
	if err != nil {
		t.Fatal(err)
	}
	f.counts[created.ID] = 2
	if err = f.uc.DeleteAgent(ctx, created.ID, true); !errors.Is(err, infraModel.ErrAgentInUse.Err()) {
		t.Fatalf("delete of an agent with running groups = %v, even confirmed", err)
	}
	f.counts[created.ID] = 0

	f.uc.impacts = fakeImpacts{items: []ReservationImpact{{EventName: "CTF", CPUMillicores: 4000}}}
	preview, err := f.uc.PreviewAgentDelete(ctx, created.ID)
	if err != nil || preview.RunningGroups != 0 || len(preview.FutureReservations) != 1 {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	if err = f.uc.DeleteAgent(ctx, created.ID, false); !errors.Is(err, infraModel.ErrAgentDeleteNeedsConfirm.Err()) {
		t.Fatalf("future reservations need a confirmation: %v", err)
	}
	reloads := f.fleet.reloads
	if err = f.uc.DeleteAgent(ctx, created.ID, true); err != nil || f.fleet.reloads != reloads+1 {
		t.Fatalf("confirmed delete = %v, reloads %d -> %d", err, reloads, f.fleet.reloads)
	}
	archived := f.store.records[created.ID]
	if archived.ArchivedAt == nil || !strings.HasPrefix(archived.Name, "a (archived ") || archived.Endpoint != "" || archived.CertPEM != "" || archived.KeyCiphertext != "" || archived.AccessPrivateKeyCiphertext != "" || archived.Enabled {
		t.Fatalf("archived = %+v", archived)
	}
	// The name is free again, the archived record is hidden by default and gone for every action.
	if _, err = f.uc.EnrollAgent(ctx, enrollForm("a", 1)); err != nil {
		t.Fatalf("the same endpoint can be enrolled again: %v", err)
	}
	if view, _ := f.uc.ListAgents(ctx, false); len(view.Items) != 1 {
		t.Fatalf("archived agents are hidden: %d", len(view.Items))
	}
	if view, _ := f.uc.ListAgents(ctx, true); len(view.Items) != 2 || view.Items[0].InUse && view.Items[1].InUse {
		t.Fatalf("archived listed on request and not in use: %+v", view.Items)
	}
	if err = f.uc.DeleteAgent(ctx, created.ID, true); !errors.Is(err, infraModel.ErrAgentNotFound.Err()) {
		t.Fatalf("deleting twice = %v", err)
	}
	if _, err = f.uc.RenewAgentCertificate(ctx, created.ID); !errors.Is(err, infraModel.ErrAgentNotFound.Err()) {
		t.Fatalf("an archived agent cannot be used = %v", err)
	}
}

func TestReconnectEnrollsTheSameClusterAgainForNewKeys(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	view, err := f.uc.EnrollAgent(ctx, enrollForm("eu", 7))
	if err != nil {
		t.Fatal(err)
	}
	before := f.store.records[view.ID]
	f.counts[view.ID] = 3
	if _, err = f.uc.ReconnectAgent(ctx, view.ID, "new-token", ""); err != nil {
		t.Fatal(err)
	}
	after := f.store.records[view.ID]
	if after.ID != before.ID || after.Priority != 7 || after.Endpoint != before.Endpoint || after.Name != before.Name {
		t.Fatalf("the record must stay: %+v", after.AgentRegistration)
	}
	if after.CertPEM == before.CertPEM || after.KeyCiphertext == before.KeyCiphertext || after.AccessKeyID == before.AccessKeyID || after.AccessPublicKey == before.AccessPublicKey {
		t.Fatal("every key must be new")
	}
	if f.remote.enrolled.token != "new-token" || f.remote.enrolled.endpoint != before.Endpoint {
		t.Fatalf("remote saw %+v", f.remote.enrolled)
	}
	// Another tenant means another cluster: that is a move, not a reconnect.
	f.remote.tenant = "other"
	if _, err = f.uc.ReconnectAgent(ctx, view.ID, "t", ""); !errors.Is(err, infraModel.ErrAgentTenantChanged.Err()) {
		t.Fatalf("another tenant = %v", err)
	}
	if f.store.records[view.ID].CertPEM != after.CertPEM {
		t.Fatal("a refused reconnect must not change the keys")
	}
	f.remote.tenant, f.remote.denyToken = "platform", true
	if _, err = f.uc.ReconnectAgent(ctx, view.ID, "t", ""); !errors.Is(err, infraModel.ErrAgentEnrollmentRejected.Err()) {
		t.Fatalf("a used token = %v", err)
	}
	if _, err = f.uc.ReconnectAgent(ctx, view.ID, " ", ""); !errors.Is(err, infraModel.ErrAgentInvalid.Err()) {
		t.Fatalf("no token = %v", err)
	}
}

func TestRecordAgentCapacityWritesOnChangeAndWhenStale(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	view, _ := f.uc.EnrollAgent(ctx, enrollForm("eu", 1))
	cpu, memory := int64(8000), int64(1<<30)
	if err := f.uc.RecordAgentCapacity(ctx, view.ID, &cpu, &memory, f.now); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.RecordAgentCapacity(ctx, view.ID, &cpu, &memory, f.now.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if f.store.capacityWrites != 1 {
		t.Fatalf("an unchanged capacity is not written again: %d writes", f.store.capacityWrites)
	}
	bigger := int64(16000)
	if err := f.uc.RecordAgentCapacity(ctx, view.ID, &bigger, &memory, f.now.Add(2*time.Minute)); err != nil || f.store.capacityWrites != 2 {
		t.Fatalf("a changed capacity is written: %v %d", err, f.store.capacityWrites)
	}
	if err := f.uc.RecordAgentCapacity(ctx, view.ID, &bigger, &memory, f.now.Add(10*time.Minute)); err != nil || f.store.capacityWrites != 3 {
		t.Fatalf("a stale reading is refreshed: %v %d", err, f.store.capacityWrites)
	}
	// No quota = no limit: nil values with a read time.
	if err := f.uc.RecordAgentCapacity(ctx, view.ID, nil, nil, f.now.Add(11*time.Minute)); err != nil {
		t.Fatal(err)
	}
	got := f.store.records[view.ID]
	if got.CapacityCPUMillicores != nil || got.CapacityMemoryBytes != nil || got.CapacitySeenAt == nil {
		t.Fatalf("unlimited = %+v", got.AgentRegistration)
	}
}

func TestRenewalIsDueAtTwoThirdsOfTheCertificateLifetime(t *testing.T) {
	f := newAgentsFixture(t, boundSealer{})
	key, err := agentcrypto.NewClientKey()
	if err != nil {
		t.Fatal(err)
	}
	csr := key.CSRPEM
	issue := func(notBefore, notAfter time.Time) string {
		f.remote.notBefore, f.remote.notAfter = notBefore, notAfter
		return f.remote.sign(csr, "platform")
	}
	start := f.now
	record := infraModel.AgentRecord{CertPEM: issue(start, start.Add(30*24*time.Hour))}
	for at, want := range map[time.Duration]bool{
		19 * 24 * time.Hour: false, 20*24*time.Hour - time.Hour: false, 20 * 24 * time.Hour: true, 29 * 24 * time.Hour: true,
	} {
		if got := renewDue(record, start.Add(at)); got != want {
			t.Errorf("after %v: due = %v, want %v", at, got, want)
		}
	}
	// A shorter lifetime moves the point with it.
	short := infraModel.AgentRecord{CertPEM: issue(start, start.Add(3*time.Hour))}
	if renewDue(short, start.Add(time.Hour)) || !renewDue(short, start.Add(2*time.Hour)) {
		t.Error("the renewal point follows the certificate's own lifetime")
	}
	// Without a readable certificate the agent's reported lifetime stands in; without that nothing is guessed.
	notAfter := start.Add(30 * 24 * time.Hour)
	lost := infraModel.AgentRecord{CertPEM: "garbage"}
	lost.CertNotAfter = &notAfter
	if renewDue(lost, start.Add(29*24*time.Hour)) {
		t.Error("no lifetime known: never guess a renewal")
	}
	lost.Features = &infraModel.AgentFeatures{Certificate: infraModel.CertificateFeature{IssuedTTLSeconds: 30 * 24 * 3600}}
	if renewDue(lost, start.Add(19*24*time.Hour)) || !renewDue(lost, start.Add(21*24*time.Hour)) {
		t.Error("the reported lifetime sets the point")
	}
}

func TestAccessKeyRetentionFollowsTheProxyTokenLimit(t *testing.T) {
	uc := &AgentsUseCase{}
	if uc.accessKeyRetention() != MinAccessKeyRetention {
		t.Fatalf("no limit known: %v", uc.accessKeyRetention())
	}
	uc.tokenMaxTTL = 5 * time.Minute
	if uc.accessKeyRetention() != 15*time.Minute {
		t.Fatalf("5m tokens: %v", uc.accessKeyRetention())
	}
	uc.tokenMaxTTL = 20 * time.Minute
	if uc.accessKeyRetention() != time.Hour {
		t.Fatalf("20m tokens keep the key an hour: %v", uc.accessKeyRetention())
	}
}

func TestRecordAgentFeaturesKeepsTheLastReport(t *testing.T) {
	ctx := context.Background()
	f := newAgentsFixture(t, boundSealer{})
	view, err := f.uc.EnrollAgent(ctx, enrollForm("eu", 1))
	if err != nil {
		t.Fatal(err)
	}
	features := infraModel.AgentFeatures{Persistence: infraModel.PersistenceFeature{Available: true}}
	if err = f.uc.RecordAgentFeatures(ctx, view.ID, features, f.now); err != nil {
		t.Fatal(err)
	}
	stored := f.store.records[view.ID]
	if stored.Features == nil || !stored.Features.Persistence.Available || stored.FeaturesAt == nil || !stored.FeaturesAt.Equal(f.now) {
		t.Fatalf("stored = %+v at %v", stored.Features, stored.FeaturesAt)
	}
}
