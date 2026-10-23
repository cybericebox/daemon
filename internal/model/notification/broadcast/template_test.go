package broadcastModel_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gofrs/uuid"

	broadcastModel "github.com/cybericebox/daemon/internal/model/notification/broadcast"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
)

func TestTemplateInputValidate(t *testing.T) {
	inApp := broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelInApp}, InAppTitle: "Hi {{.user_name}}"}
	email := broadcastModel.Content{Channels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail}, Subject: "Hi", EmailBody: json.RawMessage(`[{"type":"paragraph"}]`)}
	cases := []struct {
		name string
		in   broadcastModel.TemplateInput
		ok   bool
	}{
		{"in-app only", broadcastModel.TemplateInput{Name: "Reminder", Content: inApp}, true},
		{"email only", broadcastModel.TemplateInput{Name: "Reminder", Content: email}, true},
		{"name required", broadcastModel.TemplateInput{Name: "  ", Content: inApp}, false},
		{"name too long", broadcastModel.TemplateInput{Name: strings.Repeat("я", broadcastModel.MaxTextLen+1), Content: inApp}, false},
		{"a channel is required", broadcastModel.TemplateInput{Name: "Reminder", Content: broadcastModel.Content{}}, false},
		{"email needs a subject", broadcastModel.TemplateInput{Name: "Reminder", Content: broadcastModel.Content{Channels: email.Channels, EmailBody: email.EmailBody}}, false},
		{"only broadcast variables", broadcastModel.TemplateInput{Name: "Reminder", Content: broadcastModel.Content{Channels: inApp.Channels, InAppTitle: "{{.team_name}}"}}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := c.in.Validate(); (err == nil) != c.ok {
				t.Fatalf("Validate = %v, want ok=%v", err, c.ok)
			}
		})
	}
}

func TestScopeFilter(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	if broadcastModel.ScopeFilter(nil) != "platform" || broadcastModel.ScopeFilter(&id) != id.String() {
		t.Fatal("scope filter")
	}
}
