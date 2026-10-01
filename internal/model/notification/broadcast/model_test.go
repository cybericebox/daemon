package broadcastModel_test

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"

	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
)

func TestAudienceValidate(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	cases := []struct {
		name  string
		event bool
		aud   broadcastModel.Audience
		ok    bool
	}{
		{"platform all", false, broadcastModel.Audience{Kind: broadcastModel.KindAll}, true},
		{"platform roles", false, broadcastModel.Audience{Kind: broadcastModel.KindRoles, Roles: []string{"admin"}}, true},
		{"platform roles need a role", false, broadcastModel.Audience{Kind: broadcastModel.KindRoles}, false},
		{"platform unknown role", false, broadcastModel.Audience{Kind: broadcastModel.KindRoles, Roles: []string{"root"}}, false},
		{"platform users", false, broadcastModel.Audience{Kind: broadcastModel.KindUsers, UserIDs: []uuid.UUID{id}}, true},
		{"platform users need a user", false, broadcastModel.Audience{Kind: broadcastModel.KindUsers}, false},
		{"platform cannot use an event audience", false, broadcastModel.Audience{Kind: broadcastModel.KindApproved}, false},
		{"event approved", true, broadcastModel.Audience{Kind: broadcastModel.KindApproved}, true},
		{"event staff", true, broadcastModel.Audience{Kind: broadcastModel.KindStaff}, true},
		{"event teams", true, broadcastModel.Audience{Kind: broadcastModel.KindTeams, TeamIDs: []uuid.UUID{id}}, true},
		{"event teams need a team", true, broadcastModel.Audience{Kind: broadcastModel.KindTeams}, false},
		{"event participants need a participant", true, broadcastModel.Audience{Kind: broadcastModel.KindParticipants}, false},
		{"event cannot mail every user", true, broadcastModel.Audience{Kind: broadcastModel.KindAll}, false},
		{"event cannot pick platform roles", true, broadcastModel.Audience{Kind: broadcastModel.KindRoles, Roles: []string{"admin"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.aud.Validate(c.event); (err == nil) != c.ok {
				t.Fatalf("Validate = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestContentValidate(t *testing.T) {
	email, inApp := notificationTypes.NotificationChannelEmail, notificationTypes.NotificationChannelInApp
	body := json.RawMessage(`[{"type":"paragraph","content":[{"type":"variable","varName":"user_name"}]}]`)
	cases := []struct {
		name string
		c    broadcastModel.Content
		ok   bool
	}{
		{"email only", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{email}, Subject: "Hi {{.user_name}}", EmailBody: body}, true},
		{"in-app only", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{inApp}, InAppTitle: "Hi", InAppBody: "<p>{{.event_name}}</p>"}, true},
		{"both", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{email, inApp}, Subject: "s", EmailBody: body, InAppTitle: "t"}, true},
		{"no channel", broadcastModel.Content{Subject: "s", EmailBody: body}, false},
		{"duplicate channel", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{inApp, inApp}, InAppTitle: "t"}, false},
		{"unknown channel", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{"sms"}}, false},
		{"email needs a subject", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{email}, EmailBody: body}, false},
		{"email needs a body", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{email}, Subject: "s", EmailBody: json.RawMessage(`[]`)}, false},
		{"in-app needs a title", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{inApp}, InAppBody: "b"}, false},
		{"unknown variable in the subject", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{email}, Subject: "{{.secret}}", EmailBody: body}, false},
		{"unknown variable in the in-app body", broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{inApp}, InAppTitle: "t", InAppBody: "{{.secret}}"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.c.Validate(); (err == nil) != c.ok {
				t.Fatalf("Validate = %v, want ok=%v", err, c.ok)
			}
		})
	}
}
