package errorJournal

import (
	"github.com/gofrs/uuid"
	"time"
)

// SMTPProviderFailure groups changing failure messages by the configured provider.
func SMTPProviderFailure(id uuid.UUID, name, failure string, at time.Time) Event {
	message := "SMTP provider failed to send email"
	if name != "" {
		message = "SMTP provider " + name + " failed to send email"
	}
	return Event{Kind: KindMail, Source: "smtp-provider:" + id.String(), Key: "smtp-provider-delivery", Message: message,
		At: at.Truncate(time.Microsecond), Details: map[string]string{"provider": name, "error": failure}}
}
