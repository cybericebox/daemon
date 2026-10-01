package event_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gofrs/uuid"

	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

type templateContractUC struct {
	eventHandler.IUseCase
	emailItems []eventUseCase.EventEmailTemplateView
	inAppItems []eventUseCase.EventInAppTemplateView
	customErr  error
	customized struct {
		channel             string
		eventID, templateID uuid.UUID
		actor               uuid.UUID
	}
	resetErr error
	resets   []string // "channel|eventID|type"
}

func (u *templateContractUC) ResetEventEmailTemplateType(_ context.Context, eventID uuid.UUID, notificationType string) error {
	u.resets = append(u.resets, "email|"+eventID.String()+"|"+notificationType)
	return u.resetErr
}

func (u *templateContractUC) ResetEventInAppTemplateType(_ context.Context, eventID uuid.UUID, notificationType string) error {
	u.resets = append(u.resets, "in-app|"+eventID.String()+"|"+notificationType)
	return u.resetErr
}

func (u *templateContractUC) RequireReadEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (u *templateContractUC) RequireManageEvent(context.Context, uuid.UUID, uuid.UUID) error {
	return nil
}

func (u *templateContractUC) ListEventEmailTemplates(context.Context, uuid.UUID, emailModel.ListFilter) ([]eventUseCase.EventEmailTemplateView, error) {
	return u.emailItems, nil
}

func (u *templateContractUC) GetEventEmailTemplate(_ context.Context, _, templateID uuid.UUID) (eventUseCase.EventEmailTemplateView, error) {
	for _, item := range u.emailItems {
		if item.ID == templateID {
			return item, nil
		}
	}
	return eventUseCase.EventEmailTemplateView{}, nil
}

func (u *templateContractUC) CustomizeEventEmailTemplate(_ context.Context, eventID, templateID, actor uuid.UUID) (emailModel.EmailTemplate, error) {
	u.customized.channel, u.customized.eventID, u.customized.templateID, u.customized.actor = "email", eventID, templateID, actor
	if u.customErr != nil {
		return emailModel.EmailTemplate{}, u.customErr
	}
	return emailModel.EmailTemplate{ID: uuid.Must(uuid.NewV7()), ScopeEventID: &eventID, NotificationType: "participant.enrolled", Status: "draft"}, nil
}

func (u *templateContractUC) ListEventInAppTemplates(context.Context, uuid.UUID, inAppModel.ListFilter) ([]eventUseCase.EventInAppTemplateView, error) {
	return u.inAppItems, nil
}

func (u *templateContractUC) GetEventInAppTemplate(_ context.Context, _, templateID uuid.UUID) (eventUseCase.EventInAppTemplateView, error) {
	for _, item := range u.inAppItems {
		if item.ID == templateID {
			return item, nil
		}
	}
	return eventUseCase.EventInAppTemplateView{}, nil
}

func (u *templateContractUC) CustomizeEventInAppTemplate(_ context.Context, eventID, templateID, actor uuid.UUID) (inAppModel.InAppTemplate, error) {
	u.customized.channel, u.customized.eventID, u.customized.templateID, u.customized.actor = "in-app", eventID, templateID, actor
	if u.customErr != nil {
		return inAppModel.InAppTemplate{}, u.customErr
	}
	return inAppModel.InAppTemplate{ID: uuid.Must(uuid.NewV7()), ScopeEventID: &eventID, NotificationType: "participant.enrolled", Status: "draft"}, nil
}

func requireSource(t *testing.T, raw json.RawMessage, want string) {
	t.Helper()
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	if string(item["Source"]) != `"`+want+`"` {
		t.Fatalf("Source=%s want %q in %s", item["Source"], want, raw)
	}
}

