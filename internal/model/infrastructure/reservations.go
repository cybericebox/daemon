package infrastructure

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"

	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// ClusterReservation is a reservation as an agent holds it: one part of a backend reservation, by its part id.
type ClusterReservation struct {
	ID       uuid.UUID
	From, To time.Time
	Amount   resourcesModel.Amount
	// Status is what the agent reports; nil in a desired state.
	Status *ClusterReservationStatus
}

// ClusterReservationStatus is the agent's report about a reservation: its phase, the ending notice and the usage.
type ClusterReservationStatus struct {
	// Phase is Pending, Active, Ending or Ended.
	Phase string
	// NoticeAt is when the ending notice was given; EndsAt when the groups under the reservation are deleted.
	NoticeAt, EndsAt *time.Time
	// EndingReason is WindowClosing, TenantGone, AmountReduced or empty.
	EndingReason string
	Used         resourcesModel.Amount
	Groups       int
	Exceeded     bool
	OverAmount   bool
	Invalid      bool
	ObservedAt   time.Time
}

// Refusal reasons the agent gives for an item (ItemResult.reason).
const (
	ReasonReservationRequired = "RESERVATION_REQUIRED"
	ReasonReservationNotFound = "RESERVATION_NOT_FOUND"
	ReasonReservationEnded    = "RESERVATION_ENDED"
	ReasonReservationFull     = "RESERVATION_FULL"
	ReasonReservationMismatch = "RESERVATION_MISMATCH"
	ReasonOverQuota           = "OVER_QUOTA"
)

// The agent's refusals of an item, by reason.
var (
	ErrReservationRequired  = errors.New("reservation required")
	ErrReservationNotFound  = errors.New("reservation not found")
	ErrReservationEnded     = errors.New("reservation ended")
	ErrReservationFull      = errors.New("not enough room in this reservation")
	ErrReservationMismatch  = errors.New("deploy group under another reservation")
	ErrReservationOverQuota = errors.New("reservations over the tenant quota")
	// ErrNoClusterReservation: the part a group or lab must run under is not on its agent yet (retryable).
	ErrNoClusterReservation = errors.New("the cluster reservation is not ready")
	// ErrAgentIncompatible: the agent has no reservation API; it must be upgraded.
	ErrAgentIncompatible = errors.New("agent has no reservation API; upgrade laboratory")
)

// ReasonError maps a refusal reason onto its error; an unknown or empty reason is nil.
func ReasonError(reason string) error {
	switch reason {
	case ReasonReservationRequired:
		return ErrReservationRequired
	case ReasonReservationNotFound:
		return ErrReservationNotFound
	case ReasonReservationEnded:
		return ErrReservationEnded
	case ReasonReservationFull:
		return ErrReservationFull
	case ReasonReservationMismatch:
		return ErrReservationMismatch
	case ReasonOverQuota:
		return ErrReservationOverQuota
	}
	return nil
}

// ItemOutcome is the agent's answer for one reservation of a batch.
type ItemOutcome struct {
	ID uuid.UUID
	// OK: the agent holds the desired state (created, exists, updated, deleted, or not found for a delete).
	OK bool
	// Reason is the machine code of a refusal (OVER_QUOTA, ...); Err the refusal itself.
	Reason    string
	Err       error
	Retryable bool
}

// ReservationPort is the agents' reservation API, addressed by agent.
type ReservationPort interface {
	// ListReservations lists the reservations of this platform instance on the agent.
	ListReservations(ctx context.Context, agentID uuid.UUID) ([]ClusterReservation, error)
	CreateReservations(ctx context.Context, agentID uuid.UUID, rs []ClusterReservation) ([]ItemOutcome, error)
	UpdateReservations(ctx context.Context, agentID uuid.UUID, rs []ClusterReservation) ([]ItemOutcome, error)
	DeleteReservations(ctx context.Context, agentID uuid.UUID, ids []uuid.UUID) ([]ItemOutcome, error)
}

// ErrPlacementMismatch: a group is placed on another agent than its reservation's unit (it never moves).
var ErrPlacementMismatch = errors.New("the group is placed on another agent than its reservation")

// ReservationRef names the backend reservation a group or a lab runs under; the calendar resolves it to the part on
// the group's agent. One of the forms is set:
//   - a calendar reservation (CalendarID, or the active reservation of EventID) and the Unit inside it (a team, a
//     preview owner, the booking);
//   - a test run (TestDeployID);
//   - an author's test group (OwnerID).
type ReservationRef struct {
	CalendarID   *uuid.UUID
	EventID      *uuid.UUID
	Unit         string
	TestDeployID *uuid.UUID
	OwnerID      *uuid.UUID
}

// Empty is true when the ref names no reservation.
func (r ReservationRef) Empty() bool {
	return r.CalendarID == nil && r.EventID == nil && r.TestDeployID == nil && r.OwnerID == nil
}

// PartResolver finds the cluster reservation a ref runs under.
type PartResolver interface {
	// Resolve returns the agent of the ref (assigning its unit when needed) and the part id there. pinned is the
	// agent the group already lives on, when it has one. A part that is not on its agent yet is sent once; still
	// missing is ErrNoClusterReservation (retryable).
	Resolve(ctx context.Context, ref ReservationRef, pinned *uuid.UUID) (agentID, partID uuid.UUID, err error)
}

type groupReservationKey struct{}

// WithGroupReservation carries the reservation of the group a call creates (EnsureVPNGroup, the preparation check).
func WithGroupReservation(ctx context.Context, ref ReservationRef) context.Context {
	return context.WithValue(ctx, groupReservationKey{}, ref)
}

// GroupReservationFrom returns what WithGroupReservation stored; false when none.
func GroupReservationFrom(ctx context.Context) (ReservationRef, bool) {
	ref, ok := ctx.Value(groupReservationKey{}).(ReservationRef)
	return ref, ok && !ref.Empty()
}
