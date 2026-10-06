package resourceCalendarUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	calModel "github.com/cybericebox/daemon/internal/model/resourceCalendar"
)

// maxActiveBookings is how many windows one author holds at once.
const maxActiveBookings = 3

// sizeWithOverhead adds the group's own pods a test laboratory brings.
func (u *ResourceCalendarUseCase) sizeWithOverhead(size Amount) Amount {
	if u.overhead == nil {
		return size
	}
	return size.Add(u.overhead())
}

// admission is the decision for one test laboratory.
type admission struct {
	via         string
	reservation *uuid.UUID
	nearest     *time.Time
}

// decideTestLab decides whether a test laboratory may start now: inside the author's own booked window, else
// inside the guaranteed pool, else from room no event has reserved above the pool; otherwise it says when the
// nearest free window starts. The calendar lock is held by the caller.
func (u *ResourceCalendarUseCase) decideTestLab(ctx context.Context, s Store, states []agentState, size, device Amount, owner uuid.UUID, lease time.Duration, now time.Time) (admission, error) {
	settings, err := s.Settings(ctx)
	if err != nil {
		return admission{}, platformErr(err, "Failed to read the calendar settings")
	}
	holds, err := s.ActiveHolds(ctx, now)
	if err != nil {
		return admission{}, platformErr(err, "Failed to read the test laboratory holds")
	}
	// 1. The author's booked window that covers the lease and still has room.
	bookings, err := s.ListOwnedBookings(ctx, owner, now)
	if err != nil {
		return admission{}, platformErr(err, "Failed to read the bookings")
	}
	for _, b := range bookings {
		if !b.Window.Contains(now) {
			continue
		}
		held := Amount{}
		for _, h := range holds {
			if h.ReservationID != nil && *h.ReservationID == b.ID {
				held = held.Add(h.Size)
			}
		}
		if held.Add(size).Within(b.Size) {
			id := b.ID
			return admission{via: calModel.ViaBooking, reservation: &id}, nil
		}
	}
	// 2. The guaranteed pool: always on, never reserved by events.
	pooled := Amount{}
	for _, h := range holds {
		if h.Via != calModel.ViaBooking {
			pooled = pooled.Add(h.Size)
		}
	}
	if pooled.Add(size).Within(settings.TestPool) {
		return admission{via: calModel.ViaPool}, nil
	}
	// 3. Above the pool: room no event or booking has reserved while the lease runs, on one agent, and the pool
	// (or what the labs hold above it) stays untouched.
	agents := usedAgents(states)
	w, err := calModel.NewWindow(now, now.Add(lease))
	if err != nil {
		return admission{}, calModel.ErrBookingInvalid.Err()
	}
	rs, err := s.ListInWindow(ctx, w)
	if err != nil {
		return admission{}, platformErr(err, "Failed to read the reservations")
	}
	if u.fitsAbovePool(agents, rs, w, settings.TestPool, pooled, size, device) {
		return admission{via: calModel.ViaFree}, nil
	}
	// 4. Nothing now: the nearest free window for a booking.
	later, err := s.ListEndingAfter(ctx, now)
	if err != nil {
		return admission{}, platformErr(err, "Failed to read the reservations")
	}
	if from, ok := calModel.NearestFree(now, lease, u.cfg.SearchHorizon, agents, later, settings.TestPool, size, device); ok {
		return admission{nearest: &from}, nil
	}
	return admission{}, nil
}

// fitsAbovePool: some agent holds the lab in every slot of the lease next to what is placed there, and the
// room that is free in total, after the pool (or what the running labs already hold above it), is enough.
func (u *ResourceCalendarUseCase) fitsAbovePool(agents []calModel.Agent, rs []*calModel.Reservation, w calModel.Window, pool, pooled, size, device Amount) bool {
	peak := calModel.PeakLoad(w, rs)
	var free Amount
	fits := false
	for _, a := range agents {
		// A maintenance window inside the lease takes the agent's room for all of it.
		a.Capacity = a.CapacityOver(w)
		f := a.Free(peak[a.ID])
		free.CPUMillicores = min(free.CPUMillicores+f.CPUMillicores, calModel.Unlimited)
		free.MemoryBytes = min(free.MemoryBytes+f.MemoryBytes, calModel.Unlimited)
		if a.Allows(device) && size.Within(f) {
			fits = true
		}
	}
	held := pool.Max(pooled)
	rest := Amount{CPUMillicores: free.CPUMillicores - held.CPUMillicores, MemoryBytes: free.MemoryBytes - held.MemoryBytes}
	return fits && size.Within(rest)
}

