package event

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gofrs/uuid"

	labAccessSyncModel "github.com/cybericebox/daemon/internal/delivery/repository/labAccessSyncRepo"
	"github.com/cybericebox/daemon/internal/model"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

// LabClientAccess is the desired complete ACL for one named VPN client in a
// Laboratory LabGroup. The caller always replaces the complete set, never
// grants one Lab incrementally.
type LabClientAccess = labAccessModel.ClientPolicy

// labAccessInfrastructure is deliberately separate from Infrastructure while
// the deployed Laboratory agent lacks the capability. Existing agents retain
// their current contract; this optional capability makes every sync retryable
// rather than pretending a VPN route was revoked or granted.
type labAccessInfrastructure interface {
	SetLabGroupVPNDisabled(context.Context, string, bool) error
	SetLabGroupSuspended(context.Context, string, bool) error
	ReconcileLabGroupAccess(context.Context, string, []labAccessModel.ClientPolicy) error
}

const labAccessSyncBatchSize = 100

func (u *EventUseCase) RequestLabAccessSync(ctx context.Context, teamID uuid.UUID) error {
	return requestLabAccessSyncInTransaction(ctx, u.labAccessSyncs, teamID, time.Now())
}

func (u *EventUseCase) RequestEventLabAccessSyncs(ctx context.Context, eventID uuid.UUID) error {
	if err := u.labAccessSyncs.RequestEvent(ctx, eventID, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to request event laboratory access sync").Err()
	}
	return nil
}

func (u *EventUseCase) supportsLabAccessPolicy() bool {
	_, ok := u.infra.(labAccessInfrastructure)
	return ok
}

func (u *EventUseCase) requestLabAccessSyncInTransaction(ctx context.Context, repo IRepository, teamID uuid.UUID, now time.Time) error {
	if !u.supportsLabAccessPolicy() {
		return nil
	}
	return requestLabAccessSyncInTransaction(ctx, labAccessSyncModel.New(repo), teamID, now)
}

// ReconcilePendingLabAccess processes durable desired revisions. An agent
// error intentionally leaves its revision dirty; a later worker pass retries
// the same full replacement policy.
func (u *EventUseCase) ReconcilePendingLabAccess(ctx context.Context) error {
	if !u.laboratoriesConfigured() {
		return nil
	}
	if u.infrastructureCapability != nil {
		if err := u.infrastructureCapability.RequireLaboratories(ctx); err != nil {
			return err
		}
	}
	dirty, err := u.labAccessSyncs.ListDirty(ctx, labAccessSyncBatchSize)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list pending laboratory access syncs").Err()
	}
	now := time.Now()
	var errs []error
	for _, sync := range dirty {
		if !u.labAccessBackoff.ready(sync.TeamID, now) {
			continue
		}
		if err = u.reconcileLabAccess(ctx, sync); err != nil {
			if _, terminating := infraModel.AsTerminating(err); terminating || errors.Is(err, infraModel.ErrNoAgentFitsTask.Err()) {
				return err
			}
			// One failing team neither blocks the others nor is retried on every
			// pass: it waits out a growing delay, so its error reaches the journal
			// once per delay instead of once per pass.
			u.labAccessBackoff.fail(sync.TeamID, now)
			errs = append(errs, err)
			continue
		}
		u.labAccessBackoff.succeed(sync.TeamID)
	}
	return errors.Join(errs...)
}

