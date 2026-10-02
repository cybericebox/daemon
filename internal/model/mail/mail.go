// Package mailModel is the W7 mail domain: SMTP transports (platform and
// per-Event), the sender identity rules and the Event mail settings.
package mailModel

import (
	"fmt"
	"net/mail"
	"net/netip"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/pkg/email"
)

type TLSMode string

const (
	TLSStartTLS TLSMode = "starttls"
	TLSImplicit TLSMode = "tls"
)

// SMTPConfig is one stored SMTP transport. ScopeEventID nil is the platform
// transport. The password is kept only as ciphertext bound to ID.
type SMTPConfig struct {
	ID                 uuid.UUID
	ScopeEventID       *uuid.UUID
	Host               string
	Port               int
	TLSMode            TLSMode
	Username           string
	PasswordCiphertext string
	UpdatedBy          *uuid.UUID
	UpdatedAt          time.Time
	// Send limits of this transport; nil = not set here.
	MaxPerSecond *float64
	DailyQuota   *int

	// Provider fields: set on the platform rows only (a list of providers).
	Name      string
	Priority  int
	Enabled   bool
	CreatedAt time.Time
	// Identity is the optional sender of this provider; empty fields use the
	// platform sender.
	Identity Identity
	Usage    Usage
}

// SMTPInput is an edit of the connection fields. An empty Password keeps the
// stored one unless ClearPassword is set.
type SMTPInput struct {
	Host          string
	Port          int
	TLSMode       TLSMode
	Username      string
	Password      string
	ClearPassword bool
	// Send limits; nil (or 0) clears the saved value.
	MaxPerSecond *float64
	DailyQuota   *int
}

// Normalize trims the input and validates the connection fields.
func (in SMTPInput) Normalize() (SMTPInput, error) {
	in.Host = strings.TrimSpace(in.Host)
	in.Username = strings.TrimSpace(in.Username)
	if in.TLSMode == "" {
		in.TLSMode = TLSStartTLS
		if in.Port == 465 {
			in.TLSMode = TLSImplicit
		}
	}
	switch {
	case in.Host == "" || len(in.Host) > 253 || strings.ContainsAny(in.Host, " /:@"):
		return in, ErrSMTPSettingsInvalid.WithMessage("SMTP host is invalid").Err()
	case in.Port < 1 || in.Port > 65535:
		return in, ErrSMTPSettingsInvalid.WithMessage("SMTP port must be 1-65535").Err()
	case in.TLSMode != TLSStartTLS && in.TLSMode != TLSImplicit:
		return in, ErrSMTPSettingsInvalid.WithMessage("TLS mode must be starttls or tls").Err()
	case len(in.Username) > 254 || len(in.Password) > 1024:
		return in, ErrSMTPSettingsInvalid.WithMessage("SMTP field is too long").Err()
	}
	var err error
	if in.MaxPerSecond, in.DailyQuota, err = NormalizeLimits(in.MaxPerSecond, in.DailyQuota); err != nil {
		return in, err
	}
	return in, nil
}

// SMTPPolicy limits what an organizer may point an event SMTP at.
type SMTPPolicy struct {
	// AllowedPorts is the ports an event SMTP may use; empty = the usual mail submission ports.
	AllowedPorts []int
}

// DefaultSMTPPorts are the mail submission ports.
var DefaultSMTPPorts = []int{25, 465, 587, 2525}

// internalHostSuffixes mark names that only resolve inside a network.
var internalHostSuffixes = []string{".local", ".localdomain", ".internal", ".lan", ".home.arpa", ".svc", ".cluster", ".intranet", ".corp"}

// CheckEventTarget refuses a destination an organizer must not reach: an IP literal in a loopback, private,
// link-local, carrier-grade NAT or other special range, a name that is not a public domain (no dot, an
// internal suffix, localhost), and a port outside the allowed list. A name that looks public is checked
// again when it is resolved, at connect time (the dial guard).
func (in SMTPInput) CheckEventTarget(policy SMTPPolicy) error {
	bad := func(msg string) error { return ErrSMTPSettingsInvalid.WithMessage(msg).Err() }
	ports := policy.AllowedPorts
	if len(ports) == 0 {
		ports = DefaultSMTPPorts
	}
	if !slices.Contains(ports, in.Port) {
		return bad("SMTP port is not allowed")
	}
	host := strings.ToLower(strings.TrimSuffix(in.Host, "."))
	if ip, err := netip.ParseAddr(strings.Trim(host, "[]")); err == nil {
		if email.AddressBlocked(ip) {
			return bad("SMTP host is not allowed")
		}
		return nil
	}
	if host == "localhost" || !strings.Contains(host, ".") {
		return bad("SMTP host is not allowed")
	}
	for _, suffix := range internalHostSuffixes {
		if strings.HasSuffix(host, suffix) {
			return bad("SMTP host is not allowed")
		}
	}
	return nil
}

// SameConnection reports whether the input points at the stored connection: the same host, port, username
// and TLS mode. Only then the stored password may be used with it.
func (in SMTPInput) SameConnection(stored SMTPConfig) bool {
	return strings.EqualFold(strings.TrimSuffix(in.Host, "."), strings.TrimSuffix(stored.Host, ".")) &&
		in.Port == stored.Port && in.Username == stored.Username && in.TLSMode == stored.TLSMode
}

// MaxNameLength bounds the sender and Reply-To display names.
const MaxNameLength = 64

