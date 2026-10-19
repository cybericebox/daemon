package inboxUseCase

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// ReservationNotice is an event reservation part people hear about: it ends while the event runs, or it was made
// smaller than what runs in it. The router is the calendar's ReservationNotifier and the exercise use case's
// TestLabNotifier.
type ReservationNotice struct {
	ReservationID uuid.UUID
	EventID       uuid.UUID
	EventName     string
	EventTag      string
	AgentID       uuid.UUID
	EndsAt        time.Time
}

// kyiv is the zone the times of these notices are written in.
var kyiv = func() *time.Location {
	loc, err := time.LoadLocation("Europe/Kyiv")
	if err != nil {
		return time.UTC
	}
	return loc
}()

func clock(t time.Time) string { return t.In(kyiv).Format("15:04") }

func reservationVars(n ReservationNotice) map[string]any {
	return map[string]any{"event_id": n.EventID.String(), "event_name": n.EventName, "event_tag": n.EventTag, "ends_at": clock(n.EndsAt)}
}

// ReservationEnding tells the super admins and the event's organizers that its reservation ends while the event
// still runs.
func (r *RequestRouter) ReservationEnding(ctx context.Context, n ReservationNotice) error {
	admins, err := r.queries.ListSuperAdminUserIDs(ctx)
	if err != nil {
		return fmt.Errorf("inbox: list super admins: %w", err)
	}
	organizers, err := r.queries.ListEventWriteManagerUserIDs(ctx, n.EventID)
	if err != nil {
		return fmt.Errorf("inbox: list event organizers: %w", err)
	}
	meta := inboxModel.NewMeta(inboxModel.TypeReservationEnding, inboxModel.RoleSubject, "")
	return r.fanOut(ctx, union(admins, organizers), inboxModel.TypeReservationEnding, reservationVars(n), meta)
}

// ReservationReduced tells the super admins that a reservation was made smaller than what runs in it.
func (r *RequestRouter) ReservationReduced(ctx context.Context, n ReservationNotice) error {
	admins, err := r.queries.ListSuperAdminUserIDs(ctx)
	if err != nil {
		return fmt.Errorf("inbox: list super admins: %w", err)
	}
	meta := inboxModel.NewMeta(inboxModel.TypeReservationReduced, inboxModel.RoleSubject, "")
	return r.fanOut(ctx, admins, inboxModel.TypeReservationReduced, reservationVars(n), meta)
}

// TestLabEnding tells the author that the test laboratory gets deleted at endsAt unless extended.
func (r *RequestRouter) TestLabEnding(ctx context.Context, owner, deployID uuid.UUID, exerciseName string, endsAt time.Time) error {
	vars := map[string]any{"deploy_id": deployID.String(), "exercise_name": exerciseName, "ends_at": clock(endsAt)}
	return r.fanOut(ctx, []uuid.UUID{owner}, inboxModel.TypeTestLabEnding, vars, inboxModel.NewMeta(inboxModel.TypeTestLabEnding, inboxModel.RoleSubject, ""))
}

// TestLabEnded tells the author that the test laboratory's time is over and it was deleted.
func (r *RequestRouter) TestLabEnded(ctx context.Context, owner, deployID uuid.UUID, exerciseName string) error {
	vars := map[string]any{"deploy_id": deployID.String(), "exercise_name": exerciseName, "ends_at": ""}
	return r.fanOut(ctx, []uuid.UUID{owner}, inboxModel.TypeTestLabEnded, vars, inboxModel.NewMeta(inboxModel.TypeTestLabEnded, inboxModel.RoleSubject, ""))
}

// union lists every id once, in order.
func union(lists ...[]uuid.UUID) []uuid.UUID {
	seen := map[uuid.UUID]bool{}
	var out []uuid.UUID
	for _, list := range lists {
		for _, id := range list {
			if !seen[id] {
				seen[id] = true
				out = append(out, id)
			}
		}
	}
	return out
}
