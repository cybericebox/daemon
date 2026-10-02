package infrastructure

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/infrastructure/agentfleet"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	"github.com/cybericebox/daemon/pkg/agentcrypto"
)

// MinAccessKeyRetention is the least a rotated-out access key stays at the agent. The real retention is
// three times the longest token the proxy accepts (the agent reports it), never less than this: only then
// is no token signed with the old key still alive.
const MinAccessKeyRetention = 15 * time.Minute

// certRenewAt is the fraction of its lifetime after which a client certificate is renewed.
const certRenewAtNumerator, certRenewAtDenominator = 2, 3

// ErrEnrollmentDenied is returned by the agent adapter when the agent refuses the enrollment token or
// the request.
var ErrEnrollmentDenied = infraModel.ErrEnrollmentDenied

type (
	// AgentStore is the agent registry the admin edits.
	AgentStore interface {
		ListRecords(ctx context.Context) ([]infraModel.AgentRecord, error)
		ListAllRecords(ctx context.Context) ([]infraModel.AgentRecord, error)
		Get(ctx context.Context, id uuid.UUID) (infraModel.AgentRecord, error)
		Create(ctx context.Context, a infraModel.AgentRecord) (infraModel.AgentRecord, error)
		Update(ctx context.Context, a infraModel.AgentRecord) (bool, error)
		SetCertificate(ctx context.Context, id uuid.UUID, certPEM, keyCiphertext, tenant string, notAfter, now time.Time) (bool, error)
		SetAccessKey(ctx context.Context, id uuid.UUID, keyID, privateCiphertext, publicPEM string, retired []infraModel.RetiredKey, now time.Time) (bool, error)
		SetRetiredKeys(ctx context.Context, id uuid.UUID, retired []infraModel.RetiredKey, now time.Time) (bool, error)
		Delete(ctx context.Context, id uuid.UUID) (bool, error)
		SetCapacity(ctx context.Context, id uuid.UUID, cpuMillicores, memoryBytes *int64, seenAt time.Time) error
		SetFeatures(ctx context.Context, id uuid.UUID, features infraModel.AgentFeatures, seenAt time.Time) error
		Archive(ctx context.Context, id uuid.UUID, name string, at time.Time) (bool, error)
		ReplaceCredentials(ctx context.Context, a infraModel.AgentRecord) (bool, error)
	}

	// AgentImpacts says which future reservations lose capacity when an agent goes (the calendar).
	AgentImpacts interface {
		FutureImpact(ctx context.Context, agent uuid.UUID, now time.Time) ([]ReservationImpact, error)
	}

	// ReservationImpact is one future reservation that an agent's deletion would leave not covered.
	ReservationImpact struct {
		ReservationID uuid.UUID
		EventID       uuid.UUID
		EventName     string
		CPUMillicores int64
		MemoryBytes   int64
		StartsAt      time.Time
		EndsAt        time.Time
	}

	// DeletePreview is what deleting an agent would do: RunningGroups block it, FutureReservations need a
	// confirmation.
	DeletePreview struct {
		RunningGroups      int64
		FutureReservations []ReservationImpact
	}

	// AgentPlacements counts the lab groups each agent holds.
	AgentPlacements interface {
		CountByAgent(ctx context.Context) (map[uuid.UUID]int64, error)
	}

	// AgentSealer encrypts and opens the private keys with the platform secrets key.
	AgentSealer interface {
		EncryptWithContext(plaintext, context []byte) (string, error)
		DecryptWithContext(encoded string, context []byte) ([]byte, error)
	}

	// AgentFleet applies the registry to the live fleet and probes the agents.
	AgentFleet interface {
		Reload(ctx context.Context) error
		Probe(ctx context.Context, id uuid.UUID) agentfleet.AgentProbe
	}

	// AgentRemote is what the platform asks of an agent for enrollment and key upkeep. Enroll works over
	// server-authenticated TLS (the platform has no certificate yet); the others use the agent's
	// mutual-TLS connection from the fleet. A refused token is ErrEnrollmentDenied.
	AgentRemote interface {
		Enroll(ctx context.Context, endpoint string, caPEM []byte, token, csrPEM, accessPublicKeyPEM, accessKeyID string) (certPEM string, err error)
		RenewCertificate(ctx context.Context, agent uuid.UUID, csrPEM string) (certPEM string, err error)
		RotateAccessKey(ctx context.Context, agent uuid.UUID, keyID, publicKeyPEM string) error
		RemoveAccessKey(ctx context.Context, agent uuid.UUID, keyID string) error
	}

	AgentsUseCase struct {
		store      AgentStore
		placements AgentPlacements
		sealer     AgentSealer
		fleet      AgentFleet
		remote     AgentRemote
		impacts    AgentImpacts
		now        func() time.Time
		// tokenMaxTTL is the longest lab access token the proxy accepts.
		tokenMaxTTL time.Duration

		capacityMu   sync.Mutex
		lastCapacity map[uuid.UUID]capacityReading
	}

	// capacityReading is the last capacity written for an agent, to skip repeated writes.
	capacityReading struct {
		cpu, memory *int64
		at          time.Time
	}

	AgentsDependencies struct {
		Store      AgentStore
		Placements AgentPlacements
		// Sealer is nil while the platform secrets key is unset: agents cannot be enrolled then.
		Sealer AgentSealer
		Fleet  AgentFleet
		Remote AgentRemote
		// Impacts reports the future reservations an agent's deletion affects; nil without a calendar.
		Impacts AgentImpacts
		Now     func() time.Time
		// AccessTokenMaxTTL is the longest lab access token the proxy accepts; the retention of a rotated-out
		// key follows it.
		AccessTokenMaxTTL time.Duration
	}

	// AgentAdminView is one agent for the admin list.
	AgentAdminView struct {
		infraModel.AgentRegistration
		// InUse is false for an archived (deleted) agent.
		InUse bool
		// Groups is how many lab groups the agent holds.
		Groups int64
		// RetiredKeys is how many rotated-out access keys still wait for removal at the agent.
		RetiredKeys int
		Probe       agentfleet.AgentProbe
	}

	// AgentsView is the admin agent list.
	AgentsView struct {
		Items []AgentAdminView
	}
)

