// Package agentfleet keeps the fleet of infrastructure agents in step with the registry: the
// admin-configured agents while any exist, else the single environment agent. For every agent it
// holds the connection and runs its monitoring subscription.
package agentfleet

import (
	"context"
	"crypto/ed25519"
	"errors"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/infrastructure/labagent"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/pkg/agentcrypto"
)

// Registry lists the agents with their stored connection material.
type Registry interface {
	ListRecords(ctx context.Context) ([]infraModel.AgentRecord, error)
}

// Cipher opens the stored certificate and key of an admin agent.
type Cipher interface {
	DecryptWithContext(encoded string, context []byte) ([]byte, error)
}

// Monitor follows one agent's Monitoring stream until ctx ends. envAgent is true for the agent of
// the deployment environment: its stored state keeps the agent's own id, as before agents were
// configurable.
type Monitor func(ctx context.Context, member *labagent.Member, envAgent bool) error

// Enroller performs the enrollment call of a new agent over server-authenticated TLS (the platform has
// no certificate yet). nil means the agent API of this build cannot enroll.
type Enroller func(ctx context.Context, endpoint string, caPEM []byte, token, csrPEM, accessPublicKeyPEM, accessKeyID string) (certPEM string, err error)

// KeyUpkeep is the optional part of an agent client that renews its certificate and rotates the access
// keys of its tenant over its mutual-TLS connection.
type KeyUpkeep interface {
	RenewCertificate(ctx context.Context, csrPEM string) (certPEM string, err error)
	RotateAccessKey(ctx context.Context, keyID, publicKeyPEM string) error
	RemoveAccessKey(ctx context.Context, keyID string) error
}

// ErrEnrollmentUnsupported is returned while the agent API of this build has no enrollment.
var ErrEnrollmentUnsupported = errors.New("the agent API of this build does not support enrollment")

// EnvAccess is the access signing key and tenant of the environment agent. The tenant is its client
// certificate's CN.
type EnvAccess struct {
	Tenant string
	KeyID  string
	Key    ed25519.PrivateKey
}

// Dialer builds the client of an admin agent from its connection material.
type Dialer func(c labagent.Connection, instance string) (*labagent.Client, error)

type Config struct {
	Instance string
	// Env is the client of the environment agent; nil when AGENT_* is not configured.
	Env      *labagent.Client
	Registry Registry
	// Cipher may be nil: admin agents then cannot be opened and are skipped.
	Cipher  Cipher
	Fleet   *labagent.Fleet
	Monitor Monitor
	// Dial defaults to labagent.NewFromConnection.
	Dial Dialer
	// Enroll performs the enrollment call; see Enroller.
	Enroll Enroller
	// EnvAccess is the signing key of the environment agent's lab access tokens; nil when unset.
	EnvAccess *EnvAccess
	// Interval is how often the registry is re-read; a minute by default.
	Interval time.Duration
}

type running struct {
	member    *labagent.Member
	signature string
	env       bool
	cancel    context.CancelFunc
	done      chan struct{}
}

// Manager reloads the fleet and runs one monitoring goroutine per agent.
type Manager struct {
	cfg     Config
	trigger chan struct{}

	mu      sync.Mutex
	current map[uuid.UUID]*running
	base    context.Context
}

func New(cfg Config) *Manager {
	if cfg.Dial == nil {
		cfg.Dial = labagent.NewFromConnection
	}
	if cfg.Interval <= 0 {
		cfg.Interval = time.Minute
	}
	return &Manager{cfg: cfg, trigger: make(chan struct{}, 1), current: map[uuid.UUID]*running{}}
}

// Probe pings one agent of the fleet with a short timeout. An agent that is not in the fleet (it
// could not be opened) is not connected.
func (m *Manager) Probe(ctx context.Context, id uuid.UUID) AgentProbe {
	for _, member := range m.cfg.Fleet.Members() {
		if member.ID != id {
			continue
		}
		ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		started := time.Now()
		if err := member.Client.Health(ctx); err != nil {
			return AgentProbe{Connected: true, Error: err.Error()}
		}
		return AgentProbe{Connected: true, Healthy: true, Latency: time.Since(started)}
	}
	return AgentProbe{}
}

// AgentProbe is the live state of one agent: Connected is false when the manager holds no
// connection for it (its credentials could not be opened), Healthy when it answered a ping.
type AgentProbe struct {
	Connected bool
	Healthy   bool
	Latency   time.Duration
	Error     string
}

