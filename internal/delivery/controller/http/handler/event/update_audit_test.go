package event_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/controller/http/audit"
	eventHandler "github.com/cybericebox/daemon/internal/delivery/controller/http/handler/event"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	"github.com/cybericebox/daemon/internal/model/rbac"
	eventUseCase "github.com/cybericebox/daemon/internal/useCase/event"
)

// Renaming an event is recorded as the old and the new name; the fields that did not change are not listed.
func TestEventUpdateRecordsWhatChanged(t *testing.T) {
	actor, eventID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	start := time.Date(2026, 10, 1, 9, 0, 0, 0, time.UTC)
	u := &eventCRUDContractUC{view: eventUseCase.EventView{
		ID: eventID, Tag: "autumn", Name: "Old name", AvailableFrom: start,
		Status: eventModel.EventPendingStatus, LifecycleStatus: eventModel.LifecycleNotPublished,
	}}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(response.WithErrorHandler)
	var changes []string
	router.Use(func(ctx *gin.Context) {
		claims := rbac.Claims{UserID: actor, Role: rbac.RoleSuperAdmin}
		ctx.Request = ctx.Request.WithContext(rbac.ContextWithCurrentUserSession(ctx.Request.Context(), claims))
		ctx.Next()
		for _, c := range audit.Changes(ctx) {
			changes = append(changes, c.Field+":"+c.Before.(string)+"->"+c.After.(string))
		}
	})
	eventHandler.NewEventAPIHandler(u, allowEventCRUD{}).Init(router.Group("api"))

	req := httptest.NewRequest(http.MethodPut, "/api/events/"+eventID.String(),
		strings.NewReader(`{"Tag":"autumn","Name":"New name","AvailableFrom":"2026-10-01T09:00:00Z","ArchiveAt":null}`))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("update: %d %s", w.Code, w.Body.String())
	}
	if len(changes) != 1 || changes[0] != "Name:Old name->New name" {
		t.Fatalf("changes = %v", changes)
	}
}
