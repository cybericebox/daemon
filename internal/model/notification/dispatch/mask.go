package dispatchModel

import (
	"regexp"
	"strings"
)

// emailInText finds addresses inside free text (an SMTP reply quotes the recipient): anything around an «@» up to
// a space, a bracket or a quote, so unicode local parts and IDN domains are caught as well.
var emailInText = regexp.MustCompile(`[^\s<>@"']+@[^\s<>@"']+`)

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
		known := []string{row.RecipientEmail}
		for _, target := range row.Targets {
			known = append(known, target.Recipient)
		}
		row.RecipientEmail = MaskEmail(row.RecipientEmail)
		targets := make([]DispatchTarget, len(row.Targets))
		for j, target := range row.Targets {
			target.Recipient = MaskEmail(target.Recipient)
			target.Error = MaskEmailsIn(target.Error, known...)
			target.FallbackError = MaskEmailsIn(target.FallbackError, known...)
			targets[j] = target
		}
		row.Targets = targets
		masked[i] = row
	}
	return masked
}

// MaskEmailsIn masks the given known addresses wherever they appear (whatever their form), then any other address.
func MaskEmailsIn(text string, known ...string) string {
	for _, address := range known {
		if strings.Contains(address, "@") {
			text = strings.ReplaceAll(text, address, MaskEmail(address))
		}
	}
	return emailInText.ReplaceAllStringFunc(text, func(found string) string {
		if strings.Contains(found, "***@") {
			return found
		}
		return MaskEmail(found)
	})
}
