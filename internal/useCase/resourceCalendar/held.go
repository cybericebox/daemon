package resourceCalendarUseCase

import (
	"context"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"time"
)

// Outstanding runtime without a covering reservation cannot become free room
// at a calendar boundary, cancellation or telemetry outage. The owner must
// restore coverage or obtain exact release evidence. Logical retained quota
// remains separate and need not disappear when a compute window ends.
func (u *ResourceCalendarUseCase) requireHeldCoverage(ctx context.Context, s Store, now time.Time, candidate *calModel.Reservation) error {
	if u.usage == nil {
		return nil
	}
	usage, err := u.usage.Usage(ctx, now)
	if err != nil {
		return platformErr(err, "Failed to read held allocations")
	}
	if len(usage.UnaccountedByEvent) > 0 {
		return calModel.ErrNotEnoughReserved.Err()
	}
	ids := map[uuid.UUID]bool{}
	for id := range usage.ByEvent {
		ids[id] = true
	}
	for id := range usage.StorageByEvent {
		ids[id] = true
	}
	for id := range ids {
		compute := usage.ByEvent[id]
		storage := usage.StorageByEvent[id].SnapshotQuotaBytes
		if compute == (Amount{}) && storage == 0 {
			continue
		}
		var r *calModel.Reservation
		if candidate != nil && candidate.EventID != nil && *candidate.EventID == id {
			r = candidate
		} else {
			r, err = s.GetEventReservation(ctx, id)
			if err != nil {
				if compute == (Amount{}) {
					continue
				}
				return calModel.ErrNotEnoughReserved.Err()
			}
		}
		if r == nil || !r.Active() || !compute.Within(r.Size) || storage > r.SizeSnapshotQuotaBytes || (compute != (Amount{}) && (!r.Window.Contains(now) || r.Unplaced > 0 || len(r.Placement) == 0)) {
			return calModel.ErrNotEnoughReserved.Err()
		}
	}
	return nil
}