func NewAgentsUseCase(deps AgentsDependencies) *AgentsUseCase {
	now := deps.Now
	if now == nil {
		now = time.Now
	}
	return &AgentsUseCase{store: deps.Store, placements: deps.Placements, sealer: deps.Sealer, fleet: deps.Fleet, remote: deps.Remote, impacts: deps.Impacts, now: now, tokenMaxTTL: deps.AccessTokenMaxTTL, lastCapacity: map[uuid.UUID]capacityReading{}}
}

// ListAgents lists the agents with their live state, ordered like the placement: by priority, then by name.
func (u *AgentsUseCase) ListAgents(ctx context.Context, includeArchived bool) (AgentsView, error) {
	list := u.store.ListRecords
	if includeArchived {
		list = u.store.ListAllRecords
	}
	records, err := list(ctx)
	if err != nil {
		return AgentsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list infrastructure agents").Err()
	}
	counts, err := u.placements.CountByAgent(ctx)
	if err != nil {
		return AgentsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count laboratories per agent").Err()
	}
	view := AgentsView{Items: make([]AgentAdminView, len(records))}
	var wg sync.WaitGroup
	for i, r := range records {
		item := AgentAdminView{AgentRegistration: r.AgentRegistration, Groups: counts[r.ID], RetiredKeys: len(r.RetiredAccessKeys), InUse: r.ArchivedAt == nil}
		view.Items[i] = item
		if item.InUse {
			wg.Add(1)
			go func() {
				defer wg.Done()
				view.Items[i].Probe = u.fleet.Probe(ctx, r.ID)
			}()
		}
	}
	wg.Wait()
	sort.SliceStable(view.Items, func(i, j int) bool {
		a, b := view.Items[i], view.Items[j]
		if a.Priority != b.Priority {
			return a.Priority < b.Priority
		}
		return a.Name < b.Name
	})
	return view, nil
}

// EnrollAgent adds an agent. The platform generates the mutual-TLS key and the access signing key
// pair itself, sends only the certificate request and the public access key with the one-time token,
// and stores the issued certificate with both private keys (encrypted). The tenant is the
// certificate's CN.
func (u *AgentsUseCase) EnrollAgent(ctx context.Context, in infraModel.AgentEnrollment) (AgentAdminView, error) {
	id, err := u.enroll(ctx, in, infraModel.AgentSourceAdmin)
	if err != nil {
		return AgentAdminView{}, err
	}
	return u.afterChange(ctx, id)
}

