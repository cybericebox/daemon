package event_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventFormModel "github.com/cybericebox/daemon/internal/model/eventForm"
	signalModel "github.com/cybericebox/daemon/internal/model/signal"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type formDeliveryStore struct {
	assignment eventUseCase.FormDeliveryAssignment
	delivered  map[uuid.UUID]int
	recipients []uuid.UUID
}

func (s *formDeliveryStore) ListFormDeliveryAssignments(_ context.Context, _ uuid.UUID, _ eventFormModel.Trigger) ([]eventUseCase.FormDeliveryAssignment, error) {
	return []eventUseCase.FormDeliveryAssignment{s.assignment}, nil
}
func (s *formDeliveryStore) ListActiveFutureTimedFormDeliveryAssignments(_ context.Context, _ uuid.UUID) ([]eventUseCase.FormDeliveryAssignment, error) {
	return []eventUseCase.FormDeliveryAssignment{s.assignment}, nil
}
func (s *formDeliveryStore) CreateFormDelivery(_ context.Context, _ eventUseCase.FormDeliveryAssignment, userID uuid.UUID) error {
	s.delivered[userID]++
	return nil
}
func (s *formDeliveryStore) ResolveFormDeliveryRecipients(_ context.Context, _ uuid.UUID, audience eventFormModel.Audience, subject *uuid.UUID) ([]uuid.UUID, error) {
	if audience.Kind == eventFormModel.AudienceSignalSubject && subject != nil {
		return []uuid.UUID{*subject}, nil
	}
	return s.recipients, nil
}
func mustSignalPayload(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal signal payload: %v", err)
	}
	return payload
}

func TestCaptainAudienceCreatesSeparateUserDeliveries(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	firstCaptain := uuid.Must(uuid.NewV7())
	secondCaptain := uuid.Must(uuid.NewV7())
	assignment := eventFormModel.Assignment{
		Trigger:      eventFormModel.TriggerRegistrationOpenCompleted,
		Audience:     eventFormModel.Audience{Kind: eventFormModel.AudienceAllCaptains},
		Presentation: eventFormModel.PresentationBanner,
	}
	store := &formDeliveryStore{
		assignment: eventUseCase.FormDeliveryAssignment{Assignment: assignment},
		delivered:  map[uuid.UUID]int{},
		recipients: []uuid.UUID{firstCaptain, secondCaptain},
	}
	planner := eventUseCase.NewFormDeliveryHook(store, signalModel.DefaultRegistry)
	payload := signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: uuid.Must(uuid.NewV7())}

	if err := planner.Handle(context.Background(), signalModel.Signal{
		ID: uuid.Must(uuid.NewV7()), Type: signalModel.TypeParticipantOpenRegistrationCompleted, Payload: mustSignalPayload(t, &payload),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := store.delivered[firstCaptain]; got != 1 {
		t.Fatalf("first captain deliveries = %d, want 1", got)
	}
	if got := store.delivered[secondCaptain]; got != 1 {
		t.Fatalf("second captain deliveries = %d, want 1", got)
	}
}

func TestTimedAssignmentIncludesFutureOnlyWhenConfigured(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	assignment := eventFormModel.Assignment{
		Trigger:      eventFormModel.TriggerAtTime,
		Audience:     eventFormModel.Audience{Kind: eventFormModel.AudienceAllParticipants},
		Presentation: eventFormModel.PresentationTask,
		At:           timePtr(time.Now().UTC()),
	}
	store := &formDeliveryStore{
		assignment: eventUseCase.FormDeliveryAssignment{Assignment: assignment},
		delivered:  map[uuid.UUID]int{},
		recipients: []uuid.UUID{userID},
	}
	planner := eventUseCase.NewFormDeliveryHook(store, signalModel.DefaultRegistry)
	payload := signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: userID}
	if err := planner.Handle(context.Background(), signalModel.Signal{
		ID: uuid.Must(uuid.NewV7()), Type: signalModel.TypeParticipantEnrolled, Payload: mustSignalPayload(t, &payload),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := store.delivered[userID]; got != 1 {
		t.Fatalf("future timed delivery = %d, want 1", got)
	}
}

func timePtr(value time.Time) *time.Time { return &value }

func TestRegistrationSignalCreatesOneSubjectDelivery(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())
	assignment := eventFormModel.Assignment{
		Trigger:      eventFormModel.TriggerRegistrationOpenCompleted,
		Audience:     eventFormModel.Audience{Kind: eventFormModel.AudienceSignalSubject},
		Presentation: eventFormModel.PresentationTask,
	}
	store := &formDeliveryStore{assignment: eventUseCase.FormDeliveryAssignment{Assignment: assignment}, delivered: map[uuid.UUID]int{}}
	planner := eventUseCase.NewFormDeliveryHook(store, signalModel.DefaultRegistry)
	payload := signalModel.ParticipantPayload{ScopeEventID: eventID, SubjectUserID: userID}

	if err := planner.Handle(context.Background(), signalModel.Signal{
		ID: uuid.Must(uuid.NewV7()), Type: signalModel.TypeParticipantOpenRegistrationCompleted, Payload: mustSignalPayload(t, &payload),
	}); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if got := store.delivered[userID]; got != 1 {
		t.Fatalf("subject deliveries = %d, want 1", got)
	}
}
