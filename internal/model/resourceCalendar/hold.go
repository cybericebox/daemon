package resourceCalendar

import (
	"time"

	"github.com/gofrs/uuid"
)

// How a test laboratory was admitted.
const (
	// ViaPool: inside the guaranteed minimum pool.
	ViaPool = "pool"
	// ViaFree: above the pool, from room no event has reserved.
	ViaFree = "free"
	// ViaBooking: inside the author's booked window.
	ViaBooking = "booking"
)

// TestLabHold is the room a running test laboratory was admitted with, until its lease ends.
type TestLabHold struct {
	ID            uuid.UUID
	OwnerID       uuid.UUID
	Via           string
	ReservationID *uuid.UUID
	Size          Amount
	StartsAt      time.Time
	ExpiresAt     time.Time
}

// NamedChangeRequest is a change request with the name of its event, for the admin list.
type NamedChangeRequest struct {
	ChangeRequest
	EventName string
	EventTag  string
}

// NamedAlarm is an alarm with the name of its event, for the admin list.
type NamedAlarm struct {
	Alarm
	EventName string
	EventTag  string
}

// Label names the event of a reservation.
type Label struct {
	EventName string
	EventTag  string
}
