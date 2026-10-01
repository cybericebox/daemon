package eventAnalyticsModel_test

import (
	"testing"
	"time"

	"github.com/gofrs/uuid"

	eventAnalyticsModel "github.com/cybericebox/daemon/internal/model/eventAnalytics"
	eventManagerModel "github.com/cybericebox/daemon/internal/model/eventManager"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

func membership(role eventManagerModel.Role) *eventManagerModel.EventManager {
	return &eventManagerModel.EventManager{EventID: uuid.Must(uuid.NewV7()), UserID: uuid.Must(uuid.NewV7()), Role: role}
}

// §7: sections for every event staff role; wrong answers and integrity for
// the owner, write moderators and platform admins only.
func TestResolveAccess(t *testing.T) {
	full := eventAnalyticsModel.Access{Sections: true, Sensitive: true}
	read := eventAnalyticsModel.Access{Sections: true}
	none := eventAnalyticsModel.Access{}
	cases := []struct {
		name       string
		membership *eventManagerModel.EventManager
		role       rbac.Role
		want       eventAnalyticsModel.Access
	}{
		{"owner", membership(eventManagerModel.RoleOwner), rbac.RoleUser, full},
		{"write moderator", membership(eventManagerModel.RoleManager), rbac.RoleUser, full},
		{"read-only moderator", membership(eventManagerModel.RoleViewer), rbac.RoleUser, read},
		{"platform admin without a role in the event", nil, rbac.RoleAdmin, full},
		{"super admin", nil, rbac.RoleSuperAdmin, full},
		{"admin viewer", nil, rbac.RoleAdminViewer, read},
		{"admin viewer who is an event owner", membership(eventManagerModel.RoleOwner), rbac.RoleAdminViewer, full},
		{"read-only moderator who is a platform admin", membership(eventManagerModel.RoleViewer), rbac.RoleAdmin, full},
		{"participant", nil, rbac.RoleUser, none},
	}
	for _, tc := range cases {
		if got := eventAnalyticsModel.ResolveAccess(tc.membership, tc.role); got != tc.want {
			t.Errorf("%s: got %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

func TestFinalAndFreeze(t *testing.T) {
	finish := time.Date(2026, 9, 29, 18, 0, 0, 0, time.UTC)
	if eventAnalyticsModel.Final(nil, finish.AddDate(1, 0, 0)) {
		t.Fatal("an event without a finish is never final")
	}
	if eventAnalyticsModel.Final(&finish, finish.Add(14*time.Minute)) || !eventAnalyticsModel.Final(&finish, finish.Add(15*time.Minute)) {
		t.Fatal("final 15 minutes after the finish")
	}
	if got := eventAnalyticsModel.FreezeAt(&finish, true, 60); got == nil || !got.Equal(finish.Add(-time.Hour)) {
		t.Fatalf("freeze at: %v", got)
	}
	if eventAnalyticsModel.FreezeAt(&finish, false, 60) != nil || eventAnalyticsModel.FreezeAt(nil, true, 60) != nil {
		t.Fatal("no freeze without the setting or a finish")
	}
}