// BootstrapAgent enrolls the agent named by the deployment config (AGENT_ENDPOINT with a one-time
// token) when no live agent with that endpoint exists yet. Afterwards the token is not used again: the
// agent lives in the database like any other. It reports whether it enrolled; an enrollment that fails
// (a used or expired token) is returned for the caller to log, the daemon starts anyway.
func (u *AgentsUseCase) BootstrapAgent(ctx context.Context, in infraModel.AgentEnrollment) (enrolled bool, err error) {
	if strings.TrimSpace(in.Endpoint) == "" {
		return false, nil
	}
	records, err := u.store.ListRecords(ctx)
	if err != nil {
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to list infrastructure agents").Err()
	}
	for _, r := range records {
		if r.Endpoint == strings.TrimSpace(in.Endpoint) {
			return false, nil
		}
	}
	if _, err = u.enroll(ctx, in, infraModel.AgentSourceEnv); err != nil {
		return false, err
	}
	return true, u.reload(ctx)
}

// enroll runs the enrollment and stores the agent; source says how it was added.
func (u *AgentsUseCase) enroll(ctx context.Context, in infraModel.AgentEnrollment, source string) (uuid.UUID, error) {
	in, err := in.Normalize()
	if err != nil {
		return uuid.Nil, err
	}
	if u.sealer == nil {
		return uuid.Nil, infraModel.ErrAgentSecretsUnavailable.Err()
	}
	clientKey, err := agentcrypto.NewClientKey()
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to generate the client key").Err()
	}
	accessKey, err := agentcrypto.NewAccessKey()
	if err != nil {
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to generate the access key").Err()
	}
	certPEM, err := u.remote.Enroll(ctx, in.Endpoint, []byte(in.CAPEM), in.Token, clientKey.CSRPEM, accessKey.PublicKeyPEM, accessKey.ID)
	if err != nil {
		if errors.Is(err, ErrEnrollmentDenied) {
			return uuid.Nil, infraModel.ErrAgentEnrollmentRejected.WithError(err).Err()
		}
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to enroll the agent").Err()
	}
	cert, err := u.checkCertificate(certPEM, clientKey.KeyPEM)
	if err != nil {
		return uuid.Nil, err
	}
	id := uuid.Must(uuid.NewV7())
	keyCT, err := u.seal(id, "key", clientKey.KeyPEM)
	if err != nil {
		return uuid.Nil, err
	}
	accessCT, err := u.seal(id, "access", accessKey.PrivateKeyPEM)
	if err != nil {
		return uuid.Nil, err
	}
	now := u.now().UTC()
	record := infraModel.AgentRecord{
		AgentRegistration: infraModel.AgentRegistration{
			ID: id, Name: in.Name, Source: source, Endpoint: in.Endpoint, Configured: true, Enabled: in.Enabled,
			Priority: in.Priority, Tenant: cert.Tenant, AccessKeyID: accessKey.ID, CertNotAfter: &cert.NotAfter, CreatedAt: now, UpdatedAt: now,
		},
		CertPEM: certPEM, KeyCiphertext: keyCT, CAPEM: in.CAPEM, AccessPrivateKeyCiphertext: accessCT, AccessPublicKey: accessKey.PublicKeyPEM,
	}
	if _, err = u.store.Create(ctx, record); err != nil {
		if creator, unique := repositoryTools.UniqueViolationError(err, infraModel.ErrAgentExists); unique {
			return uuid.Nil, creator.WithError(err).Err()
		}
		return uuid.Nil, model.ErrPlatform.WithError(err).WithMessage("Failed to save the infrastructure agent").Err()
	}
	return id, nil
}

