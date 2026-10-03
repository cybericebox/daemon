package inboxUseCase

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/gofrs/uuid"

	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
)

// ResourceChange is the resource change request snapshot the calendar reports.
type ResourceChange struct {
	ID           uuid.UUID
	EventID      uuid.UUID
	EventName    string
	EventTag     string
	RequestedBy  uuid.UUID
	RequestedAt  time.Time
	Reason       string
	DecisionNote string
	// Summary is what was asked for as text ("size 4 / 8Gi; until 2026-10-05 18:00 UTC").
	Summary string
}

// ResourceAlarm is the readiness alarm snapshot the calendar reports.
type ResourceAlarm struct {
	ID        uuid.UUID
	Kind      string
	EventID   uuid.UUID
	EventName string
	EventTag  string
	AgentName string
	Units     int
	// Stage is how far the escalation went; Escalated says this report is a new stage of a known alarm.
	Stage     int
	Escalated bool
	RaisedAt  time.Time
}

func (r *RequestRouter) resourceChangeVars(ctx context.Context, c ResourceChange) (map[string]any, error) {
	vars := map[string]any{
		"change_id": c.ID.String(), "event_id": c.EventID.String(), "event_name": c.EventName, "event_tag": c.EventTag,
		"reason": c.Reason, "decision_note": c.DecisionNote, "summary": c.Summary, "requester_name": "",
	}
	if c.RequestedBy != uuid.Nil {
		requester, err := r.profile(ctx, c.RequestedBy)
		if err != nil {
			return nil, err
		}
		vars["requester_name"] = displayName(requester)
	}
	return vars, nil
}

// ResourceChangeRequested asks every super admin (but the organizer) to decide the request.
func (r *RequestRouter) ResourceChangeRequested(ctx context.Context, c ResourceChange) error {
	// Infrastructure is decided by super admins only.
	admins, err := r.queries.ListSuperAdminUserIDs(ctx)
	if err != nil {
		return fmt.Errorf("inbox requests: list super admins: %w", err)
	}
	vars, err := r.resourceChangeVars(ctx, c)
	if err != nil {
		return err
	}
	meta := inboxModel.NewMeta(inboxModel.TypeResourceChangeRequested, inboxModel.RoleAdmin, inboxModel.ResourceChangeRef(c.ID)).Raised(c.RequestedAt)
	return r.fanOut(ctx, excluding(admins, c.RequestedBy), inboxModel.TypeResourceChangeRequested, vars, meta)
}

// ResourceChangeDecided closes the admins' request and tells the organizer the outcome.
func (r *RequestRouter) ResourceChangeDecided(ctx context.Context, c ResourceChange, approved bool, by uuid.UUID) error {
	resolution, typ := inboxModel.ResolutionRejected, inboxModel.TypeResourceChangeRejected
	if approved {
		resolution, typ = inboxModel.ResolutionApproved, inboxModel.TypeResourceChangeApproved
	}
	if err := r.resolve(ctx, inboxModel.ResourceChangeRef(c.ID), resolution, by); err != nil {
		return err
	}
	if c.RequestedBy == uuid.Nil {
		return nil
	}
	vars, err := r.resourceChangeVars(ctx, c)
	if err != nil {
		return err
	}
	return r.fanOut(ctx, []uuid.UUID{c.RequestedBy}, typ, vars, inboxModel.NewMeta(typ, inboxModel.RoleSubject, ""))
}

// ResourceAlarmRaised tells every super admin that a reservation cannot be served as promised. A new stage of
// a known alarm is sent again (the escalation at the deploy lead).
func (r *RequestRouter) ResourceAlarmRaised(ctx context.Context, a ResourceAlarm) error {
	// Infrastructure is decided by super admins only.
	admins, err := r.queries.ListSuperAdminUserIDs(ctx)
	if err != nil {
		return fmt.Errorf("inbox requests: list super admins: %w", err)
	}
	if a.Escalated {
		// The old copy is closed so one request stays per alarm.
		if err = r.resolve(ctx, inboxModel.ResourceAlarmRef(a.ID), inboxModel.ResolutionResolved, uuid.Nil); err != nil {
			return err
		}
	}
	vars := map[string]any{
		"alarm_id": a.ID.String(), "alarm_kind": a.Kind, "event_id": a.EventID.String(), "event_name": a.EventName, "event_tag": a.EventTag,
		"agent_name": a.AgentName, "units": strconv.Itoa(a.Units),
	}
	meta := inboxModel.NewMeta(inboxModel.TypeResourceAlarmRaised, inboxModel.RoleAdmin, inboxModel.ResourceAlarmRef(a.ID)).Raised(a.RaisedAt)
	return r.fanOut(ctx, admins, inboxModel.TypeResourceAlarmRaised, vars, meta)
}

// ResourceAlarmClosed closes the admins' requests of an alarm that was resolved or acknowledged.
func (r *RequestRouter) ResourceAlarmClosed(ctx context.Context, alarmID uuid.UUID, by uuid.UUID) error {
	return r.resolve(ctx, inboxModel.ResourceAlarmRef(alarmID), inboxModel.ResolutionResolved, by)
}
