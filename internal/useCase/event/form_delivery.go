package event

import (
	"context"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	eventFormRepo "github.com/cybericebox/daemon/internal/delivery/repository/eventFormRepo"
	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

// FormDeliveryAssignment is the event-owned delivery rule supplied by the
// persistence adapter. The rule's form version is selected when it is read so
// a retry creates the same idempotent delivery key.
type FormDeliveryAssignment struct {
	ID            uuid.UUID
	FormVersionID uuid.UUID
	Assignment    eventFormModel.Assignment
}

// FormDeliveryRepository is the small persistence seam used by the concrete
// store. Keeping it here lets the signal hook remain testable without making
// the aggregate repository a dependency of the domain reaction.
type FormDeliveryRepository interface {
	ListAssignmentsByTrigger(context.Context, uuid.UUID, eventFormModel.Trigger) ([]eventFormRepo.Assignment, error)
	ListActiveFutureTimedAssignments(context.Context, uuid.UUID) ([]eventFormRepo.Assignment, error)
	LatestByFormID(context.Context, uuid.UUID) (eventFormRepo.Version, error)
	ListRecipientCandidates(context.Context, uuid.UUID) ([]eventFormRepo.RecipientCandidate, error)
	CreateDelivery(context.Context, eventFormRepo.Delivery) (bool, error)
}

type formDeliveryRepositoryStore struct {
	repo FormDeliveryRepository
	now  func() time.Time
}

func NewFormDeliveryRepositoryStore(repo FormDeliveryRepository, now func() time.Time) FormDeliveryStore {
	return &formDeliveryRepositoryStore{repo: repo, now: now}
}

func (s *formDeliveryRepositoryStore) ListFormDeliveryAssignments(ctx context.Context, eventID uuid.UUID, trigger eventFormModel.Trigger) ([]FormDeliveryAssignment, error) {
	rows, err := s.repo.ListAssignmentsByTrigger(ctx, eventID, trigger)
	if err != nil {
		return nil, err
	}
	assignments := make([]FormDeliveryAssignment, 0, len(rows))
	for _, row := range rows {
		version, err := s.repo.LatestByFormID(ctx, row.FormID)
		if err != nil {
			return nil, fmt.Errorf("resolve form %s version: %w", row.FormID, err)
		}
		assignments = append(assignments, FormDeliveryAssignment{ID: row.ID, FormVersionID: version.ID, Assignment: row.Rule})
	}
	return assignments, nil
}

func (s *formDeliveryRepositoryStore) CreateFormDelivery(ctx context.Context, assignment FormDeliveryAssignment, userID uuid.UUID) error {
	_, err := s.repo.CreateDelivery(ctx, eventFormRepo.Delivery{
		FormVersionID: assignment.FormVersionID,
		UserID:        userID,
		AssignmentID:  assignment.ID,
		Presentation:  assignment.Assignment.Presentation,
		Dismissible:   assignment.Assignment.Dismissible,
		Gates:         assignment.Assignment.Gates,
		CreatedAt:     s.now().UTC(),
	})
	return err
}

func (s *formDeliveryRepositoryStore) ResolveFormDeliveryRecipients(ctx context.Context, eventID uuid.UUID, audience eventFormModel.Audience, subject *uuid.UUID) ([]uuid.UUID, error) {
	if audience.Kind == eventFormModel.AudienceSignalSubject {
		if subject == nil || *subject == uuid.Nil {
			return nil, nil
		}
		return []uuid.UUID{*subject}, nil
	}
	candidates, err := s.repo.ListRecipientCandidates(ctx, eventID)
	if err != nil {
		return nil, err
	}
	users := make([]uuid.UUID, 0, len(candidates))
	for _, candidate := range candidates {
		if matchesFormAudience(candidate, audience) {
			users = append(users, candidate.UserID)
		}
	}
	return users, nil
}

func matchesFormAudience(candidate eventFormRepo.RecipientCandidate, audience eventFormModel.Audience) bool {
	switch audience.Kind {
	case eventFormModel.AudienceAllParticipants:
		return true
	case eventFormModel.AudienceAllCaptains:
		return candidate.TeamRole != nil && *candidate.TeamRole == 0
	case eventFormModel.AudienceSelectedUsers:
		return containsUUID(audience.UserIDs, candidate.UserID)
	case eventFormModel.AudienceSelectedTeams:
		return candidate.TeamID != nil && containsUUID(audience.TeamIDs, *candidate.TeamID)
	case eventFormModel.AudienceParticipantsNoTeam:
		return candidate.TeamID == nil
	case eventFormModel.AudienceTeamsBelowSize:
		return candidate.TeamID != nil && audience.BelowSize != nil && candidate.TeamMemberCount < *audience.BelowSize
	default:
		return false
	}
}

func containsUUID(values []uuid.UUID, target uuid.UUID) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

// FormDeliveryStore is deliberately narrower than the event repository: signal
// handling only needs assignments in one event/trigger scope and an idempotent
// per-user delivery write.
type FormDeliveryStore interface {
	ListFormDeliveryAssignments(context.Context, uuid.UUID, eventFormModel.Trigger) ([]FormDeliveryAssignment, error)
	ListActiveFutureTimedFormDeliveryAssignments(context.Context, uuid.UUID) ([]FormDeliveryAssignment, error)
	ResolveFormDeliveryRecipients(context.Context, uuid.UUID, eventFormModel.Audience, *uuid.UUID) ([]uuid.UUID, error)
	CreateFormDelivery(context.Context, FormDeliveryAssignment, uuid.UUID) error
}

func (s *formDeliveryRepositoryStore) ListActiveFutureTimedFormDeliveryAssignments(ctx context.Context, eventID uuid.UUID) ([]FormDeliveryAssignment, error) {
	rows, err := s.repo.ListActiveFutureTimedAssignments(ctx, eventID)
	if err != nil {
		return nil, err
	}
	assignments := make([]FormDeliveryAssignment, 0, len(rows))
	for _, row := range rows {
		version, err := s.repo.LatestByFormID(ctx, row.FormID)
		if err != nil {
			return nil, fmt.Errorf("resolve form %s version: %w", row.FormID, err)
		}
		assignments = append(assignments, FormDeliveryAssignment{ID: row.ID, FormVersionID: version.ID, Assignment: row.Rule})
	}
	return assignments, nil
}

// FormDeliveryHook creates form deliveries from durable participant signals.
// It does not reuse notification planning: form audiences and form completion
// are independent from notification subscriptions.
type FormDeliveryHook struct {
	store    FormDeliveryStore
	payloads *signalModel.Registry
}

func NewFormDeliveryHook(store FormDeliveryStore, payloads *signalModel.Registry) *FormDeliveryHook {
	return &FormDeliveryHook{store: store, payloads: payloads}
}

func (h *FormDeliveryHook) Name() string { return "form-delivery" }

func (h *FormDeliveryHook) Handle(ctx context.Context, signal signalModel.Signal) error {
	if signal.Type == signalModel.TypeParticipantEnrolled {
		return h.deliverActiveFutureTimedAssignments(ctx, signal)
	}
	trigger, ok := formTriggerForSignal(signal.Type)
	if !ok {
		return nil
	}
	payload, err := h.payloads.Decode(signal.Type, signal.Payload)
	if err != nil {
		return fmt.Errorf("form delivery: decode signal: %w", err)
	}
	routing := payload.Routing()
	if routing.ScopeEventID == uuid.Nil || routing.SubjectUserID == nil || *routing.SubjectUserID == uuid.Nil {
		return nil
	}
	assignments, err := h.store.ListFormDeliveryAssignments(ctx, routing.ScopeEventID, trigger)
	if err != nil {
		return fmt.Errorf("form delivery: list assignments: %w", err)
	}
	for _, assignment := range assignments {
		if err := MaterializeFormDeliveries(ctx, h.store, routing.ScopeEventID, assignment, routing.SubjectUserID); err != nil {
			return err
		}
	}
	return nil
}

func (h *FormDeliveryHook) deliverActiveFutureTimedAssignments(ctx context.Context, signal signalModel.Signal) error {
	payload, err := h.payloads.Decode(signal.Type, signal.Payload)
	if err != nil {
		return fmt.Errorf("form delivery: decode enrollment signal: %w", err)
	}
	routing := payload.Routing()
	if routing.ScopeEventID == uuid.Nil || routing.SubjectUserID == nil || *routing.SubjectUserID == uuid.Nil {
		return nil
	}
	assignments, err := h.store.ListActiveFutureTimedFormDeliveryAssignments(ctx, routing.ScopeEventID)
	if err != nil {
		return fmt.Errorf("form delivery: list active future timed assignments: %w", err)
	}
	for _, assignment := range assignments {
		if err := MaterializeFormDeliveryForSubject(ctx, h.store, routing.ScopeEventID, assignment, *routing.SubjectUserID); err != nil {
			return err
		}
	}
	return nil
}

// MaterializeFormDeliveries resolves the assignment audience and creates one
// idempotent, individual delivery per recipient. It is shared by durable
// signal hooks, the timed worker, and manual manager commands.
func MaterializeFormDeliveries(ctx context.Context, store FormDeliveryStore, eventID uuid.UUID, assignment FormDeliveryAssignment, subject *uuid.UUID) error {
	if err := assignment.Assignment.Validate(); err != nil {
		return fmt.Errorf("form delivery: invalid assignment: %w", err)
	}
	recipients, err := store.ResolveFormDeliveryRecipients(ctx, eventID, assignment.Assignment.Audience, subject)
	if err != nil {
		return fmt.Errorf("form delivery: resolve recipients: %w", err)
	}
	for _, recipient := range recipients {
		if err = store.CreateFormDelivery(ctx, assignment, recipient); err != nil {
			return fmt.Errorf("form delivery: create delivery: %w", err)
		}
	}
	return nil
}

// MaterializeFormDeliveryForSubject is used for a previously materialized
// timed rule with include_future_participants. It evaluates the same audience
// selector but only creates a delivery for the just-enrolled subject.
func MaterializeFormDeliveryForSubject(ctx context.Context, store FormDeliveryStore, eventID uuid.UUID, assignment FormDeliveryAssignment, subject uuid.UUID) error {
	if err := assignment.Assignment.Validate(); err != nil {
		return fmt.Errorf("form delivery: invalid assignment: %w", err)
	}
	recipients, err := store.ResolveFormDeliveryRecipients(ctx, eventID, assignment.Assignment.Audience, &subject)
	if err != nil {
		return fmt.Errorf("form delivery: resolve recipients: %w", err)
	}
	for _, recipient := range recipients {
		if recipient == subject {
			if err = store.CreateFormDelivery(ctx, assignment, recipient); err != nil {
				return fmt.Errorf("form delivery: create delivery: %w", err)
			}
			return nil
		}
	}
	return nil
}

func formTriggerForSignal(typ signalModel.Type) (eventFormModel.Trigger, bool) {
	switch typ {
	case signalModel.TypeParticipantOpenRegistrationCompleted:
		return eventFormModel.TriggerRegistrationOpenCompleted, true
	case signalModel.TypeParticipantApprovalRegistrationSubmitted:
		return eventFormModel.TriggerRegistrationApprovalSubmitted, true
	default:
		return "", false
	}
}
