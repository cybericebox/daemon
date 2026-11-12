package imageRegistryRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// ExternalImageConfig is the stored start configuration of an external image reference.
type ExternalImageConfig struct {
	Digest     string
	Service    exerciseModel.ImageService
	ResolvedAt time.Time
}

// ExternalImageConfigs keeps the start configuration of images by reference to an external registry, by normalized
// reference and credential scope ("" public, else an event id).
type ExternalImageConfigs struct{ db postgres.DBTX }

func NewExternalImageConfigs(db postgres.DBTX) *ExternalImageConfigs {
	return &ExternalImageConfigs{db: db}
}

// Save stores the configuration read for a reference in a scope; reading it again replaces it.
func (r *ExternalImageConfigs) Save(ctx context.Context, reference, scope, digest string, service exerciseModel.ImageService, now time.Time) error {
	encoded, err := json.Marshal(service)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO external_image_configs (reference, scope, digest, service, resolved_at) VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (reference, scope) DO UPDATE SET digest = EXCLUDED.digest, service = EXCLUDED.service, resolved_at = EXCLUDED.resolved_at`,
		reference, scope, digest, encoded, now)
	return err
}

// Get reads the configurations of the references that were read in one of the scopes; a reference read in several keeps
// the one of the first scope listed. A reference without one is not in the answer.
func (r *ExternalImageConfigs) Get(ctx context.Context, references, scopes []string) (map[string]ExternalImageConfig, error) {
	out := map[string]ExternalImageConfig{}
	if len(references) == 0 || len(scopes) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `SELECT reference, scope, digest, service, resolved_at FROM external_image_configs
WHERE reference = ANY($1) AND scope = ANY($2)`, references, scopes)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rank := map[string]int{}
	for i, scope := range scopes {
		rank[scope] = i + 1
	}
	best := map[string]int{}
	for rows.Next() {
		var reference, scope string
		var entry ExternalImageConfig
		var raw []byte
		if err = rows.Scan(&reference, &scope, &entry.Digest, &raw, &entry.ResolvedAt); err != nil {
			return nil, err
		}
		if json.Unmarshal(raw, &entry.Service) != nil {
			continue // an unreadable row reads as "not known"
		}
		if current, ok := best[reference]; !ok || rank[scope] < current {
			best[reference] = rank[scope]
			out[reference] = entry
		}
	}
	return out, rows.Err()
}
