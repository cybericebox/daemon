package dispatchModel

import (
	"regexp"
	"strings"
)

// emailInText finds addresses inside free text (an SMTP reply quotes the recipient).
var emailInText = regexp.MustCompile(`[A-Za-z0-9._%+\-]+@[A-Za-z0-9.\-]+\.[A-Za-z]{2,}`)

// MaskEmail keeps the first character of the local part and the domain: «olena@example.com» → «o***@example.com».
// A text without «@» is returned as it is.
func MaskEmail(email string) string {
	at := strings.LastIndex(email, "@")
	if at < 0 {
		return email
	}
	local := []rune(email[:at])
	if len(local) == 0 {
		return "***" + email[at:]
	}
	return string(local[0]) + "***" + email[at:]
}

// MaskRecipients hides the recipients' addresses of journal rows from a read-only event viewer: the address of every
// row and target, and any address quoted in an error text. Names, statuses and the rest stay.
func MaskRecipients(rows []DispatchDetail) []DispatchDetail {
	masked := make([]DispatchDetail, len(rows))
	for i, row := range rows {
		row.RecipientEmail = MaskEmail(row.RecipientEmail)
		targets := make([]DispatchTarget, len(row.Targets))
		for j, target := range row.Targets {
			target.Recipient = MaskEmail(target.Recipient)
			target.Error = maskEmailsIn(target.Error)
			target.FallbackError = maskEmailsIn(target.FallbackError)
			targets[j] = target
		}
		row.Targets = targets
		masked[i] = row
	}
	return masked
}

func maskEmailsIn(text string) string {
	return emailInText.ReplaceAllStringFunc(text, MaskEmail)
}