func (u *ResourceCalendarUseCase) leaseOf(l time.Duration) time.Duration {
	if l <= 0 {
		return u.cfg.TestLabLease
	}
	return l
}

func noRoom(a admission, lease time.Duration) error {
	e := calModel.ErrNoTestLabRoom.WithPublicContext("window_minutes", int(lease/time.Minute))
	if a.nearest != nil {
		e = e.WithPublicContext("nearest_from", a.nearest.UTC().Format(time.RFC3339))
	}
	return e.Err()
}

// AdmitTestLab decides, and records the hold of, a catalog author's test laboratory. It fails with
// ErrNoTestLabRoom ("no free resources now, the nearest window is from HH:MM") when neither a booking, the pool
// nor free room holds it; the author may then book that window. Release the hold when the laboratory ends.
func (u *ResourceCalendarUseCase) AdmitTestLab(ctx context.Context, req TestLabRequest) (TestLabRoom, error) {
	now := u.now().UTC()
	lease := u.leaseOf(req.Lease)
	size := u.sizeWithOverhead(req.Size)
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return TestLabRoom{}, err
	}
	var result admission
	err = u.inTx(ctx, func(ctx context.Context, s Store) error {
		a, dErr := u.decideTestLab(ctx, s, states, size, req.LargestDevice, req.Owner, lease, now)
		if dErr != nil {
			return dErr
		}
		result = a
		if a.via == "" {
			return noRoom(a, lease)
		}
		hold := calModel.TestLabHold{ID: req.ID, OwnerID: req.Owner, Via: a.via, ReservationID: a.reservation, Size: size, StartsAt: now, ExpiresAt: now.Add(lease)}
		if hErr := s.SaveHold(ctx, hold); hErr != nil {
			return platformErr(hErr, "Failed to record the test laboratory hold")
		}
		return nil
	})
	if err != nil {
		return TestLabRoom{}, err
	}
	return TestLabRoom{Available: true, Via: result.via}, nil
}

// ExtendTestLab moves the end of a hold when the lease of its laboratory is extended.
func (u *ResourceCalendarUseCase) ExtendTestLab(ctx context.Context, id, owner uuid.UUID, size Amount, until time.Time) error {
	now := u.now().UTC()
	return u.inTx(ctx, func(ctx context.Context, s Store) error {
		holds, err := s.ActiveHolds(ctx, now)
		if err != nil {
			return platformErr(err, "Failed to read the test laboratory holds")
		}
		for _, h := range holds {
			if h.ID == id && h.OwnerID == owner && until.After(h.ExpiresAt) {
				h.ExpiresAt = until
				if err = s.SaveHold(ctx, h); err != nil {
					return platformErr(err, "Failed to extend the test laboratory hold")
				}
			}
		}
		return nil
	})
}

// ReleaseTestLab frees the room of a test laboratory that ended.
func (u *ResourceCalendarUseCase) ReleaseTestLab(ctx context.Context, id uuid.UUID) error {
	if err := u.store.DeleteHold(ctx, id); err != nil {
		return platformErr(err, "Failed to release the test laboratory hold")
	}
	return nil
}

// CheckTestLabRoom answers, without holding anything, whether a test laboratory fits now and, if not, when the
// nearest free window starts (the author's "book a window" offer).
func (u *ResourceCalendarUseCase) CheckTestLabRoom(ctx context.Context, owner uuid.UUID, size, device Amount, lease time.Duration) (TestLabRoom, error) {
	if lease > calModel.MaxBooking {
		return TestLabRoom{}, calModel.ErrBookingInvalid.WithContext("reason", "lease too long").Err()
	}
	now := u.now().UTC()
	lease = u.leaseOf(lease)
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return TestLabRoom{}, err
	}
	var result admission
	err = u.tx.Do(ctx, func(ctx context.Context, s Store) error {
		a, dErr := u.decideTestLab(ctx, s, states, u.sizeWithOverhead(size), device, owner, lease, now)
		result = a
		return dErr
	})
	if err != nil {
		return TestLabRoom{}, err
	}
	if result.via == "" {
		return TestLabRoom{NearestFrom: result.nearest}, nil
	}
	return TestLabRoom{Available: true, Via: result.via}, nil
}

