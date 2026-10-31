// Package waitDetailRepo keeps what a waiting stand Lab needs and what the reservation has left.
package waitDetailRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventStandModel "github.com/cybericebox/daemon/internal/model/eventStand"
)

type Repository struct{ db postgres.DBTX }

func New(db postgres.DBTX) *Repository { return &Repository{db: db} }

// Set records the need and the room of the waiting Lab binding.
func (r *Repository) Set(ctx context.Context, bindingID uuid.UUID, d eventStandModel.WaitDetail, now time.Time) error {
	_, err := r.db.Exec(ctx, `INSERT INTO lab_binding_wait_details (lab_binding_id, need_cpu_millicores, need_memory_bytes, have_cpu_millicores, have_memory_bytes, need_storage_bytes, have_storage_bytes, updated_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
ON CONFLICT (lab_binding_id) DO UPDATE SET need_cpu_millicores = EXCLUDED.need_cpu_millicores, need_memory_bytes = EXCLUDED.need_memory_bytes,
 have_cpu_millicores = EXCLUDED.have_cpu_millicores, have_memory_bytes = EXCLUDED.have_memory_bytes,
 need_storage_bytes = EXCLUDED.need_storage_bytes, have_storage_bytes = EXCLUDED.have_storage_bytes, updated_at = EXCLUDED.updated_at`,
		bindingID, d.NeedCPUMillicores, d.NeedMemoryBytes, d.HaveCPUMillicores, d.HaveMemoryBytes, d.NeedStorageBytes, d.HaveStorageBytes, now)
	return err
}

// Clear forgets the detail of a Lab that does not wait any more.
func (r *Repository) Clear(ctx context.Context, bindingID uuid.UUID) error {
	_, err := r.db.Exec(ctx, `DELETE FROM lab_binding_wait_details WHERE lab_binding_id = $1`, bindingID)
	return err
}