// UpdateAgent changes the label, order, switch and server CA of an admin agent.
func (u *AgentsUseCase) UpdateAgent(ctx context.Context, id uuid.UUID, in infraModel.AgentUpdate) (AgentAdminView, error) {
	existing, err := u.loadAdmin(ctx, id)
	if err != nil {
		return AgentAdminView{}, err
	}
	in, err = in.Normalize()
	if err != nil {
		return AgentAdminView{}, err
	}
	existing.Name, existing.CAPEM, existing.Enabled, existing.Priority, existing.UpdatedAt = in.Name, in.CAPEM, in.Enabled, in.Priority, u.now().UTC()
	ok, err := u.store.Update(ctx, existing)
	if err != nil {
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save the infrastructure agent").Err()
	}
	if !ok {
		return AgentAdminView{}, infraModel.ErrAgentNotFound.Err()
	}
	return u.afterChange(ctx, id)
}

// RenewAgentCertificate gets a new client certificate for a new key over the agent's current mutual-TLS
// connection. The certificate must name the same tenant: the tenant is the issuer of the access tokens.
func (u *AgentsUseCase) RenewAgentCertificate(ctx context.Context, id uuid.UUID) (AgentAdminView, error) {
	record, err := u.loadAdmin(ctx, id)
	if err != nil {
		return AgentAdminView{}, err
	}
	if err = u.renew(ctx, record); err != nil {
		return AgentAdminView{}, err
	}
	return u.afterChange(ctx, id)
}

func (u *AgentsUseCase) renew(ctx context.Context, record infraModel.AgentRecord) error {
	if u.sealer == nil {
		return infraModel.ErrAgentSecretsUnavailable.Err()
	}
	clientKey, err := agentcrypto.NewClientKey()
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to generate the client key").Err()
	}
	certPEM, err := u.remote.RenewCertificate(ctx, record.ID, clientKey.CSRPEM)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to renew the agent certificate").Err()
	}
	cert, err := u.checkCertificate(certPEM, clientKey.KeyPEM)
	if err != nil {
		return err
	}
	if cert.Tenant != record.Tenant {
		return infraModel.ErrAgentTenantChanged.Err()
	}
	keyCT, err := u.seal(record.ID, "key", clientKey.KeyPEM)
	if err != nil {
		return err
	}
	if _, err = u.store.SetCertificate(ctx, record.ID, certPEM, keyCT, cert.Tenant, cert.NotAfter, u.now().UTC()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save the renewed certificate").Err()
	}
	return nil
}

// RotateAgentAccessKey adds a new access public key at the agent (both keys work during the rotation),
// signs new tokens with the new key from now on and keeps the old key to be removed after
// the retention.
func (u *AgentsUseCase) RotateAgentAccessKey(ctx context.Context, id uuid.UUID) (AgentAdminView, error) {
	record, err := u.loadAdmin(ctx, id)
	if err != nil {
		return AgentAdminView{}, err
	}
	if u.sealer == nil {
		return AgentAdminView{}, infraModel.ErrAgentSecretsUnavailable.Err()
	}
	next, err := agentcrypto.NewAccessKey()
	if err != nil {
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to generate the access key").Err()
	}
	if err = u.remote.RotateAccessKey(ctx, id, next.ID, next.PublicKeyPEM); err != nil {
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to add the access key at the agent").Err()
	}
	sealed, err := u.seal(id, "access", next.PrivateKeyPEM)
	if err != nil {
		return AgentAdminView{}, err
	}
	now := u.now().UTC()
	retired := record.RetiredAccessKeys
	if record.AccessKeyID != "" {
		retired = append(append([]infraModel.RetiredKey(nil), retired...), infraModel.RetiredKey{KeyID: record.AccessKeyID, RetiredAt: now})
	}
	if _, err = u.store.SetAccessKey(ctx, id, next.ID, sealed, next.PublicKeyPEM, retired, now); err != nil {
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save the access key").Err()
	}
	return u.afterChange(ctx, id)
}

