// Package labPlacementRepo remembers which infrastructure agent holds a lab group.
package labPlacementRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
)

type Queries interface {
	ClaimLabGroupPlacement(context.Context, postgres.ClaimLabGroupPlacementParams) (uuid.UUID, error)
	GetLabGroupPlacement(context.Context, string) (uuid.UUID, error)
	DeleteLabGroupPlacement(context.Context, string) error
	CountLabGroupPlacementsByAgent(context.Context) ([]postgres.CountLabGroupPlacementsByAgentRow, error)
}

type Repository struct {
	q   Queries
	now func() time.Time
}

func New(q Queries) *Repository { return &Repository{q: q, now: time.Now} }

// Get returns the agent of the group; found is false for a group that is not placed.
func (r *Repository) Get(ctx context.Context, group string) (agent uuid.UUID, found bool, err error) {
	agent, err = r.q.GetLabGroupPlacement(ctx, group)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return uuid.Nil, false, nil
		}
		return uuid.Nil, false, err
	}
	return agent, true, nil
}

// Claim places the group on the agent unless it is placed already and returns the agent that holds
// it. The event and team of an event group are recorded for reporting.
func (r *Repository) Claim(ctx context.Context, group string, agent uuid.UUID) (uuid.UUID, error) {
	params := postgres.ClaimLabGroupPlacementParams{LabGroupName: group, AgentID: agent, CreatedAt: r.now().UTC()}
	if eventID, teamID, ok := labBindingModel.ParseGroupName(group); ok {
		params.EventID = uuid.NullUUID{UUID: eventID, Valid: true}
		params.EventTeamID = uuid.NullUUID{UUID: teamID, Valid: true}
	}
	return r.q.ClaimLabGroupPlacement(ctx, params)
}

// Release forgets a group that was torn down.
func (r *Repository) Release(ctx context.Context, group string) error {
	return r.q.DeleteLabGroupPlacement(ctx, group)
}

// CountByAgent is how many lab groups each agent holds.
func (r *Repository) CountByAgent(ctx context.Context) (map[uuid.UUID]int64, error) {
	rows, err := r.q.CountLabGroupPlacementsByAgent(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]int64, len(rows))
	for _, row := range rows {
		out[row.AgentID] = row.Groups
	}
	return out, nil
}
