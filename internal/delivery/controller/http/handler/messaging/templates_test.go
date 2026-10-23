package messaging

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"

	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// templateUseCase records the scope and the input of every template call.
type templateUseCase struct {
	fakeUseCase
	scopes  []*uuid.UUID
	inputs  []broadcastModel.TemplateInput
	deleted []uuid.UUID
}

func (f *templateUseCase) ListBroadcastTemplates(_ context.Context, scope *uuid.UUID) ([]broadcastModel.Template, error) {
	f.scopes = append(f.scopes, scope)
	return []broadcastModel.Template{{ID: uuid.Must(uuid.NewV7()), Name: "Reminder"}}, nil
}
func (f *templateUseCase) CreateBroadcastTemplate(_ context.Context, scope *uuid.UUID, _ uuid.UUID, in broadcastModel.TemplateInput) (broadcastModel.Template, error) {
	f.scopes = append(f.scopes, scope)
	f.inputs = append(f.inputs, in)
	return broadcastModel.Template{ID: uuid.Must(uuid.NewV7()), ScopeEventID: scope, Name: in.Name, Content: in.Content}, nil
}
func (f *templateUseCase) UpdateBroadcastTemplate(_ context.Context, scope *uuid.UUID, id, _ uuid.UUID, in broadcastModel.TemplateInput) (broadcastModel.Template, error) {
	f.scopes = append(f.scopes, scope)
	f.inputs = append(f.inputs, in)
	return broadcastModel.Template{ID: id, ScopeEventID: scope, Name: in.Name, Content: in.Content}, nil
}
func (f *templateUseCase) DeleteBroadcastTemplate(_ context.Context, scope *uuid.UUID, id uuid.UUID) error {
	f.scopes = append(f.scopes, scope)
	f.deleted = append(f.deleted, id)
	return nil
}

const templateBody = `{"Name":"Reminder","Channels":["in_app"],"InAppTitle":"Hi","InAppBody":"<p>x</p>"}`

func TestPlatformTemplateRoutesNeedTheBroadcastPermission(t *testing.T) {
	uc := &templateUseCase{}
	if w := doTemplates(t, uc, http.MethodGet, "/api/notifications/broadcast-templates", "", ""); w.Code != http.StatusForbidden {
		t.Fatalf("list without a permission = %d", w.Code)
	}
	for _, perm := range []rbac.Permission{rbac.PermNotificationsTemplatesWrite, rbac.PermNotificationsBannersWrite} {
		if w := doTemplates(t, uc, http.MethodPost, "/api/notifications/broadcast-templates", string(perm), templateBody); w.Code != http.StatusForbidden {
			t.Fatalf("create with %s = %d", perm, w.Code)
		}
	}
	if w := doTemplates(t, uc, http.MethodPost, "/api/notifications/broadcast-templates", string(rbac.PermNotificationsBroadcast), templateBody); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"Name":"Reminder"`) {
		t.Fatalf("create = %d %s", w.Code, w.Body.String())
	}
	if len(uc.inputs) != 1 || uc.scopes[0] != nil || uc.inputs[0].Content.InAppTitle != "Hi" {
		t.Fatalf("platform create: scopes=%v inputs=%+v", uc.scopes, uc.inputs)
	}
	id := uuid.Must(uuid.NewV7())
	if w := doTemplates(t, uc, http.MethodDelete, "/api/notifications/broadcast-templates/"+id.String(), string(rbac.PermNotificationsBroadcast), ""); w.Code != http.StatusOK {
		t.Fatalf("delete = %d", w.Code)
	}
	if w := doTemplates(t, uc, http.MethodPut, "/api/notifications/broadcast-templates/not-a-uuid", string(rbac.PermNotificationsBroadcast), templateBody); w.Code != http.StatusBadRequest {
		t.Fatalf("bad id = %d", w.Code)
	}
}

func TestEventTemplateWritesNeedManageAccess(t *testing.T) {
	event := uuid.Must(uuid.NewV7())
	base := "/api/events/" + event.String() + "/manage/broadcast-templates"

	viewer := &templateUseCase{fakeUseCase: fakeUseCase{role: "viewer"}}
	if w := doTemplates(t, viewer, http.MethodGet, base, "", ""); w.Code != http.StatusOK {
		t.Fatalf("a viewer lists the templates: %d", w.Code)
	}
	for _, c := range []struct{ method, path string }{{http.MethodPost, base}, {http.MethodPut, base + "/" + uuid.Must(uuid.NewV7()).String()}, {http.MethodDelete, base + "/" + uuid.Must(uuid.NewV7()).String()}} {
		if w := doTemplates(t, viewer, c.method, c.path, "", templateBody); w.Code != http.StatusForbidden {
			t.Fatalf("a viewer %s: %d", c.method, w.Code)
		}
	}
	if len(viewer.inputs) != 0 || len(viewer.deleted) != 0 {
		t.Fatal("a viewer's write reached the use case")
	}

	owner := &templateUseCase{fakeUseCase: fakeUseCase{role: "owner"}}
	if w := doTemplates(t, owner, http.MethodPost, base, "", templateBody); w.Code != http.StatusOK {
		t.Fatalf("the owner creates: %d %s", w.Code, w.Body.String())
	}
	if len(owner.scopes) != 1 || owner.scopes[0] == nil || *owner.scopes[0] != event {
		t.Fatalf("event scope lost: %v", owner.scopes)
	}
}

func doTemplates(t *testing.T, uc *templateUseCase, method, path, perms, body string) *httptest.ResponseRecorder {
	t.Helper()
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	NewMessagingAPIHandler(uc, permProtection{}).Init(router.Group("/api"))
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	if perms != "" {
		req.Header.Set("X-Perms", perms)
	}
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}
