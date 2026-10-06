package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/gofrs/uuid"

	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type subscriptionContractUC struct {
	eventHandler.IUseCase
	items       []eventUseCase.NotificationSubscriptionView
	upsertInput eventUseCase.UpsertNotificationSubscriptionInput
	resetEvent  uuid.UUID
	resetSignal string
	resetChan   string
}

func (u *subscriptionContractUC) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (u *subscriptionContractUC) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (u *subscriptionContractUC) ListNotificationSubscriptions(context.Context, uuid.UUID) ([]eventUseCase.NotificationSubscriptionView, error) {
	return u.items, nil
}

func (u *subscriptionContractUC) UpsertNotificationSubscription(_ context.Context, _ uuid.UUID, in eventUseCase.UpsertNotificationSubscriptionInput) (eventUseCase.NotificationSubscriptionView, error) {
	u.upsertInput = in
	return eventUseCase.NotificationSubscriptionView{
		SignalType: in.SignalType, Channel: in.Channel, Enabled: in.Enabled, Audience: in.Audience, Source: "event",
	}, nil
}

func (u *subscriptionContractUC) ResetNotificationSubscription(_ context.Context, eventID uuid.UUID, signalType, channel string) error {
	u.resetEvent, u.resetSignal, u.resetChan = eventID, signalType, channel
	return nil
}

func TestNotificationSubscriptionsHTTPContract(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &subscriptionContractUC{items: []eventUseCase.NotificationSubscriptionView{
		{SignalType: "participant.enrolled", Channel: "email", Enabled: false, Audience: json.RawMessage(`{"kind":"signal_subject"}`), Source: "platform"},
	}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/notification-subscriptions"

	listed := eventRequest(t, router, http.MethodGet, base, "")
	var items []map[string]json.RawMessage
	if err := json.Unmarshal(listed["Data"], &items); err != nil || len(items) != 1 {
		t.Fatalf("list data=%s err=%v", listed["Data"], err)
	}
	if string(items[0]["Source"]) != `"platform"` || string(items[0]["SignalType"]) != `"participant.enrolled"` {
		t.Fatalf("list item must expose Source: %s", listed["Data"])
	}

	upserted := eventRequest(t, router, http.MethodPut, base,
		`{"SignalType":"participant.enrolled","Channel":"email","Enabled":true,"Audience":{"kind":"all_participants"},"Config":{"days_before_start":3}}`)
	if u.upsertInput.SignalType != "participant.enrolled" || !u.upsertInput.Enabled || string(u.upsertInput.Audience) != `{"kind":"all_participants"}` || string(u.upsertInput.Config) != `{"days_before_start":3}` {
		t.Fatalf("upsert input mismatch: %+v", u.upsertInput)
	}
	var saved map[string]json.RawMessage
	if err := json.Unmarshal(upserted["Data"], &saved); err != nil || string(saved["Source"]) != `"event"` {
		t.Fatalf("upsert data=%s err=%v", upserted["Data"], err)
	}

	eventRequest(t, router, http.MethodDelete, base+"/participant.enrolled/in_app", "")
	if u.resetEvent != eventID || u.resetSignal != "participant.enrolled" || u.resetChan != "in_app" {
		t.Fatalf("reset call mismatch: %v %q %q", u.resetEvent, u.resetSignal, u.resetChan)
	}
}

func TestNotificationTypesHTTPContract(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: &subscriptionContractUC{}}, actor)
	listed := eventRequest(t, router, http.MethodGet, "/api/events/"+eventID.String()+"/manage/notification-types", "")
	var items []struct {
		Type      string
		Channels  []string
		Variables []struct{ Name, Default string }
	}
	if err := json.Unmarshal(listed["Data"], &items); err != nil || len(items) == 0 {
		t.Fatalf("types data=%s err=%v", listed["Data"], err)
	}
	for _, item := range items {
		if item.Type == "participant.event.start_reminder" {
			names := map[string]bool{}
			for _, v := range item.Variables {
				names[v.Name] = true
			}
			if !names["event_name"] || !names["start_at"] {
				t.Fatalf("start reminder variables: %+v", item.Variables)
			}
			return
		}
	}
	t.Fatalf("start reminder missing from %s", listed["Data"])
}

func (u *subscriptionContractUC) ListEventEmailPresets(context.Context, uuid.UUID) ([]emailModel.BlockPreset, error) {
	return []emailModel.BlockPreset{{ID: uuid.Must(uuid.NewV7()), Name: "Footer", Blocks: json.RawMessage(`[{"type":"divider"}]`)}}, nil
}

func TestEmailPresetsHTTPContract(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: &subscriptionContractUC{}}, actor)
	listed := eventRequest(t, router, http.MethodGet, "/api/events/"+eventID.String()+"/manage/notification-templates/email/presets", "")
	var items []struct {
		Name   string
		Blocks json.RawMessage
	}
	if err := json.Unmarshal(listed["Data"], &items); err != nil || len(items) != 1 || items[0].Name != "Footer" || string(items[0].Blocks) != `[{"type":"divider"}]` {
		t.Fatalf("presets data=%s err=%v", listed["Data"], err)
	}
}
