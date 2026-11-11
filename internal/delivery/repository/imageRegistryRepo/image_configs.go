package imageRegistryRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// ImageConfigs keeps the start configuration of the images of the platform registry, by manifest digest.
type ImageConfigs struct{ db postgres.DBTX }

func NewImageConfigs(db postgres.DBTX) *ImageConfigs { return &ImageConfigs{db: db} }

// Save stores the configuration of a digest; the same digest again keeps it up to date.
func (r *ImageConfigs) Save(ctx context.Context, digest string, service exerciseModel.ImageService, now time.Time) error {
	encoded, err := json.Marshal(service)
	if err != nil {
		return err
	}
	_, err = r.db.Exec(ctx, `INSERT INTO platform_image_configs (digest, service, created_at) VALUES ($1, $2, $3)
ON CONFLICT (digest) DO UPDATE SET service = EXCLUDED.service`, digest, encoded, now)
	return err
}

// Get reads the configurations of the digests; a digest without one is not in the answer.
func (r *ImageConfigs) Get(ctx context.Context, digests []string) (map[string]exerciseModel.ImageService, error) {
	out := map[string]exerciseModel.ImageService{}
	if len(digests) == 0 {
		return out, nil
	}
	rows, err := r.db.Query(ctx, `SELECT digest, service FROM platform_image_configs WHERE digest = ANY($1)`, digests)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var digest string
		var raw []byte
		if err = rows.Scan(&digest, &raw); err != nil {
			return nil, err
		}
		var service exerciseModel.ImageService
		if err = json.Unmarshal(raw, &service); err != nil {
			continue // an unreadable row reads as "not known"
		}
		out[digest] = service
	}
	return out, rows.Err()
}
