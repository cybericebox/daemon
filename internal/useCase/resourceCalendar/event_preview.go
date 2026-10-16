package resourceCalendarUseCase

import (
	"context"
	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
	"github.com/gofrs/uuid"
	"time"
)

func previewGroupHoldID(eventID, owner uuid.UUID) uuid.UUID {
	return uuid.NewV5(eventID, "participant-preview-group:"+owner.String())
}

// Finite placed event capacity is mandatory even when unplanned/author capacity exists.
func (u *ResourceCalendarUseCase) eventPreviewReservation(ctx context.Context, s Store, eventID uuid.UUID, now time.Time) (*calModel.Reservation, error) {
	if u.events == nil {
		return nil, calModel.ErrNotEnoughReserved.Err()
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil || e.ID != eventID || !e.InfrastructureAllowed {
		return nil, calModel.ErrNotEnoughReserved.Err()
	}
	r, err := s.GetEventReservation(ctx, eventID)
	if err != nil || r == nil || !r.Active() || r.EventID == nil || *r.EventID != eventID || r.Kind != calModel.KindEvent || r.Window.Start.IsZero() || !r.Window.End.After(r.Window.Start) || !r.Window.Contains(now) || r.Unplaced > 0 || len(r.Placement) == 0 {
		return nil, calModel.ErrNotEnoughReserved.Err()
	}
	return r, nil
}
func (u *ResourceCalendarUseCase) EventPreviewAllocated(ctx context.Context, eventID uuid.UUID) (bool, error) {
	_, err := u.eventPreviewReservation(ctx, u.store, eventID, u.now().UTC())
	if err != nil {
		return false, nil
	}
	return true, nil
}
func (u *ResourceCalendarUseCase) eventPreviewRoom(ctx context.Context, s Store, req EventTestLabRequest, now time.Time) (TestLabRoom, []calModel.TestLabHold, error) {
	r, err := u.eventPreviewReservation(ctx, s, req.EventID, now)
	if err != nil {
		return TestLabRoom{}, nil, err
	}
	if u.usage == nil {
		return TestLabRoom{}, nil, calModel.ErrNotEnoughReserved.Err()
	}
	usage, err := u.usage.Usage(ctx, now)
	if err != nil {
		return TestLabRoom{}, nil, err
	}
	if usage.UnaccountedByEvent[req.EventID] {
		return TestLabRoom{}, nil, calModel.ErrNotEnoughReserved.Err()
	}
	holds, err := s.EventPreviewHolds(ctx, req.EventID)
	if err != nil {
		return TestLabRoom{}, nil, err
	}
	held := usage.ByEvent[req.EventID]
	groupHeld := false
	var groupSize Amount
	for _, h := range holds {
		if h.ID == req.ID {
			return TestLabRoom{}, nil, calModel.ErrNotEnoughReserved.Err()
		}
		held = held.Add(h.Size)
		if h.Via == calModel.ViaEventPreviewGroup && h.GroupName == req.Group {
			groupHeld = true
			groupSize = h.Size
		}
	}
	overhead := req.GroupOverhead
	if overhead == (Amount{}) && u.overhead != nil {
		overhead = u.overhead()
	}
	if overhead.CPUMillicores <= 0 || overhead.MemoryBytes <= 0 {
		return TestLabRoom{}, nil, calModel.ErrNotEnoughReserved.Err()
	}
	if groupHeld && !overhead.Within(groupSize) {
		return TestLabRoom{}, nil, calModel.ErrNotEnoughReserved.Err()
	}
	needed := req.Size
	if !groupHeld {
		needed = needed.Add(overhead)
	}
	if !held.Add(needed).Within(r.Size) {
		return TestLabRoom{}, nil, calModel.ErrNotEnoughReserved.Err()
	}
	until := now.Add(u.leaseOf(req.Lease))
	if until.After(r.Window.End) {
		until = r.Window.End
	}
	result := []calModel.TestLabHold{{ID: req.ID, OwnerID: req.Owner, EventID: &req.EventID, GroupName: req.Group, Via: calModel.ViaEventPreview, ReservationID: &r.ID, Size: req.Size, StartsAt: now, ExpiresAt: until}}
	if !groupHeld {
		result = append(result, calModel.TestLabHold{ID: previewGroupHoldID(req.EventID, req.Owner), OwnerID: req.Owner, EventID: &req.EventID, GroupName: req.Group, Via: calModel.ViaEventPreviewGroup, ReservationID: &r.ID, Size: overhead, StartsAt: now, ExpiresAt: until})
	}
	return TestLabRoom{Available: true, Via: calModel.ViaEventPreview, ExpiresAt: until}, result, nil
}
func (u *ResourceCalendarUseCase) AdmitEventTestLab(ctx context.Context, req EventTestLabRequest) (TestLabRoom, error) {
	if req.ID == uuid.Nil || req.EventID == uuid.Nil || req.Owner == uuid.Nil || req.Group == "" || req.Size.CPUMillicores < 0 || req.Size.MemoryBytes < 0 {
		return TestLabRoom{}, calModel.ErrNotEnoughReserved.Err()
	}
	var room TestLabRoom
	err := u.inTx(ctx, func(ctx context.Context, s Store) error {
		var holds []calModel.TestLabHold
		var err error
		room, holds, err = u.eventPreviewRoom(ctx, s, req, u.now().UTC())
		if err != nil {
			return err
		}
		for _, h := range holds {
			if err = s.SaveHold(ctx, h); err != nil {
				return err
			}
		}
		return nil
	})
	return room, err
}
func (u *ResourceCalendarUseCase) CheckEventTestLabRoom(ctx context.Context, req EventTestLabRequest) (TestLabRoom, error) {
	var room TestLabRoom
	err := u.inTx(ctx, func(ctx context.Context, s Store) error {
		var err error
		room, _, err = u.eventPreviewRoom(ctx, s, req, u.now().UTC())
		return err
	})
	return room, err
}

// Release occurs only after the test engine confirms deletion. A group's fixed
// services remain charged while any of its other labs has not been removed.
func (u *ResourceCalendarUseCase) ReleaseEventTestLab(ctx context.Context, eventID, id uuid.UUID) error {
	return u.inTx(ctx, func(ctx context.Context, s Store) error {
		holds, err := s.EventPreviewHolds(ctx, eventID)
		if err != nil {
			return err
		}
		var target *calModel.TestLabHold
		for i := range holds {
			if holds[i].ID == id {
				target = &holds[i]
			}
		}
		if target == nil {
			return nil
		}
		if err = s.DeleteHold(ctx, id); err != nil {
			return err
		}
		for _, h := range holds {
			if h.ID != id && h.Via == calModel.ViaEventPreview && h.GroupName == target.GroupName {
				return nil
			}
		}
		return s.DeleteHold(ctx, previewGroupHoldID(eventID, target.OwnerID))
	})
}
func (u *ResourceCalendarUseCase) ExtendEventTestLab(ctx context.Context, eventID, id, owner uuid.UUID, until time.Time) (time.Time, error) {
	var expiry time.Time
	err := u.inTx(ctx, func(ctx context.Context, s Store) error {
		now := u.now().UTC()
		r, err := u.eventPreviewReservation(ctx, s, eventID, now)
		if err != nil {
			return err
		}
		holds, err := s.EventPreviewHolds(ctx, eventID)
		if err != nil {
			return err
		}
		for _, h := range holds {
			if h.ID == id && h.OwnerID == owner && h.Via == calModel.ViaEventPreview {
				if !h.ExpiresAt.After(now) {
					return calModel.ErrNotEnoughReserved.Err()
				}
				expiry = until
				if expiry.After(r.Window.End) {
					expiry = r.Window.End
				}
				h.ExpiresAt = expiry
				return s.SaveHold(ctx, h)
			}
		}
		return calModel.ErrNotEnoughReserved.Err()
	})
	return expiry, err
}

// Scoped run holds are also durable cleanup records. Include unexpired holds:
// deployment transaction failure can leave a live native lab with no DB row.
func (u *ResourceCalendarUseCase) EventPreviewCleanupCandidates(ctx context.Context) ([]calModel.TestLabHold, error) {
	holds, err := u.store.ActiveHolds(ctx, u.now().UTC())
	if err != nil {
		return nil, err
	}
	var out []calModel.TestLabHold
	for _, h := range holds {
		if h.EventID != nil && h.Via == calModel.ViaEventPreview {
			out = append(out, h)
		}
	}
	return out, nil
}