func TestEventEmailTemplatesHTTPContract(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	platformID, eventRowID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &templateContractUC{emailItems: []eventUseCase.EventEmailTemplateView{
		{EmailTemplate: emailModel.EmailTemplate{ID: platformID, NotificationType: "participant.invitation.sent", Status: "published"}, Source: eventUseCase.TemplateSourcePlatform},
		{EmailTemplate: emailModel.EmailTemplate{ID: eventRowID, ScopeEventID: &eventID, NotificationType: "participant.enrolled", Status: "draft"}, Source: eventUseCase.TemplateSourceEvent},
	}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/notification-templates/email"

	listed := eventRequest(t, router, http.MethodGet, base, "")
	var items []json.RawMessage
	if err := json.Unmarshal(listed["Data"], &items); err != nil || len(items) != 2 {
		t.Fatalf("list data=%s err=%v", listed["Data"], err)
	}
	requireSource(t, items[0], "platform")
	requireSource(t, items[1], "event")

	got := eventRequest(t, router, http.MethodGet, base+"/"+platformID.String(), "")
	requireSource(t, got["Data"], "platform")

	customized := eventRequest(t, router, http.MethodPost, base+"/"+platformID.String()+"/customize", "")
	if u.customized.channel != "email" || u.customized.eventID != eventID || u.customized.templateID != platformID || u.customized.actor != actor {
		t.Fatalf("customize call mismatch: %+v", u.customized)
	}
	requireSource(t, customized["Data"], "event")
}

func TestEventInAppTemplatesHTTPContract(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	platformID := uuid.Must(uuid.NewV7())
	u := &templateContractUC{inAppItems: []eventUseCase.EventInAppTemplateView{
		{InAppTemplate: inAppModel.InAppTemplate{ID: platformID, NotificationType: "participant.enrolled", Status: "published"}, Source: eventUseCase.TemplateSourcePlatform},
	}}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/notification-templates/in-app"

	listed := eventRequest(t, router, http.MethodGet, base, "")
	var items []json.RawMessage
	if err := json.Unmarshal(listed["Data"], &items); err != nil || len(items) != 1 {
		t.Fatalf("list data=%s err=%v", listed["Data"], err)
	}
	requireSource(t, items[0], "platform")

	got := eventRequest(t, router, http.MethodGet, base+"/"+platformID.String(), "")
	requireSource(t, got["Data"], "platform")

	customized := eventRequest(t, router, http.MethodPost, base+"/"+platformID.String()+"/customize", "")
	if u.customized.channel != "in-app" || u.customized.eventID != eventID || u.customized.templateID != platformID || u.customized.actor != actor {
		t.Fatalf("customize call mismatch: %+v", u.customized)
	}
	requireSource(t, customized["Data"], "event")
}

func TestCustomizeEventTemplate_DraftExistsIsConflict(t *testing.T) {
	actor, eventID, templateID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &templateContractUC{customErr: notificationModel.ErrTemplateDraftExists.Err()}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	for _, channel := range []string{"email", "in-app"} {
		path := "/api/events/" + eventID.String() + "/manage/notification-templates/" + channel + "/" + templateID.String() + "/customize"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, nil))
		if w.Code != http.StatusConflict {
			t.Fatalf("%s customize: status=%d want 409 body=%s", channel, w.Code, w.Body.String())
		}
	}
}

func TestResetEventTemplateType_HTTPContract(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &templateContractUC{}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	base := "/api/events/" + eventID.String() + "/manage/notification-templates/"

	eventRequest(t, router, http.MethodDelete, base+"email/type/participant.enrolled", "")
	eventRequest(t, router, http.MethodDelete, base+"in-app/type/participant.enrolled", "")
	want := []string{
		"email|" + eventID.String() + "|participant.enrolled",
		"in-app|" + eventID.String() + "|participant.enrolled",
	}
	if len(u.resets) != 2 || u.resets[0] != want[0] || u.resets[1] != want[1] {
		t.Fatalf("resets=%v want %v", u.resets, want)
	}
}

func TestResetEventTemplateType_NonEventScopedTypeIsBadRequest(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	u := &templateContractUC{resetErr: notificationModel.ErrTemplateTypeNotEventScoped.Err()}
	router := testEventCRUDRouter(&eventCRUDContractUC{IUseCase: u}, actor)
	for _, channel := range []string{"email", "in-app"} {
		path := "/api/events/" + eventID.String() + "/manage/notification-templates/" + channel + "/type/event.manager.assigned"
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodDelete, path, nil))
		if w.Code != http.StatusBadRequest {
			t.Fatalf("%s reset: status=%d want 400 body=%s", channel, w.Code, w.Body.String())
		}
	}
}
