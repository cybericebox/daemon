// Package labAccessSyncRepo persists desired versus applied Laboratory ACL state.
package labAccessSyncRepo

import (
	"context"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/gofrs/uuid"
)

type Queries interface {
	RequestEventLabAccessSync(context.Context, postgres.RequestEventLabAccessSyncParams) (postgres.EventLabAccessSync, error)
	RequestEventLabAccessSyncsForEvent(context.Context, postgres.RequestEventLabAccessSyncsForEventParams) error
	ListDirtyEventLabAccessSyncs(context.Context, int32) ([]postgres.ListDirtyEventLabAccessSyncsRow, error)
	MarkEventLabAccessSyncApplied(context.Context, postgres.MarkEventLabAccessSyncAppliedParams) (int64, error)
	ListEventLabAccessClients(context.Context, uuid.UUID) ([]uuid.UUID, error)
	ListEventLabAccessLabs(context.Context, uuid.UUID) ([]postgres.ListEventLabAccessLabsRow, error)
	RequestModeratorsTeamLabAccessSync(context.Context, postgres.RequestModeratorsTeamLabAccessSyncParams) error
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

type Sync struct {
	TeamID, EventID                  uuid.UUID
	DesiredRevision, AppliedRevision int64
	UpdatedAt                        time.Time
	RuntimeOpen                      bool
	VPNEnabled                       bool
}

type Lab struct {
	Group, Name string
	Available   bool
}

func (r *Repository) Request(ctx context.Context, teamID uuid.UUID, now time.Time) error {
	_, err := r.q.RequestEventLabAccessSync(ctx, postgres.RequestEventLabAccessSyncParams{EventTeamID: teamID, UpdatedAt: now})
	return err
}

func (r *Repository) RequestEvent(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	return r.q.RequestEventLabAccessSyncsForEvent(ctx, postgres.RequestEventLabAccessSyncsForEventParams{EventID: eventID, UpdatedAt: now})
}

// RequestModerators re-derives the moderators team ACL (its clients are the
// event managers); a no-op while the event has no moderators team.
func (r *Repository) RequestModerators(ctx context.Context, eventID uuid.UUID, now time.Time) error {
	return r.q.RequestModeratorsTeamLabAccessSync(ctx, postgres.RequestModeratorsTeamLabAccessSyncParams{EventID: eventID, UpdatedAt: now})
}

func (r *Repository) ListDirty(ctx context.Context, limit int32) ([]Sync, error) {
	rows, err := r.q.ListDirtyEventLabAccessSyncs(ctx, limit)
	if err != nil {
		return nil, err
	}
	out := make([]Sync, 0, len(rows))
	for _, row := range rows {
		out = append(out, Sync{TeamID: row.EventTeamID, EventID: row.EventID, DesiredRevision: row.DesiredRevision, AppliedRevision: row.AppliedRevision, UpdatedAt: row.UpdatedAt, RuntimeOpen: row.RuntimeOpen, VPNEnabled: row.VpnEnabled})
	}
	return out, nil
}

func (r *Repository) MarkApplied(ctx context.Context, teamID uuid.UUID, desired int64, runtimeOpen, vpnEnabled bool, now time.Time) (int64, error) {
	return r.q.MarkEventLabAccessSyncApplied(ctx, postgres.MarkEventLabAccessSyncAppliedParams{EventTeamID: teamID, DesiredRevision: desired, RuntimeOpen: runtimeOpen, VpnEnabled: vpnEnabled, UpdatedAt: now})
}

func (r *Repository) Clients(ctx context.Context, teamID uuid.UUID) ([]uuid.UUID, error) {
	return r.q.ListEventLabAccessClients(ctx, teamID)
}

func (r *Repository) Labs(ctx context.Context, teamID uuid.UUID) ([]Lab, error) {
	rows, err := r.q.ListEventLabAccessLabs(ctx, teamID)
	if err != nil {
		return nil, err
	}
	out := make([]Lab, 0, len(rows))
	for _, row := range rows {
		out = append(out, Lab{Group: row.LabGroupName, Name: row.LabName, Available: row.Available})
	}
	return out, nil
}
