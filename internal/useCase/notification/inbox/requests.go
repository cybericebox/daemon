package inboxUseCase

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/inboxRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// Notifier is the one-recipient dispatch port (the notification dispatcher).
type Notifier interface {
	Notify(context.Context, uuid.UUID, notificationTypes.NotificationPayload, ...dispatchModel.NotifyOption) error
}

// RequestQueries is the data port of the request router; *postgres.Queries
// satisfies it structurally.
type RequestQueries interface {
	inboxRepo.Queries
	ListEventWriteManagerUserIDs(ctx context.Context, eventID uuid.UUID) ([]uuid.UUID, error)
	ListPlatformAdminUserIDs(ctx context.Context) ([]uuid.UUID, error)
	ListSuperAdminUserIDs(ctx context.Context) ([]uuid.UUID, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (postgres.User, error)
}

// RequestRouter owns the inbox requests that are not a plain signal delivery:
// it fans a request out to every person who may act on it (the Event managers
// with write access, all platform admins) and resolves every copy once the
// object is decided. As a signal hook it handles the registration application signals;
// the exercise and stand use cases call it directly.
type RequestRouter struct {
	queries  RequestQueries
	inbox    *inboxRepo.Repository
	dispatch Notifier
}

func NewRequestRouter(queries RequestQueries, dispatch Notifier) *RequestRouter {
	return &RequestRouter{queries: queries, inbox: inboxRepo.New(queries), dispatch: dispatch}
}

func (r *RequestRouter) Name() string { return "inbox-requests" }

// Signals are the exact hook registrations of the router.
func (r *RequestRouter) Signals() []signalModel.Type {
	return []signalModel.Type{
		signalModel.TypeParticipantApprovalRegistrationSubmitted,
		signalModel.TypeParticipantApprovalRegistrationApproved,
		signalModel.TypeParticipantApprovalRegistrationRejected,
		signalModel.TypeParticipantEventFinished,
	}
}

// Handle turns a new application into a request for every Event manager who
// may act on it, closes it for all of them on approval or rejection, and
// expires the Event's undecided applications when it finishes.
func (r *RequestRouter) Handle(ctx context.Context, signal signalModel.Signal) error {
	if signal.Type == signalModel.TypeParticipantEventFinished {
		var notice signalModel.EventNoticePayload
		if err := json.Unmarshal(signal.Payload, &notice); err != nil {
			return fmt.Errorf("inbox requests: decode signal: %w", err)
		}
		return r.resolvePattern(ctx, inboxModel.EventApplicationsPattern(notice.ScopeEventID), inboxModel.ResolutionExpired)
	}
	var payload signalModel.ParticipantPayload
	if err := json.Unmarshal(signal.Payload, &payload); err != nil {
		return fmt.Errorf("inbox requests: decode signal: %w", err)
	}
	ref := inboxModel.ApplicationRef(payload.ScopeEventID, payload.SubjectUserID)
	switch signal.Type {
	case signalModel.TypeParticipantApprovalRegistrationSubmitted:
		return r.applicationSubmitted(ctx, payload, ref, signal.OccurredAt)
	case signalModel.TypeParticipantApprovalRegistrationApproved:
		return r.resolve(ctx, ref, inboxModel.ResolutionApproved, payload.ActorUserID)
	case signalModel.TypeParticipantApprovalRegistrationRejected:
		return r.resolve(ctx, ref, inboxModel.ResolutionRejected, payload.ActorUserID)
	}
	return nil
}

func (r *RequestRouter) applicationSubmitted(ctx context.Context, p signalModel.ParticipantPayload, ref string, raisedAt time.Time) error {
	managers, err := r.queries.ListEventWriteManagerUserIDs(ctx, p.ScopeEventID)
	if err != nil {
		return fmt.Errorf("inbox requests: list event managers: %w", err)
	}
	applicant, err := r.profile(ctx, p.SubjectUserID)
	if err != nil {
		return err
	}
	vars := map[string]any{
		"scope_event_id":    p.ScopeEventID.String(),
		"event_tag":         p.EventTag,
		"event_name":        p.EventName,
		"applicant_user_id": p.SubjectUserID.String(),
		"applicant_name":    displayName(applicant),
		"applicant_email":   applicant.Email,
	}
	meta := inboxModel.NewMeta(inboxModel.TypeApplicationSubmitted, inboxModel.RoleManager, ref).Raised(raisedAt)
	return r.fanOut(ctx, excluding(managers, p.SubjectUserID), inboxModel.TypeApplicationSubmitted, vars, meta,
		dispatchModel.WithEventScope(p.ScopeEventID))
}

// Proposal is the catalog proposal snapshot the exercise use case reports.
type Proposal struct {
	ID                uuid.UUID
	ExerciseID        uuid.UUID
	ExerciseName      string
	ProposedBy        uuid.UUID
	ProposedAt        time.Time
	Note              string
	DecisionNote      string
	CatalogExerciseID *uuid.UUID
}

// ProposalSubmitted asks every platform admin (but the proposer) to review
// the proposal.
func (r *RequestRouter) ProposalSubmitted(ctx context.Context, p Proposal) error {
	admins, err := r.queries.ListPlatformAdminUserIDs(ctx)
	if err != nil {
		return fmt.Errorf("inbox requests: list platform admins: %w", err)
	}
	vars, err := r.proposalVars(ctx, p)
	if err != nil {
		return err
	}
	meta := inboxModel.NewMeta(inboxModel.TypeProposalSubmitted, inboxModel.RoleAdmin, inboxModel.ProposalRef(p.ID)).Raised(p.ProposedAt)
	return r.fanOut(ctx, excluding(admins, p.ProposedBy), inboxModel.TypeProposalSubmitted, vars, meta)
}

// ProposalDecided closes the admins' request and tells the proposer the
// outcome.
func (r *RequestRouter) ProposalDecided(ctx context.Context, p Proposal, approved bool, by uuid.UUID) error {
	resolution, typ := inboxModel.ResolutionRejected, inboxModel.TypeProposalRejected
	if approved {
		resolution, typ = inboxModel.ResolutionApproved, inboxModel.TypeProposalApproved
	}
	if err := r.resolve(ctx, inboxModel.ProposalRef(p.ID), resolution, by); err != nil {
		return err
	}
	if p.ProposedBy == uuid.Nil {
		return nil
	}
	vars, err := r.proposalVars(ctx, p)
	if err != nil {
		return err
	}
	return r.fanOut(ctx, []uuid.UUID{p.ProposedBy}, typ, vars, inboxModel.NewMeta(typ, inboxModel.RoleSubject, ""))
}

// Elevation is the resource elevation request snapshot the exercise use case reports.
type Elevation struct {
	ID           uuid.UUID
	ExerciseID   uuid.UUID
	ExerciseName string
	RequestedBy  uuid.UUID
	RequestedAt  time.Time
	Reason       string
	DecisionNote string
	// Devices is the devices with their values as text ("db: 500m / 2Gi, web: 250m / 1Gi").
	Devices string
}

// ElevationRequested asks every super admin (but the author) to decide the request.
func (r *RequestRouter) ElevationRequested(ctx context.Context, e Elevation) error {
	// Infrastructure is decided by super admins only.
	admins, err := r.queries.ListSuperAdminUserIDs(ctx)
	if err != nil {
		return fmt.Errorf("inbox requests: list super admins: %w", err)
	}
	vars, err := r.elevationVars(ctx, e)
	if err != nil {
		return err
	}
	meta := inboxModel.NewMeta(inboxModel.TypeElevationRequested, inboxModel.RoleAdmin, inboxModel.ElevationRef(e.ID)).Raised(e.RequestedAt)
	return r.fanOut(ctx, excluding(admins, e.RequestedBy), inboxModel.TypeElevationRequested, vars, meta)
}

// ElevationDecided closes the admins' request and tells the author the outcome.
func (r *RequestRouter) ElevationDecided(ctx context.Context, e Elevation, approved bool, by uuid.UUID) error {
	resolution, typ := inboxModel.ResolutionRejected, inboxModel.TypeElevationRejected
	if approved {
		resolution, typ = inboxModel.ResolutionApproved, inboxModel.TypeElevationApproved
	}
	if err := r.resolve(ctx, inboxModel.ElevationRef(e.ID), resolution, by); err != nil {
		return err
	}
	if e.RequestedBy == uuid.Nil {
		return nil
	}
	vars, err := r.elevationVars(ctx, e)
	if err != nil {
		return err
	}
	return r.fanOut(ctx, []uuid.UUID{e.RequestedBy}, typ, vars, inboxModel.NewMeta(typ, inboxModel.RoleSubject, ""))
}

func (r *RequestRouter) elevationVars(ctx context.Context, e Elevation) (map[string]any, error) {
	vars := map[string]any{
		"elevation_id":   e.ID.String(),
		"exercise_id":    e.ExerciseID.String(),
		"exercise_name":  e.ExerciseName,
		"reason":         e.Reason,
		"decision_note":  e.DecisionNote,
		"devices":        e.Devices,
		"requester_name": "",
	}
	if e.RequestedBy != uuid.Nil {
		requester, err := r.profile(ctx, e.RequestedBy)
		if err != nil {
			return nil, err
		}
		vars["requester_name"] = displayName(requester)
	}
	return vars, nil
}

// StandRecreated closes the failed-laboratory request of the team for every
// recipient: a moderator re-created the stand. A later automatic recovery
// does not close it.
func (r *RequestRouter) StandRecreated(ctx context.Context, eventID, teamID, by uuid.UUID) error {
	return r.resolve(ctx, inboxModel.StandRef(eventID, teamID), inboxModel.ResolutionFixed, by)
}

// UserDeleted withdraws the undecided applications of a deleted account.
func (r *RequestRouter) UserDeleted(ctx context.Context, userID uuid.UUID) error {
	return r.resolvePattern(ctx, inboxModel.UserApplicationsPattern(userID), inboxModel.ResolutionWithdrawn)
}

func (r *RequestRouter) proposalVars(ctx context.Context, p Proposal) (map[string]any, error) {
	vars := map[string]any{
		"proposal_id":         p.ID.String(),
		"exercise_id":         p.ExerciseID.String(),
		"exercise_name":       p.ExerciseName,
		"note":                p.Note,
		"decision_note":       p.DecisionNote,
		"proposer_name":       "",
		"catalog_exercise_id": "",
	}
	if p.CatalogExerciseID != nil {
		vars["catalog_exercise_id"] = p.CatalogExerciseID.String()
	}
	if p.ProposedBy != uuid.Nil {
		proposer, err := r.profile(ctx, p.ProposedBy)
		if err != nil {
			return nil, err
		}
		vars["proposer_name"] = displayName(proposer)
	}
	return vars, nil
}

// fanOut sends one in-app notification per recipient, each with its own
// recipient variables and the shared inbox classification.
func (r *RequestRouter) fanOut(ctx context.Context, recipients []uuid.UUID, typ string, vars map[string]any, meta inboxModel.Meta, opts ...dispatchModel.NotifyOption) error {
	for _, recipient := range recipients {
		profile, err := r.profile(ctx, recipient)
		if err != nil {
			return err
		}
		personal := make(map[string]any, len(vars)+6)
		for k, v := range vars {
			personal[k] = v
		}
		personal["user_id"] = profile.ID.String()
		personal["user_email"] = profile.Email
		personal["user_first_name"] = profile.FirstName
		personal["user_last_name"] = profile.LastName
		personal["user_picture"] = profile.Picture
		personal["user_name"] = strings.TrimSpace(profile.FirstName + " " + profile.LastName)
		payload := notificationPayloads.DefaultPayload{
			Type:      notificationTypes.NotificationType(typ),
			Channels:  []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelInApp},
			Variables: personal,
		}
		all := append([]dispatchModel.NotifyOption{
			dispatchModel.WithOverrideChannels(notificationTypes.NotificationChannelInApp),
			dispatchModel.WithInbox(meta),
		}, opts...)
		if err = r.dispatch.Notify(ctx, recipient, payload, all...); err != nil {
			return fmt.Errorf("inbox requests: notify %s: %w", typ, err)
		}
	}
	return nil
}