// BookTestLab reserves a window of room for the author's test laboratory (for example 2 hours from HH:MM). The
// booking holds in the calendar like an event reservation. When the window does not fit, the answer carries the
// nearest window that does.
func (u *ResourceCalendarUseCase) BookTestLab(ctx context.Context, owner uuid.UUID, start time.Time, duration time.Duration, size, device Amount) (BookingView, error) {
	now := u.now().UTC()
	w, err := calModel.NewBookingWindow(start, duration, now)
	if err != nil {
		return BookingView{}, err
	}
	size = u.sizeWithOverhead(size)
	states, err := u.agentStates(ctx, now)
	if err != nil {
		return BookingView{}, err
	}
	var booking *calModel.Reservation
	err = u.inTx(ctx, func(ctx context.Context, s Store) error {
		mine, lErr := s.ListOwnedBookings(ctx, owner, now)
		if lErr != nil {
			return platformErr(lErr, "Failed to read the bookings")
		}
		if len(mine) >= maxActiveBookings {
			return calModel.ErrBookingInvalid.WithContext("reason", "too many bookings").Err()
		}
		settings, sErr := s.Settings(ctx)
		if sErr != nil {
			return platformErr(sErr, "Failed to read the calendar settings")
		}
		agents := usedAgents(states)
		rs, rErr := s.ListInWindow(ctx, w)
		if rErr != nil {
			return platformErr(rErr, "Failed to read the reservations")
		}
		if !calModel.FitsWindow(w, agents, rs, settings.TestPool, size, device) {
			later, eErr := s.ListEndingAfter(ctx, now)
			if eErr != nil {
				return platformErr(eErr, "Failed to read the reservations")
			}
			a := admission{}
			if from, ok := calModel.NearestFree(w.Start, duration, u.cfg.SearchHorizon, agents, later, settings.TestPool, size, device); ok {
				a.nearest = &from
			}
			return noRoom(a, duration)
		}
		b, nErr := calModel.NewBooking(owner, w, size, device, now)
		if nErr != nil {
			return nErr
		}
		calModel.PlaceReservation(agents, rs, b)
		if b.Unplaced > 0 {
			return noRoom(admission{}, duration)
		}
		if cErr := s.CreateReservation(ctx, b); cErr != nil {
			return platformErr(cErr, "Failed to save the booking")
		}
		booking = b
		return nil
	})
	if err != nil {
		return BookingView{}, err
	}
	return BookingView{ID: booking.ID, From: booking.Window.Start, To: booking.Window.End, Size: booking.Size}, nil
}

// ListBookings lists the author's bookings that have not ended.
func (u *ResourceCalendarUseCase) ListTestLabBookings(ctx context.Context, owner uuid.UUID) ([]BookingView, error) {
	rs, err := u.store.ListOwnedBookings(ctx, owner, u.now().UTC())
	if err != nil {
		return nil, platformErr(err, "Failed to read the bookings")
	}
	out := make([]BookingView, 0, len(rs))
	for _, r := range rs {
		out = append(out, BookingView{ID: r.ID, From: r.Window.Start, To: r.Window.End, Size: r.Size})
	}
	return out, nil
}

// CancelBooking cancels the author's own booking; its room is free at once.
func (u *ResourceCalendarUseCase) CancelTestLabBooking(ctx context.Context, owner, id uuid.UUID) error {
	now := u.now().UTC()
	return u.inTx(ctx, func(ctx context.Context, s Store) error {
		r, err := s.GetReservation(ctx, id)
		if err != nil || r.Kind != calModel.KindBooking || r.OwnerID == nil || *r.OwnerID != owner || !r.Active() {
			if err == nil || notFound(err) {
				return calModel.ErrBookingNotFound.Err()
			}
			return platformErr(err, "Failed to read the booking")
		}
		r.Cancel(now)
		if _, err = s.UpdateReservation(ctx, r); err != nil {
			return platformErr(err, "Failed to cancel the booking")
		}
		return nil
	})
}
