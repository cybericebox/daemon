package mailModel_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	mailModel "github.com/cybericebox/daemon/internal/model/mail"
)

var platform = mailModel.Identity{
	FromName: "CyberICEBox", FromAddress: "notifications@mail.cybericebox.com",
	ReplyToName: "Support", ReplyToAddress: "support@cybericebox.com",
}

func TestEventIdentity_DefaultsAreTagAtSendingDomainAndPlatformReplyTo(t *testing.T) {
	id, err := mailModel.EventIdentity(platform, "mail.cybericebox.com", "Кібер Олімпіада", "olymp", mailModel.Identity{})
	require.NoError(t, err)
	require.Equal(t, mailModel.Identity{
		FromName: "Кібер Олімпіада", FromAddress: "olymp@mail.cybericebox.com",
		ReplyToName: "Support", ReplyToAddress: "support@cybericebox.com",
	}, id)

	_, err = mailModel.EventIdentity(mailModel.Identity{}, "", "CTF", "ctf", mailModel.Identity{})
	require.Error(t, err, "no sending domain and no own address")

	id, err = mailModel.EventIdentity(mailModel.Identity{}, "", "CTF", "ctf", mailModel.Identity{FromAddress: "ctf@uni.edu"})
	require.NoError(t, err)
	require.Equal(t, "ctf@uni.edu", id.FromAddress)
}

func TestEventIdentity_OwnValuesOverrideFieldByField(t *testing.T) {
	id, err := mailModel.EventIdentity(platform, "mail.cybericebox.com", "CTF", "ctf", mailModel.Identity{FromName: "Оргкомітет", ReplyToAddress: "org@uni.edu"})
	require.NoError(t, err)
	require.Equal(t, "Оргкомітет", id.FromName)
	require.Equal(t, "ctf@mail.cybericebox.com", id.FromAddress, "empty address inherits")
	require.Equal(t, "org@uni.edu", id.ReplyToAddress)
	require.Empty(t, id.ReplyToName, "an own Reply-To address does not inherit the platform name")

	id, err = mailModel.EventIdentity(platform, "mail.cybericebox.com", "CTF", "ctf", mailModel.Identity{ReplyToName: "Штаб"})
	require.NoError(t, err)
	require.Equal(t, mailModel.Identity{FromName: "CTF", FromAddress: "ctf@mail.cybericebox.com", ReplyToName: "Штаб", ReplyToAddress: "support@cybericebox.com"}, id)
}

func TestOverlay_EmptyInheritsEverything(t *testing.T) {
	require.Equal(t, platform, mailModel.Overlay(platform, mailModel.Identity{}))
}

func TestIdentity_Normalize(t *testing.T) {
	id, err := mailModel.Identity{FromName: " Org ", FromAddress: " a@b.co ", ReplyToAddress: "r@b.co"}.Normalize()
	require.NoError(t, err)
	require.Equal(t, "Org", id.FromName)
	require.Equal(t, "a@b.co", id.FromAddress)

	_, err = mailModel.Identity{}.Normalize()
	require.NoError(t, err, "every field is optional")

	_, err = mailModel.Identity{FromName: strings.Repeat("я", 64)}.Normalize()
	require.NoError(t, err, "64 characters, not bytes")
	for _, bad := range []mailModel.Identity{
		{FromName: strings.Repeat("я", 65)},
		{ReplyToName: strings.Repeat("x", 65)},
		{FromName: "a\r\nBcc: x@y.z"},
		{FromAddress: "not-an-address"},
		{FromAddress: "Name <a@b.co>"},
		{ReplyToAddress: "nope"},
	} {
		_, err = bad.Normalize()
		require.ErrorIs(t, err, mailModel.ErrIdentityInvalid.Err(), "%+v", bad)
	}
}

func TestSMTPInput_Normalize(t *testing.T) {
	in, err := mailModel.SMTPInput{Host: " smtp.example.com ", Port: 465}.Normalize()
	require.NoError(t, err)
	require.Equal(t, "smtp.example.com", in.Host)
	require.Equal(t, mailModel.TLSImplicit, in.TLSMode, "465 defaults to implicit TLS")

	for _, bad := range []mailModel.SMTPInput{{Host: "", Port: 587}, {Host: "h", Port: 0}, {Host: "h", Port: 587, TLSMode: "none"}, {Host: "h:25", Port: 25}} {
		_, err = bad.Normalize()
		require.Error(t, err, "%+v", bad)
	}
}

func TestNormalizeSendingDomain(t *testing.T) {
	got, err := mailModel.NormalizeSendingDomain("  Mail.CyberICEBox.com ")
	require.NoError(t, err)
	require.Equal(t, "mail.cybericebox.com", got)

	got, err = mailModel.NormalizeSendingDomain("")
	require.NoError(t, err, "empty = fall back to the server config")
	require.Empty(t, got)

	got, err = mailModel.NormalizeSendingDomain("xn--80ak6aa92e.com")
	require.NoError(t, err, "punycode labels are fine")
	require.Equal(t, "xn--80ak6aa92e.com", got)

	for _, bad := range []string{
		"localhost", "mail", "a@mail.example.com", "https://mail.example.com", "mail.example.com/",
		"mail example.com", "-mail.example.com", "mail-.example.com", "mail..example.com", ".example.com",
		"mail.example.com.", "192.168.0.1", "mail.example.com:25", "почта.укр", strings.Repeat("a", 64) + ".com",
		strings.Repeat("a.", 130) + "com",
	} {
		_, err := mailModel.NormalizeSendingDomain(bad)
		require.ErrorIs(t, err, mailModel.ErrSendingDomainInvalid.Err(), bad)
	}
}

func TestResolveSendingDomain_SavedWinsOverTheSenderAddress(t *testing.T) {
	require.Equal(t, "mail.cybericebox.com", mailModel.ResolveSendingDomain("mail.cybericebox.com", "support@cybericebox.com"))
	require.Equal(t, "cybericebox.com", mailModel.ResolveSendingDomain("", "Support@CyberICEBox.com"))
	require.Empty(t, mailModel.ResolveSendingDomain("", ""))
}

func TestIdentity_WithSendingDomainKeepsTheMailbox(t *testing.T) {
	require.Equal(t, "support@mail.cybericebox.com", mailModel.Identity{FromAddress: "support@cybericebox.com"}.WithSendingDomain("mail.cybericebox.com").FromAddress)
	require.Equal(t, "notifications@mail.cybericebox.com", mailModel.Identity{}.WithSendingDomain("mail.cybericebox.com").FromAddress)
	require.Equal(t, "support@cybericebox.com", mailModel.Identity{FromAddress: "support@cybericebox.com"}.WithSendingDomain("").FromAddress)
}
