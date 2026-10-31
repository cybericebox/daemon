// Package agentProfileRepo keeps the last node profile report of every agent.
package agentProfileRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

type Repository struct{ db postgres.DBTX }

func New(db postgres.DBTX) *Repository { return &Repository{db: db} }

// Save stores the report of an agent, replacing the one before unless that one is newer (reports from two replicas
// may cross).
func (r *Repository) Save(
	ctx context.Context,
	agent uuid.UUID,
	data []byte,
	receivedAt time.Time,
) error {
	_, err := r.db.Exec(
		ctx,
		`INSERT INTO infrastructure_agent_node_profiles (agent_id, profiles, received_at)
VALUES ($1, $2::jsonb, $3)
ON CONFLICT (agent_id) DO UPDATE SET profiles = EXCLUDED.profiles, received_at = EXCLUDED.received_at
WHERE infrastructure_agent_node_profiles.received_at <= EXCLUDED.received_at`,
		agent,
		data,
		receivedAt,
	)
	return err
}

// List returns the stored report of every agent.
func (r *Repository) List(ctx context.Context) ([]infraModel.AgentProfileReport, error) {
	rows, err := r.db.Query(
		ctx,
		`SELECT agent_id, profiles, received_at FROM infrastructure_agent_node_profiles`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []infraModel.AgentProfileReport
	for rows.Next() {
		var report infraModel.AgentProfileReport
		if err = rows.Scan(&report.AgentID, &report.Data, &report.ReceivedAt); err != nil {
			return nil, err
		}
		out = append(out, report)
	}
	return out, rows.Err()
}