func (r *RequestRouter) resolve(ctx context.Context, ref string, resolution inboxModel.Resolution, by uuid.UUID) error {
	var resolvedBy *uuid.UUID
	if by != uuid.Nil {
		resolvedBy = &by
	}
	if _, err := r.inbox.ResolveBySubject(ctx, ref, resolution, resolvedBy); err != nil {
		return fmt.Errorf("inbox requests: resolve %s: %w", ref, err)
	}
	return nil
}

func (r *RequestRouter) resolvePattern(ctx context.Context, pattern string, resolution inboxModel.Resolution) error {
	if _, err := r.inbox.ResolveByPattern(ctx, pattern, resolution); err != nil {
		return fmt.Errorf("inbox requests: resolve %s: %w", pattern, err)
	}
	return nil
}

func (r *RequestRouter) profile(ctx context.Context, id uuid.UUID) (userModel.NotificationProfile, error) {
	user, err := r.queries.GetUserByID(ctx, id)
	if err != nil {
		return userModel.NotificationProfile{}, fmt.Errorf("inbox requests: load user profile: %w", err)
	}
	return userRepo.ToDomain(user).NotificationProfile(), nil
}

func displayName(p userModel.NotificationProfile) string {
	if name := strings.TrimSpace(p.FirstName + " " + p.LastName); name != "" {
		return name
	}
	return p.Email
}

func excluding(ids []uuid.UUID, skip uuid.UUID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if id != skip {
			out = append(out, id)
		}
	}
	return out
}