// MaintainAgents is the periodic upkeep: it renews client certificates that are past two thirds of their
// lifetime and removes retired access keys older than the retention. A failure of one agent does not stop
// the others; the next pass tries again.
func (u *AgentsUseCase) MaintainAgents(ctx context.Context) error {
	records, err := u.store.ListRecords(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list infrastructure agents").Err()
	}
	now := u.now().UTC()
	var errs []error
	changed := false
	for _, r := range records {
		if renewDue(r, now) {
			if err = u.renew(ctx, r); err != nil {
				log.Error().Err(err).Str("agent", r.Name).Msg("Agent certificate renewal failed")
				errs = append(errs, err)
			} else {
				changed = true
			}
		}
		if err = u.removeRetiredKeys(ctx, r, now); err != nil {
			log.Error().Err(err).Str("agent", r.Name).Msg("Removing a retired access key failed")
			errs = append(errs, err)
		}
	}
	if changed {
		if err = u.fleet.Reload(ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (u *AgentsUseCase) removeRetiredKeys(ctx context.Context, r infraModel.AgentRecord, now time.Time) error {
	if len(r.RetiredAccessKeys) == 0 {
		return nil
	}
	var waiting []infraModel.RetiredKey
	var errs []error
	for _, key := range r.RetiredAccessKeys {
		if now.Sub(key.RetiredAt) < u.accessKeyRetention() {
			waiting = append(waiting, key)
			continue
		}
		if err := u.remote.RemoveAccessKey(ctx, r.ID, key.KeyID); err != nil {
			waiting = append(waiting, key)
			errs = append(errs, err)
		}
	}
	if len(waiting) != len(r.RetiredAccessKeys) {
		if _, err := u.store.SetRetiredKeys(ctx, r.ID, waiting, now); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// PreviewAgentDelete says what deleting an agent would do, for the admin's confirmation.
func (u *AgentsUseCase) PreviewAgentDelete(ctx context.Context, id uuid.UUID) (DeletePreview, error) {
	if _, err := u.loadAdmin(ctx, id); err != nil {
		return DeletePreview{}, err
	}
	counts, err := u.placements.CountByAgent(ctx)
	if err != nil {
		return DeletePreview{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count laboratories per agent").Err()
	}
	preview := DeletePreview{RunningGroups: counts[id], FutureReservations: []ReservationImpact{}}
	if u.impacts != nil {
		impacts, impErr := u.impacts.FutureImpact(ctx, id, u.now().UTC())
		if impErr != nil {
			return DeletePreview{}, model.ErrPlatform.WithError(impErr).WithMessage("Failed to read the reservations of the agent").Err()
		}
		preview.FutureReservations = append(preview.FutureReservations, impacts...)
	}
	return preview, nil
}

// DeleteAgent soft-deletes an admin agent: blocked while it holds lab groups; with future reservations
// it needs confirm (after PreviewAgentDelete). The record stays for history, its keys, certificate and
// endpoint are wiped and its name is freed by an archive suffix. The reservations stay and become not
// covered.
func (u *AgentsUseCase) DeleteAgent(ctx context.Context, id uuid.UUID, confirm bool) error {
	record, err := u.loadAdmin(ctx, id)
	if err != nil {
		return err
	}
	preview, err := u.PreviewAgentDelete(ctx, id)
	if err != nil {
		return err
	}
	if preview.RunningGroups > 0 {
		return infraModel.ErrAgentInUse.Err()
	}
	if len(preview.FutureReservations) > 0 && !confirm {
		return infraModel.ErrAgentDeleteNeedsConfirm.Err()
	}
	at := u.now().UTC()
	ok, err := u.store.Archive(ctx, id, infraModel.ArchivedName(record.Name, at), at)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete the infrastructure agent").Err()
	}
	if !ok {
		return infraModel.ErrAgentNotFound.Err()
	}
	return u.reload(ctx)
}

// ReconnectAgent enrolls the same cluster again for new keys (lost or compromised credentials). The
// record, its priority and its lab groups stay; the tenant must be the same, as it is the issuer of the
// access tokens. It is not a move: a move is a delete plus a normal enrollment.
func (u *AgentsUseCase) ReconnectAgent(ctx context.Context, id uuid.UUID, token, caPEM string) (AgentAdminView, error) {
	record, err := u.loadAdmin(ctx, id)
	if err != nil {
		return AgentAdminView{}, err
	}
	token, caPEM = strings.TrimSpace(token), strings.TrimSpace(caPEM)
	if token == "" {
		return AgentAdminView{}, infraModel.ErrAgentInvalid.WithMessage("The enrollment token is required").Err()
	}
	if caPEM == "" {
		caPEM = record.CAPEM
	}
	if u.sealer == nil {
		return AgentAdminView{}, infraModel.ErrAgentSecretsUnavailable.Err()
	}
	clientKey, err := agentcrypto.NewClientKey()
	if err != nil {
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to generate the client key").Err()
	}
	accessKey, err := agentcrypto.NewAccessKey()
	if err != nil {
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to generate the access key").Err()
	}
	certPEM, err := u.remote.Enroll(ctx, record.Endpoint, []byte(caPEM), token, clientKey.CSRPEM, accessKey.PublicKeyPEM, accessKey.ID)
	if err != nil {
		if errors.Is(err, ErrEnrollmentDenied) {
			return AgentAdminView{}, infraModel.ErrAgentEnrollmentRejected.WithError(err).Err()
		}
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to reconnect the agent").Err()
	}
	cert, err := u.checkCertificate(certPEM, clientKey.KeyPEM)
	if err != nil {
		return AgentAdminView{}, err
	}
	if cert.Tenant != record.Tenant {
		return AgentAdminView{}, infraModel.ErrAgentTenantChanged.Err()
	}
	keyCT, err := u.seal(id, "key", clientKey.KeyPEM)
	if err != nil {
		return AgentAdminView{}, err
	}
	accessCT, err := u.seal(id, "access", accessKey.PrivateKeyPEM)
	if err != nil {
		return AgentAdminView{}, err
	}
	record.CertPEM, record.KeyCiphertext, record.CAPEM = certPEM, keyCT, caPEM
	record.Tenant, record.CertNotAfter = cert.Tenant, &cert.NotAfter
	record.AccessKeyID, record.AccessPrivateKeyCiphertext, record.AccessPublicKey = accessKey.ID, accessCT, accessKey.PublicKeyPEM
	record.UpdatedAt = u.now().UTC()
	if ok, err := u.store.ReplaceCredentials(ctx, record); err != nil {
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save the reconnected agent").Err()
	} else if !ok {
		return AgentAdminView{}, infraModel.ErrAgentNotFound.Err()
	}
	return u.afterChange(ctx, id)
}

// RecordAgentCapacity keeps the last known capacity of an agent (its tenant quota; nil = no limit on
// that resource) so that planning survives the agent being offline. Called on every capacity the agent
// reports; a write happens only when the values change or the stored reading is stale.
func (u *AgentsUseCase) RecordAgentCapacity(ctx context.Context, id uuid.UUID, cpuMillicores, memoryBytes *int64, seenAt time.Time) error {
	u.capacityMu.Lock()
	last, known := u.lastCapacity[id]
	u.capacityMu.Unlock()
	if known && equalInt64Ptr(last.cpu, cpuMillicores) && equalInt64Ptr(last.memory, memoryBytes) && seenAt.Sub(last.at) < capacityRewriteAfter {
		return nil
	}
	if err := u.store.SetCapacity(ctx, id, cpuMillicores, memoryBytes, seenAt); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record the agent capacity").Err()
	}
	u.capacityMu.Lock()
	u.lastCapacity[id] = capacityReading{cpu: cpuMillicores, memory: memoryBytes, at: seenAt}
	u.capacityMu.Unlock()
	return nil
}

// capacityRewriteAfter is how often an unchanged capacity is stored again, so "last seen" stays fresh.
const capacityRewriteAfter = 5 * time.Minute

func equalInt64Ptr(a, b *int64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// CheckAgent probes the agent now and reports its state.
func (u *AgentsUseCase) CheckAgent(ctx context.Context, id uuid.UUID) (AgentAdminView, error) {
	record, err := u.store.Get(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return AgentAdminView{}, infraModel.ErrAgentNotFound.Err()
		}
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the infrastructure agent").Err()
	}
	view := u.viewOf(ctx, record)
	view.Probe = u.fleet.Probe(ctx, id)
	return view, nil
}

func (u *AgentsUseCase) afterChange(ctx context.Context, id uuid.UUID) (AgentAdminView, error) {
	if err := u.reload(ctx); err != nil {
		return AgentAdminView{}, err
	}
	record, err := u.store.Get(ctx, id)
	if err != nil {
		return AgentAdminView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the infrastructure agent").Err()
	}
	view := u.viewOf(ctx, record)
	view.Probe = u.fleet.Probe(ctx, id)
	return view, nil
}

func (u *AgentsUseCase) viewOf(ctx context.Context, r infraModel.AgentRecord) AgentAdminView {
	view := AgentAdminView{AgentRegistration: r.AgentRegistration, InUse: true, RetiredKeys: len(r.RetiredAccessKeys)}
	if counts, err := u.placements.CountByAgent(ctx); err == nil {
		view.Groups = counts[r.ID]
	}
	return view
}

func (u *AgentsUseCase) reload(ctx context.Context) error {
	if err := u.fleet.Reload(ctx); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to apply the infrastructure agents").Err()
	}
	return nil
}

// loadAdmin loads an agent that is in use (not archived).
func (u *AgentsUseCase) loadAdmin(ctx context.Context, id uuid.UUID) (infraModel.AgentRecord, error) {
	record, err := u.store.Get(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return infraModel.AgentRecord{}, infraModel.ErrAgentNotFound.Err()
		}
		return infraModel.AgentRecord{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the infrastructure agent").Err()
	}
	if record.ArchivedAt != nil {
		return infraModel.AgentRecord{}, infraModel.ErrAgentNotFound.Err()
	}
	return record, nil
}

// checkCertificate reads the issued certificate: it must belong to our key and name a tenant.
func (u *AgentsUseCase) checkCertificate(certPEM, keyPEM string) (agentcrypto.Certificate, error) {
	cert, err := agentcrypto.ReadCertificate(certPEM)
	if err != nil {
		return agentcrypto.Certificate{}, infraModel.ErrAgentInvalid.WithError(err).WithMessage("The agent returned an unusable certificate").Err()
	}
	if err = agentcrypto.MatchesKey(certPEM, keyPEM); err != nil {
		return agentcrypto.Certificate{}, infraModel.ErrAgentInvalid.WithError(err).WithMessage("The agent returned a certificate for another key").Err()
	}
	return cert, nil
}

func (u *AgentsUseCase) seal(id uuid.UUID, field, plaintext string) (string, error) {
	if u.sealer == nil {
		return "", infraModel.ErrAgentSecretsUnavailable.Err()
	}
	sealed, err := u.sealer.EncryptWithContext([]byte(plaintext), infraModel.AgentSecretContext(id, field))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to seal the agent " + field + " key").Err()
	}
	return sealed, nil
}

// accessKeyRetention is how long a rotated-out access key stays at the agent.
func (u *AgentsUseCase) accessKeyRetention() time.Duration {
	return max(MinAccessKeyRetention, 3*u.tokenMaxTTL)
}

// renewDue says the agent's client certificate is past two thirds of its own lifetime. The lifetime
// comes from the stored certificate; without a readable one the end the agent reported and the lifetime
// it issues now stand in for it.
func renewDue(r infraModel.AgentRecord, now time.Time) bool {
	notBefore, notAfter, ok := certValidity(r.CertPEM)
	if !ok {
		if r.CertNotAfter == nil || r.Features == nil || r.Features.Certificate.IssuedTTLSeconds <= 0 {
			return false
		}
		notAfter = *r.CertNotAfter
		notBefore = notAfter.Add(-time.Duration(r.Features.Certificate.IssuedTTLSeconds) * time.Second)
	}
	renewAt := notBefore.Add(notAfter.Sub(notBefore) * certRenewAtNumerator / certRenewAtDenominator)
	return !now.Before(renewAt)
}

// certValidity reads the validity period of a PEM certificate.
func certValidity(certPEM string) (notBefore, notAfter time.Time, ok bool) {
	cert, err := agentcrypto.ReadCertificate(certPEM)
	if err != nil {
		return time.Time{}, time.Time{}, false
	}
	return cert.NotBefore, cert.NotAfter, true
}

// RecordAgentFeatures keeps what an agent last reported it offers the platform: persistence, the image
// cache, the scheduler, endpoints and the certificate. The agent sends it first and again when it
// changes, so every report is stored.
func (u *AgentsUseCase) RecordAgentFeatures(ctx context.Context, id uuid.UUID, features infraModel.AgentFeatures, seenAt time.Time) error {
	if err := u.store.SetFeatures(ctx, id, features, seenAt); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record the agent features").Err()
	}
	return nil
}