// Fleet is the infrastructure port over the current agents.
func (m *Manager) Fleet() *labagent.Fleet { return m.cfg.Fleet }

// Refresh asks the running manager to reload at once (after the admin changed the agents).
func (m *Manager) Refresh() {
	select {
	case m.trigger <- struct{}{}:
	default:
	}
}

// Run loads the fleet and keeps it current until ctx ends, then stops every monitor.
func (m *Manager) Run(ctx context.Context) {
	m.mu.Lock()
	m.base = ctx
	m.mu.Unlock()
	if err := m.Reload(ctx); err != nil {
		log.Error().Err(err).Msg("Agent fleet: initial load failed")
	}
	ticker := time.NewTicker(m.cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case <-ticker.C:
		case <-m.trigger:
		}
		if err := m.Reload(ctx); err != nil {
			log.Error().Err(err).Msg("Agent fleet: reload failed")
		}
	}
}

func signature(r infraModel.AgentRecord) string {
	return r.Endpoint + "\x00" + r.CertPEM + "\x00" + r.KeyCiphertext + "\x00" + r.CAPEM + "\x00" + r.AccessKeyID + "\x00" + r.AccessPrivateKeyCiphertext
}

// Reload makes the fleet match the registry. An agent whose material cannot be opened is skipped
// (and logged) without affecting the others.
func (m *Manager) Reload(ctx context.Context) error {
	records, err := m.cfg.Registry.ListRecords(ctx)
	if err != nil {
		return err
	}
	var admin []infraModel.AgentRecord
	envID := uuid.Nil
	for _, r := range records {
		switch r.Source {
		case infraModel.AgentSourceAdmin:
			admin = append(admin, r)
		default:
			if r.Key == infraModel.ConfiguredPrimaryAgentKey {
				envID = r.ID
			}
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	next := map[uuid.UUID]*running{}
	switch {
	case len(admin) > 0:
		for _, r := range admin {
			next[r.ID] = m.adminMember(r)
		}
		for id, run := range next {
			if run == nil {
				delete(next, id)
			}
		}
	case m.cfg.Env != nil:
		member := &labagent.Member{ID: envID, Name: "", Priority: 100, Enabled: true, Client: m.cfg.Env}
		if access := m.cfg.EnvAccess; access != nil {
			member.Tenant, member.AccessKeyID, member.AccessKey = access.Tenant, access.KeyID, access.Key
		}
		next[envID] = &running{member: member, signature: "env", env: true}
		if old := m.current[envID]; old != nil && old.signature == "env" {
			next[envID] = old
		}
	}

	members := make([]*labagent.Member, 0, len(next))
	for id, run := range next {
		members = append(members, run.member)
		if run.cancel == nil {
			m.startMonitor(id, run)
		}
	}
	m.cfg.Fleet.Replace(members)

	for id, old := range m.current {
		if kept, ok := next[id]; ok && kept == old {
			continue
		}
		m.stop(old)
		// Only clients this manager dialed are closed; the environment client belongs to the app.
		if !old.env {
			_ = old.member.Client.Close()
		}
	}
	m.current = next
	return nil
}

// adminMember returns the running entry of an admin agent: the current one when its connection
// material is unchanged (the member is refreshed with the new name, priority and enabled flag),
// otherwise a freshly dialed one. nil when the material cannot be used.
func (m *Manager) adminMember(r infraModel.AgentRecord) *running {
	sig := signature(r)
	if old := m.current[r.ID]; old != nil && old.signature == sig {
		// The fleet's readers hold the old member: replace it instead of changing it in place.
		updated := *old.member
		updated.Name, updated.Priority, updated.Enabled = r.Name, r.Priority, r.Enabled
		old.member = &updated
		return old
	}
	if m.cfg.Cipher == nil {
		log.Error().Str("agent", r.Name).Msg("Agent fleet: the platform secrets key is not configured, the agent is skipped")
		return nil
	}
	key, keyErr := m.cfg.Cipher.DecryptWithContext(r.KeyCiphertext, infraModel.AgentSecretContext(r.ID, "key"))
	if keyErr != nil {
		log.Error().Str("agent", r.Name).Msg("Agent fleet: cannot open the agent credentials, the agent is skipped")
		return nil
	}
	client, err := m.cfg.Dial(labagent.Connection{Endpoint: r.Endpoint, CertPEM: []byte(r.CertPEM), KeyPEM: key, CAPEM: []byte(r.CAPEM)}, m.cfg.Instance)
	if err != nil {
		log.Error().Err(err).Str("agent", r.Name).Msg("Agent fleet: cannot connect to the agent, it is skipped")
		return nil
	}
	member := &labagent.Member{ID: r.ID, Name: r.Name, Priority: r.Priority, Enabled: r.Enabled, Client: client, Tenant: r.Tenant, AccessKeyID: r.AccessKeyID}
	if r.AccessPrivateKeyCiphertext != "" {
		plain, openErr := m.cfg.Cipher.DecryptWithContext(r.AccessPrivateKeyCiphertext, infraModel.AgentSecretContext(r.ID, "access"))
		if openErr == nil {
			member.AccessKey, openErr = agentcrypto.ParseAccessPrivateKey(string(plain))
		}
		if openErr != nil {
			// The agent still serves labs; only the web links need the key.
			log.Error().Str("agent", r.Name).Msg("Agent fleet: cannot open the access signing key, web links of this agent are unavailable")
			member.AccessKey = nil
		}
	}
	return &running{member: member, signature: sig}
}

func (m *Manager) startMonitor(id uuid.UUID, run *running) {
	if m.cfg.Monitor == nil || m.base == nil {
		return
	}
	ctx, cancel := context.WithCancel(m.base)
	run.cancel = cancel
	done := make(chan struct{})
	run.done = done
	// The goroutine reads copies: Reload replaces run.member while the monitor runs.
	member, env, monitor := run.member, run.env, m.cfg.Monitor
	go func() {
		defer close(done)
		if err := monitor(ctx, member, env); err != nil && ctx.Err() == nil {
			log.Error().Err(err).Str("agent_id", id.String()).Msg("Agent fleet: monitoring stopped")
		}
	}()
}

func (m *Manager) stop(run *running) {
	if run.cancel != nil {
		run.cancel()
		<-run.done
	}
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, run := range m.current {
		m.stop(run)
		if !run.env {
			_ = run.member.Client.Close()
		}
	}
	m.current = map[uuid.UUID]*running{}
}

// Enroll asks the agent at endpoint to issue a certificate for the request and to trust the access
// public key.
func (m *Manager) Enroll(ctx context.Context, endpoint string, caPEM []byte, token, csrPEM, accessPublicKeyPEM, accessKeyID string) (string, error) {
	if m.cfg.Enroll == nil {
		return "", ErrEnrollmentUnsupported
	}
	return m.cfg.Enroll(ctx, endpoint, caPEM, token, csrPEM, accessPublicKeyPEM, accessKeyID)
}

// upkeep finds the key upkeep of an agent's connection in the fleet.
func (m *Manager) upkeep(id uuid.UUID) (KeyUpkeep, error) {
	for _, member := range m.cfg.Fleet.Members() {
		if member.ID != id {
			continue
		}
		upkeep, ok := any(member.Client).(KeyUpkeep)
		if !ok {
			return nil, ErrEnrollmentUnsupported
		}
		return upkeep, nil
	}
	return nil, errors.New("the agent is not connected")
}

// RenewCertificate asks the agent for a new client certificate over its current connection.
func (m *Manager) RenewCertificate(ctx context.Context, agent uuid.UUID, csrPEM string) (string, error) {
	upkeep, err := m.upkeep(agent)
	if err != nil {
		return "", err
	}
	return upkeep.RenewCertificate(ctx, csrPEM)
}

// RotateAccessKey adds an access public key to the agent's tenant.
func (m *Manager) RotateAccessKey(ctx context.Context, agent uuid.UUID, keyID, publicKeyPEM string) error {
	upkeep, err := m.upkeep(agent)
	if err != nil {
		return err
	}
	return upkeep.RotateAccessKey(ctx, keyID, publicKeyPEM)
}

// RemoveAccessKey removes a retired access key from the agent's tenant.
func (m *Manager) RemoveAccessKey(ctx context.Context, agent uuid.UUID, keyID string) error {
	upkeep, err := m.upkeep(agent)
	if err != nil {
		return err
	}
	return upkeep.RemoveAccessKey(ctx, keyID)
}