func (u *EventUseCase) reconcileLabAccess(ctx context.Context, sync labAccessSyncModel.Sync) error {
	if u.infrastructureCapability != nil {
		if err := u.infrastructureCapability.RequireLaboratories(ctx); err != nil {
			return err
		}
	}
	access, ok := u.infra.(labAccessInfrastructure)
	if !ok {
		return infraUnavailable()
	}
	clients, err := u.labAccessSyncs.Clients(ctx, sync.TeamID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list laboratory access clients").Err()
	}
	labs, err := u.labAccessSyncs.Labs(ctx, sync.TeamID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list laboratory access labs").Err()
	}
	group, err := labBindingModel.GroupName(sync.EventID, sync.TeamID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to derive team VPN group").Err()
	}
	if sync.VPNEnabled {
		if err = u.infra.EnsureVPNGroup(u.withPlacementNeed(ctx, sync.EventID), group); err != nil {
			if errors.Is(err, infraModel.ErrNoAgentFitsTask.Err()) {
				return err
			}
			return model.ErrPlatform.WithError(err).WithMessage("Failed to ensure team VPN group").Err()
		}
	}
	allowed := make([]string, 0, len(labs))
	for _, lab := range labs {
		if lab.Group != group {
			return model.ErrPlatform.WithMessage("Team laboratory bindings have inconsistent groups").Err()
		}
		// The web proxy checks this same policy, so the allow list does not
		// depend on the VPN: events with web tasks but no VPN still list every
		// approved member with the available labs.
		if sync.RuntimeOpen && lab.Available {
			allowed = append(allowed, lab.Name)
		}
	}
	// Stop both group services on disable, before attempting policy replacement.
	if !sync.VPNEnabled {
		if err = access.SetLabGroupVPNDisabled(ctx, group, true); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to suspend team VPN group").Err()
		}
		if err = access.SetLabGroupSuspended(ctx, group, true); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to suspend team laboratory group").Err()
		}
	}
	if sync.VPNEnabled || len(labs) > 0 {
		// Every member has a LabGroupClient from the moment they are in the team
		// (or on the staff), before any lab is open; only the access config is
		// handed out on demand. Idempotent: a stored config is never recreated.
		if u.vpn != nil {
			for _, userID := range clients {
				if _, err = ensureParticipantVPNConfig(ctx, u.vpn, u.infra, group, sync.EventID, userID); err != nil {
					return err
				}
			}
		}
		policy := make([]LabClientAccess, 0, len(clients))
		for _, userID := range clients {
			policy = append(policy, LabClientAccess{Name: participantLabClientName(userID), AllowedLabs: append([]string(nil), allowed...)})
		}
		if err = access.ReconcileLabGroupAccess(ctx, group, policy); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to reconcile laboratory access policy").Err()
		}
	}
	// On enable, install the complete (possibly empty) ACL before bringing the
	// group's VPN and internet gateway back. Lab routes remain closed until
	// runtime access is open, even though both group services run before start.
	if sync.VPNEnabled {
		if err = access.SetLabGroupVPNDisabled(ctx, group, false); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to resume team VPN group").Err()
		}
		if err = access.SetLabGroupSuspended(ctx, group, false); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to start team laboratory group services").Err()
		}
	}
	if affected, markErr := u.labAccessSyncs.MarkApplied(ctx, sync.TeamID, sync.DesiredRevision, sync.RuntimeOpen, sync.VPNEnabled, sync.StageEpoch, time.Now()); markErr != nil {
		return model.ErrPlatform.WithError(markErr).WithMessage("Failed to acknowledge laboratory access policy").Err()
	} else if affected == 0 {
		// Desired state changed while the agent was applying the old replacement;
		// leave it dirty for the next pass instead of reporting a false success.
		return nil
	}
	return nil
}

func (u *EventUseCase) requestLabAccessSyncAfterReady(ctx context.Context, teamID uuid.UUID) error {
	if !u.supportsLabAccessPolicy() {
		return nil
	}
	if err := u.RequestLabAccessSync(ctx, teamID); err != nil {
		return fmt.Errorf("request lab access sync: %w", err)
	}
	return nil
}

func requestLabAccessSyncInTransaction(ctx context.Context, repo *labAccessSyncModel.Repository, teamID uuid.UUID, now time.Time) error {
	if err := repo.Request(ctx, teamID, now); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to request laboratory access sync").Err()
	}
	return nil
}

const (
	labAccessBackoffMin = 10 * time.Second
	labAccessBackoffMax = 5 * time.Minute
)

// labAccessBackoff is the per-team retry delay of failing access syncs. It is
// in memory only: a restart retries everything once, which is harmless.
type labAccessBackoff struct {
	mu    sync.Mutex
	state map[uuid.UUID]labAccessBackoffState
}

type labAccessBackoffState struct {
	failures int
	until    time.Time
}

func newLabAccessBackoff() *labAccessBackoff {
	return &labAccessBackoff{state: map[uuid.UUID]labAccessBackoffState{}}
}

func (b *labAccessBackoff) ready(team uuid.UUID, now time.Time) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	st, ok := b.state[team]
	return !ok || !now.Before(st.until)
}

func (b *labAccessBackoff) fail(team uuid.UUID, now time.Time) {
	b.mu.Lock()
	defer b.mu.Unlock()
	st := b.state[team]
	delay := labAccessBackoffMin << min(st.failures, 10)
	if delay > labAccessBackoffMax {
		delay = labAccessBackoffMax
	}
	b.state[team] = labAccessBackoffState{failures: st.failures + 1, until: now.Add(delay)}
}

func (b *labAccessBackoff) succeed(team uuid.UUID) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.state, team)
}
