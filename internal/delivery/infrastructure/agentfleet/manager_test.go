package agentfleet

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/infrastructure/labagent"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

type memRegistry struct{ records []infraModel.AgentRecord }

func (r *memRegistry) ListRecords(context.Context) ([]infraModel.AgentRecord, error) {
	return append([]infraModel.AgentRecord(nil), r.records...), nil
}

// plainCipher "decrypts" by stripping a prefix and checks the context binds the agent.
type plainCipher struct{ wrongContext bool }

func (c plainCipher) DecryptWithContext(encoded string, ctx []byte) ([]byte, error) {
	if c.wrongContext {
		return nil, errors.New("bad context")
	}
	return []byte(encoded), nil
}

type monitorLog struct {
	mu      sync.Mutex
	started []string
	stopped []string
}

func (l *monitorLog) monitor(ctx context.Context, m *labagent.Member) error {
	l.mu.Lock()
	tag := m.Name
	l.started = append(l.started, tag)
	l.mu.Unlock()
	<-ctx.Done()
	l.mu.Lock()
	l.stopped = append(l.stopped, tag)
	l.mu.Unlock()
	return nil
}

func admin(name string, priority int, enabled bool) infraModel.AgentRecord {
	id := uuid.Must(uuid.NewV7())
	return infraModel.AgentRecord{
		AgentRegistration: infraModel.AgentRegistration{ID: id, Key: id.String(), Name: name, Source: infraModel.AgentSourceAdmin, Endpoint: name + ".test:443", Enabled: enabled, Priority: priority},
		CertPEM:           "cert", KeyCiphertext: "key",
	}
}

func newManager(reg *memRegistry, log *monitorLog, cipher Cipher) (*Manager, *int) {
	dials := 0
	m := New(Config{
		Instance: "prod", Registry: reg, Cipher: cipher, Fleet: labagent.NewFleet(nil, nil), Monitor: log.monitor,
		Dial: func(labagent.Connection, string) (*labagent.Client, error) {
			dials++
			return &labagent.Client{Client: closedClient{}}, nil
		},
	})
	m.base = context.Background()
	return m, &dials
}

func names(f *labagent.Fleet) []string {
	var out []string
	for _, m := range f.Members() {
		out = append(out, m.Name)
	}
	return out
}

func TestReloadKeepsUnchangedAgentsAppliesFlagsAndDropsRemovedOnes(t *testing.T) {
	a, b := admin("a", 20, true), admin("b", 10, true)
	reg := &memRegistry{records: []infraModel.AgentRecord{a, b}}
	log := &monitorLog{}
	m, dials := newManager(reg, log, plainCipher{})
	ctx := context.Background()
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if got := names(m.Fleet()); len(got) != 2 || got[0] != "b" || got[1] != "a" || *dials != 2 {
		t.Fatalf("members = %v dials = %d, want b before a by priority", got, *dials)
	}

	// Name, priority and the enabled flag change without redialing or restarting the monitor.
	reg.records[0].Priority, reg.records[0].Enabled = 1, false
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	members := m.Fleet().Members()
	if members[0].Name != "a" || members[0].Enabled || *dials != 2 {
		t.Fatalf("members = %+v dials = %d", members, *dials)
	}

	// New material is dialed again; a removed agent stops.
	reg.records[0].CertPEM = "renewed"
	reg.records = reg.records[:1]
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if got := names(m.Fleet()); len(got) != 1 || got[0] != "a" || *dials != 3 {
		t.Fatalf("members = %v dials = %d", got, *dials)
	}
	m.stopAll()
	log.mu.Lock()
	defer log.mu.Unlock()
	if len(log.stopped) != len(log.started) {
		t.Fatalf("every monitor must stop: started %v stopped %v", log.started, log.stopped)
	}
}

func TestAnAgentWhoseCredentialsCannotBeOpenedIsSkipped(t *testing.T) {
	reg := &memRegistry{records: []infraModel.AgentRecord{admin("a", 1, true), admin("b", 2, true)}}
	m, _ := newManager(reg, &monitorLog{}, plainCipher{wrongContext: true})
	if err := m.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := m.Fleet().Members(); len(got) != 0 {
		t.Fatalf("members = %v, want none: nothing can be opened", names(m.Fleet()))
	}
	m2, _ := newManager(reg, &monitorLog{}, nil)
	if err := m2.Reload(context.Background()); err != nil || len(m2.Fleet().Members()) != 0 {
		t.Fatalf("no cipher: members = %v, err = %v", names(m2.Fleet()), err)
	}
}

func TestManagerUpkeepGoesToTheAgentsOwnConnection(t *testing.T) {
	a := admin("a", 1, true)
	reg := &memRegistry{records: []infraModel.AgentRecord{a}}
	var renewed, rotated, removed []string
	m := New(Config{
		Instance: "prod", Registry: reg, Cipher: plainCipher{}, Fleet: labagent.NewFleet(nil, nil),
		Dial: func(labagent.Connection, string) (*labagent.Client, error) {
			return &labagent.Client{Client: closedClient{renewed: &renewed, rotated: &rotated, removed: &removed}}, nil
		},
	})
	ctx := context.Background()
	if err := m.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if cert, err := m.RenewCertificate(ctx, a.ID, "CSR"); err != nil || cert != "NEWCERT" || len(renewed) != 1 || renewed[0] != "CSR" {
		t.Fatalf("renew = %q %v %v", cert, err, renewed)
	}
	if err := m.RotateAccessKey(ctx, a.ID, "k-2", "PUB"); err != nil || len(rotated) != 1 || rotated[0] != "k-2" {
		t.Fatalf("rotate = %v %v", err, rotated)
	}
	if err := m.RemoveAccessKey(ctx, a.ID, "k-1"); err != nil || len(removed) != 1 || removed[0] != "k-1" {
		t.Fatalf("remove = %v %v", err, removed)
	}
	unknown := uuid.Must(uuid.NewV7())
	if _, err := m.RenewCertificate(ctx, unknown, "CSR"); err == nil {
		t.Fatal("an agent that is not connected cannot renew")
	}
	// Enrollment is the platform's call to an agent it has no connection to yet.
	if _, err := m.Enroll(ctx, "a:443", nil, "t", "csr", "pub", "k"); !errors.Is(err, ErrEnrollmentUnsupported) {
		t.Fatalf("without an enroller = %v", err)
	}
	m.cfg.Enroll = func(_ context.Context, endpoint string, _ []byte, token, csr, pub, kid string) (string, error) {
		return endpoint + "|" + token + "|" + csr + "|" + pub + "|" + kid, nil
	}
	if got, err := m.Enroll(ctx, "a:443", nil, "t", "csr", "pub", "k"); err != nil || got != "a:443|t|csr|pub|k" {
		t.Fatalf("enroll = %q %v", got, err)
	}
}
