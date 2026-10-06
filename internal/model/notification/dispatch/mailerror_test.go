package dispatchModel_test

import (
	"testing"

	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

func TestClassifyMailError(t *testing.T) {
	cases := []struct {
		raw, kind, code string
	}{
		{"535 5.7.8 Authentication credentials invalid", dispatchModel.MailErrorAuth, "535"},
		{"email: failed to send: gomail: could not send email 1: 530 Authentication required", dispatchModel.MailErrorAuth, "530"},
		{"smtp: 530 5.7.0 Must issue a STARTTLS command first", dispatchModel.MailErrorAuth, "530"},
		{"550 5.1.1 <a@b.c>: Recipient address rejected", dispatchModel.MailErrorRecip, "550"},
		{"553 5.1.3 mailbox name not allowed", dispatchModel.MailErrorRecip, "553"},
		{"554 Message rejected: Email address is not verified.", dispatchModel.MailErrorRejected, "554"},
		{"dial tcp 10.0.0.1:587: i/o timeout", dispatchModel.MailErrorConnect, ""},
		{"dial tcp: lookup smtp.example.com: no such host", dispatchModel.MailErrorConnect, ""},
		{"read tcp 10.0.0.1:5000->1.2.3.4:587: connection reset by peer", dispatchModel.MailErrorConnect, ""},
		{"context deadline exceeded", dispatchModel.MailErrorConnect, ""},
		{"EOF", dispatchModel.MailErrorConnect, ""},
		{"421 4.3.2 Service not available", dispatchModel.MailErrorOther, "421"},
		{"452 4.5.3 Too many recipients", dispatchModel.MailErrorOther, "452"},
		{dispatchModel.DeferredQuotaMessage, "", ""},
		{dispatchModel.DeferredRateMessage, "", ""},
		{"template render failed", "", ""},
		{"", "", ""},
	}
	for _, c := range cases {
		kind, code := dispatchModel.ClassifyMailError(c.raw)
		if kind != c.kind || code != c.code {
			t.Errorf("%q: got (%q, %q), want (%q, %q)", c.raw, kind, code, c.kind, c.code)
		}
	}
}