// Identity is the sender and Reply-To of a message, or, stored per scope, the
// overrides of that scope where an empty field means "inherit".
type Identity struct {
	FromName       string
	FromAddress    string
	ReplyToName    string
	ReplyToAddress string
}

// Normalize trims the identity and validates it; every field may be empty.
func (id Identity) Normalize() (Identity, error) {
	id.FromName = strings.TrimSpace(id.FromName)
	id.FromAddress = strings.TrimSpace(id.FromAddress)
	id.ReplyToName = strings.TrimSpace(id.ReplyToName)
	id.ReplyToAddress = strings.TrimSpace(id.ReplyToAddress)
	for _, name := range []string{id.FromName, id.ReplyToName} {
		if utf8.RuneCountInString(name) > MaxNameLength || strings.ContainsAny(name, "\r\n") {
			return id, ErrIdentityInvalid.WithMessage("Sender name is too long or invalid").Err()
		}
	}
	if id.FromAddress != "" && !validAddress(id.FromAddress) {
		return id, ErrIdentityInvalid.WithMessage("Sender address is invalid").Err()
	}
	if id.ReplyToAddress != "" && !validAddress(id.ReplyToAddress) {
		return id, ErrIdentityInvalid.WithMessage("Reply-To address is invalid").Err()
	}
	return id, nil
}

// Overlay returns over on top of base: an empty sender field falls back to
// base field by field. The Reply-To name is bound to its address: a name
// alone decorates the inherited address, an own address never inherits the
// name of another one.
func Overlay(base, over Identity) Identity {
	out := base
	if over.FromName != "" {
		out.FromName = over.FromName
	}
	if over.FromAddress != "" {
		out.FromAddress = over.FromAddress
	}
	if over.ReplyToAddress != "" {
		out.ReplyToAddress, out.ReplyToName = over.ReplyToAddress, over.ReplyToName
	} else if over.ReplyToName != "" {
		out.ReplyToName = over.ReplyToName
	}
	return out
}

// AddressDomain is the domain of an e-mail address
// (notifications@mail.example.com → mail.example.com).
func AddressDomain(address string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return ""
	}
	return strings.ToLower(address[at+1:])
}

// AddressLocalPart is the mailbox of an e-mail address (before the last @).
func AddressLocalPart(address string) string {
	at := strings.LastIndex(address, "@")
	if at < 0 {
		return ""
	}
	return address[:at]
}

// defaultSenderMailbox is the mailbox of the platform sender when only a
// sending domain is known.
const defaultSenderMailbox = "notifications"

var domainLabelPattern = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// NormalizeSendingDomain lowercases and validates the platform sending domain
// (the domain verified at the SMTP provider, usually a mail subdomain such as
// mail.example.com). Empty is valid: the SMTP_SENDER_EMAIL domain applies.
func NormalizeSendingDomain(v string) (string, error) {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "", nil
	}
	if !validDomain(v) {
		return v, ErrSendingDomainInvalid.Err()
	}
	return v, nil
}

func validDomain(v string) bool {
	if len(v) > 253 {
		return false
	}
	labels := strings.Split(v, ".")
	if len(labels) < 2 {
		return false
	}
	for _, label := range labels {
		if !domainLabelPattern.MatchString(label) {
			return false
		}
	}
	// A numeric last label would make this an IP address, not a domain name.
	last := labels[len(labels)-1]
	return strings.Trim(last, "0123456789") != ""
}

// ResolveSendingDomain is the domain Event senders are built on: the saved
// platform sending domain, else the domain of the platform sender address.
func ResolveSendingDomain(saved, fromAddress string) string {
	if saved != "" {
		return saved
	}
	return AddressDomain(fromAddress)
}

// WithSendingDomain moves the sender address to domain, keeping its mailbox
// (notifications when there is none yet). An empty domain changes nothing.
func (id Identity) WithSendingDomain(domain string) Identity {
	if domain == "" {
		return id
	}
	mailbox := AddressLocalPart(id.FromAddress)
	if mailbox == "" {
		mailbox = defaultSenderMailbox
	}
	id.FromAddress = mailbox + "@" + domain
	return id
}

// PlatformSettings is what the admin saved for the platform mail identity:
// the sender, the sending domain and the footer. Empty fields fall back to the
// server config and the built-in defaults.
type PlatformSettings struct {
	Identity      Identity
	SendingDomain string
	// FooterContent is the saved footer (a Lexical document); FooterLegacy is
	// the markdown text saved before rich footers, read when no content exists.
	FooterContent []byte
	FooterLegacy  string
}

// EventIdentity is the sender of an Event's participant mail. Without own
// values it is the Event name at <tag>@<sendingDomain> with the platform
// Reply-To; the Event overrides apply on top field by field.
func EventIdentity(platform Identity, sendingDomain, eventName, eventTag string, own Identity) (Identity, error) {
	base := Identity{FromName: eventName, ReplyToName: platform.ReplyToName, ReplyToAddress: platform.ReplyToAddress}
	if sendingDomain != "" {
		base.FromAddress = eventTag + "@" + sendingDomain
	} else if own.FromAddress == "" {
		return Identity{}, fmt.Errorf("platform sender address is not configured")
	}
	return Overlay(base, own), nil
}

func validAddress(v string) bool {
	if v == "" || len(v) > 254 {
		return false
	}
	parsed, err := mail.ParseAddress(v)
	return err == nil && parsed.Address == v
}
