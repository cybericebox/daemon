package dispatchModel

import (
	"regexp"
	"strings"
)

// Kinds of a failed email target, so the journal can show a human line. The
// raw transport text stays available for the technical details.
const (
	MailErrorAuth     = "smtp_auth"     // 530 / 535: the SMTP login was refused
	MailErrorRecip    = "smtp_rcpt"     // 550 / 553: the recipient address was refused
	MailErrorRejected = "smtp_rejected" // 554: the message was refused (e.g. address not verified in SES)
	MailErrorConnect  = "smtp_connect"  // timeout or connection failure
	MailErrorOther    = "smtp_other"    // any other SMTP reply
)

var (
	smtpReplyCode  = regexp.MustCompile(`(?:^|[^0-9])([245][0-9]{2})(?:[ -]|$)`)
	connectFailure = regexp.MustCompile(`(?i)timeout|timed out|deadline exceeded|connection (refused|reset)|no such host|dial tcp|\beof\b|broken pipe|unreachable|tls:|i/o`)
)

// ClassifyMailError maps a raw transport error of an email target to a kind
// and the SMTP reply code (empty when the text has none). It returns an empty
// kind for texts that are not transport errors (a deferral, a render error):
// those are shown as they are.
func ClassifyMailError(raw string) (kind, code string) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.HasPrefix(raw, "Відкладено") {
		return "", ""
	}
	if m := smtpReplyCode.FindStringSubmatch(raw); m != nil {
		code = m[1]
	}
	switch code {
	case "530", "535":
		return MailErrorAuth, code
	case "550", "553":
		return MailErrorRecip, code
	case "554":
		return MailErrorRejected, code
	}
	if connectFailure.MatchString(raw) {
		return MailErrorConnect, code
	}
	if code != "" {
		return MailErrorOther, code
	}
	return "", ""
}
