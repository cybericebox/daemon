package notificationPayloads_test

import (
	"encoding/json"
	"testing"

	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
)

func TestContinueRegistrationPayload(t *testing.T) {
	p := notificationPayloads.ContinueRegistrationPayload{
		RegistrationURL: "https://id.x/setup?token=t",
		Name:            "Jane",
	}
	if p.NotificationType() != notificationTypes.NotificationTypeContinueRegistration {
		t.Fatalf("type: got %q", p.NotificationType())
	}
	raw, err := p.Marshal()
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]string
	if err = json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["RegistrationURL"] == "" || got["Name"] != "Jane" {
		t.Fatalf("vars wrong: %v", got)
	}
}

func TestPasswordResetPayload(t *testing.T) {
	p := notificationPayloads.PasswordResetPayload{
		ResetURL: "https://id.x/reset-password?token=t",
		Name:     "Jane",
	}
	if p.NotificationType() != notificationTypes.NotificationTypePasswordReset {
		t.Fatalf("type: got %q", p.NotificationType())
	}
	if _, err := p.Marshal(); err != nil {
		t.Fatalf("marshal: %v", err)
	}
}
