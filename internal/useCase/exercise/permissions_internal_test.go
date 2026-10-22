package exercise

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// Proposing an event exercise to the catalog follows the owner event's
// owners and managers, whatever the platform role: an admin manager may, an
// admin alone may not.
func TestPermissionsForPropose(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	published := exerciseModel.Exercise{
		ID: uuid.Must(uuid.NewV7()), Scope: exerciseModel.ScopeEvent, OwnerEventID: uuid.NullUUID{UUID: eventID, Valid: true},
		PublishedVersionID: uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true},
	}
	draft := published
	draft.PublishedVersionID = uuid.NullUUID{}
	archived := published
	now := time.Now()
	archived.ArchivedAt = &now
	catalog := published
	catalog.Scope, catalog.OwnerEventID = exerciseModel.ScopeCatalog, uuid.NullUUID{}
	managerOf := map[uuid.UUID]int16{eventID: int16(eventManagerModel.RoleManager)}
	ownerOf := map[uuid.UUID]int16{eventID: int16(eventManagerModel.RoleOwner)}
	viewerOf := map[uuid.UUID]int16{eventID: int16(eventManagerModel.RoleViewer)}

	cases := []struct {
		name        string
		role        rbac.Role
		exercise    exerciseModel.Exercise
		memberships map[uuid.UUID]int16
		want        bool
	}{
		{"superadmin managing the event", rbac.RoleSuperAdmin, published, managerOf, true},
		{"superadmin owning the event", rbac.RoleSuperAdmin, published, ownerOf, true},
		{"superadmin without the event", rbac.RoleSuperAdmin, published, nil, false},
		{"superadmin viewing the event", rbac.RoleSuperAdmin, published, viewerOf, false},
		{"manager", rbac.RoleUser, published, managerOf, true},
		{"manager of an unpublished exercise", rbac.RoleUser, draft, managerOf, false},
		{"manager of an archived exercise", rbac.RoleUser, archived, managerOf, false},
		{"superadmin on a catalog exercise", rbac.RoleSuperAdmin, catalog, managerOf, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := permissionsFor(Actor{UserID: uuid.Must(uuid.NewV7()), Role: tc.role}, tc.exercise, tc.memberships)
			if got.CanPropose != tc.want {
				t.Fatalf("CanPropose = %v, want %v", got.CanPropose, tc.want)
			}
		})
	}
	// The admin rights stay as they were for an admin manager.
	got := permissionsFor(Actor{Role: rbac.RoleSuperAdmin}, published, managerOf)
	if !got.CanRead || !got.CanEdit || got.CanManageAccess {
		t.Fatalf("admin rights changed: %+v", got)
	}
}
