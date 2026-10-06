package eventManagerModel_test

import (
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
)

var managerNow = time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)

func TestNewOwnerCanManageTheEvent(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	userID := uuid.Must(uuid.NewV7())

	m, err := eventManagerModel.New(eventID, userID, eventManagerModel.RoleOwner, managerNow)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.EventID != eventID || m.UserID != userID || m.Role != eventManagerModel.RoleOwner {
		t.Fatalf("manager fields: %+v", m)
	}
	if !m.CreatedAt.Equal(managerNow) {
		t.Fatalf("CreatedAt: got %s, want %s", m.CreatedAt, managerNow)
	}
	if !m.CanManage() {
		t.Fatal("owner must be able to manage the event")
	}
}

func TestViewerCannotManageTheEvent(t *testing.T) {
	m, err := eventManagerModel.New(uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), eventManagerModel.RoleViewer, managerNow)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if m.CanManage() {
		t.Fatal("viewer must not be able to change the event")
	}
}

func TestNewRejectsMissingIdentityAndUnknownRole(t *testing.T) {
	validEventID := uuid.Must(uuid.NewV7())
	validUserID := uuid.Must(uuid.NewV7())

	if _, err := eventManagerModel.New(uuid.Nil, validUserID, eventManagerModel.RoleOwner, managerNow); !errors.Is(err, eventManagerModel.ErrEventManagerIdentityInvalid.Err()) {
		t.Fatalf("missing event id: got %v", err)
	}
	if _, err := eventManagerModel.New(validEventID, uuid.Nil, eventManagerModel.RoleOwner, managerNow); !errors.Is(err, eventManagerModel.ErrEventManagerIdentityInvalid.Err()) {
		t.Fatalf("missing user id: got %v", err)
	}
	if _, err := eventManagerModel.New(validEventID, validUserID, eventManagerModel.Role(99), managerNow); !errors.Is(err, eventManagerModel.ErrEventManagerRoleInvalid.Err()) {
		t.Fatalf("unknown role: got %v", err)
	}
}
