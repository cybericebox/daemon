// Package mailUseCase owns the W7 mail transports: SMTP settings stored in
// the database (platform and per Event, passwords sealed with pkg/secret),
// the per-message transport and sender resolution used by the email channel,
// connection tests and the Event delivery journal.
package mailUseCase

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/mailRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/pkg/email"
	"github.com/cybericebox/daemon/pkg/secret"
)

// ErrNotConfigured is the delivery error when neither the database nor the
// environment defines a platform transport.
var ErrNotConfigured = errors.New("mail is not configured: no platform SMTP settings")

type (
	repoPort interface {
		mailRepo.Queries
		eventRepo.Queries
		userRepo.Queries
		dispatchRepo.Queries
	}

	// sender is one SMTP client; the factory lets tests replace the network.
	sender interface {
		SendMessage(ctx context.Context, msg email.Message) error
	}

	Dependencies struct {
		Repo repoPort
		// Cipher seals SMTP passwords (PLATFORM_SECRETS_KEY); nil disables
		// storing passwords.
		Cipher *secret.Cipher
		// Env is the bootstrap SMTP_* transport, used only while the database
		// holds no platform settings.
		Env config.SMTPConfig
		// Domain is the platform DOMAIN: the main site in the footer and the
		// support@ Reply-To fallback.
		Domain string
		// NewSender defaults to pkg/email.
		NewSender func(email.Config) (sender, error)
	}

	MailUseCase struct {
		mail       *mailRepo.Repository
		events     *eventRepo.Repository
		users      *userRepo.Repository
		dispatches *dispatchRepo.Repository
		cipher     *secret.Cipher
		env        config.SMTPConfig
		domain     string
		newSender  func(email.Config) (sender, error)
		now        func() time.Time
		limiter    *sendLimiter
	}
)

func NewMailUseCase(deps Dependencies) *MailUseCase {
	newSender := deps.NewSender
	if newSender == nil {
		newSender = func(cfg email.Config) (sender, error) { return email.New(cfg) }
	}
	u := &MailUseCase{
		mail:       mailRepo.New(deps.Repo),
		events:     eventRepo.New(deps.Repo),
		users:      userRepo.New(deps.Repo),
		dispatches: dispatchRepo.New(deps.Repo),
		cipher:     deps.Cipher,
		env:        deps.Env,
		domain:     deps.Domain,
		newSender:  newSender,
		now:        time.Now,
	}
	u.limiter = newSendLimiter(func() time.Time { return u.now() })
	return u
}

// transport is a resolved SMTP connection with the identity of its source.
type transport struct {
	source   string // dispatchModel.Transport*
	conn     email.Config
	identity mailModel.Identity
	// domain is the sending domain Event senders are built on.
	domain string
	// limits are the send limits in effect; eventID is set for an Event's own
	// SMTP. A transport without a source (an unsaved form test) is not limited.
	limits  mailModel.Limits
	eventID *uuid.UUID
	// providerID is set for a stored platform provider: its daily allowance is
	// claimed on its row before each send.
	providerID *uuid.UUID
}

// limitKey identifies the transport for rate limiting.
func (t transport) limitKey() string {
	if t.providerID != nil {
		return t.source + ":" + t.providerID.String()
	}
	return limitKey(t.source, t.eventID)
}

// platformRoute is how platform mail leaves: the saved providers (enabled ones
// with daily room left, in the order they are tried) when any is enabled, otherwise
// the SMTP_* env transport. Env is used when no provider is enabled (none
// saved, or all switched off); it is never mixed with the list.
type platformRoute struct {
	identity mailModel.Identity
	domain   string
	// transports are the attempts of one message, in order.
	transports []transport
	// configured: the env transport or at least one enabled provider exists.
	configured bool
	// exhausted: providers are enabled but every one is over its daily limit.
	exhausted bool
	// err is why a provider was left out (its password cannot be opened).
	err error
}

// platformRoute resolves the platform route on every call, so edits apply
// without a restart.
func (u *MailUseCase) platformRoute(ctx context.Context) (platformRoute, error) {
	sender, err := u.platformSender(ctx)
	if err != nil {
		return platformRoute{}, err
	}
	route := platformRoute{identity: sender.identity, domain: sender.domain}
	providers, err := u.mail.PlatformProviders(ctx)
	if err != nil {
		return platformRoute{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load SMTP providers").Err()
	}
	if !anyEnabled(providers) && u.envActive() {
		route.configured = true
		route.transports = []transport{{
			source: dispatchModel.TransportEnv,
			conn: withSender(email.Config{
				Host: u.env.Host, Port: u.env.Port, Username: u.env.Username, Password: u.env.Password,
			}, sender.identity),
			identity: sender.identity,
			domain:   sender.domain,
			limits:   u.envLimits(),
		}}
		return route, nil
	}
	now := u.now()
	route.configured = anyEnabled(providers)
	route.exhausted = mailModel.AllExhausted(providers, now)
	for _, p := range mailModel.SelectProviders(providers, now) {
		t, err := u.providerTransport(p, sender)
		if err != nil {
			route.err = err
			continue
		}
		route.transports = append(route.transports, t)
	}
	return route, nil
}

// anyEnabled: at least one saved provider is switched on.
func anyEnabled(providers []mailModel.SMTPConfig) bool {
	for _, p := range providers {
		if p.Enabled {
			return true
		}
	}
	return false
}

// envActive: the SMTP_* env vars define a transport (used while no provider is enabled).
func (u *MailUseCase) envActive() bool {
	return u.env.Host != "" && u.env.SenderEmail != ""
}

// providerTransport is the transport of one stored provider. Its sender is the
// platform sender with the provider's own fields on top. The daily limit is
// counted on the provider row, so only the rate limit goes to the limiter.
func (u *MailUseCase) providerTransport(p mailModel.SMTPConfig, sender platformSender) (transport, error) {
	conn, err := u.connection(p, "")
	if err != nil {
		return transport{}, err
	}
	identity := mailModel.Overlay(sender.identity, p.Identity)
	id := p.ID
	return transport{
		source: dispatchModel.TransportPlatform, conn: withSender(conn, identity), identity: identity, domain: sender.domain,
		limits:     mailModel.Limits{PerSecond: p.StoredLimits().PerSecond},
		providerID: &id,
	}, nil
}

// envIdentity is the SMTP_* bootstrap sender.
func (u *MailUseCase) envIdentity() mailModel.Identity {
	return mailModel.Identity{
		FromName: u.env.SenderName, FromAddress: u.env.SenderEmail,
		ReplyToName: u.env.ReplyToName, ReplyToAddress: u.env.ReplyToEmail,
	}
}

// platformSender is the resolved platform sender: what is saved, the server
// config it falls back to, and the sending domain Event senders are built on.
type platformSender struct {
	identity mailModel.Identity
	domain   string
	stored   mailModel.PlatformSettings
}

// platformSender resolves the platform sender in effect: saved values over the
// env bootstrap, with the product name and support@DOMAIN where both are
// empty. A saved sending domain moves the sender address (saved or env
// mailbox, else notifications) onto that domain unless a sender address is saved.
func (u *MailUseCase) platformSender(ctx context.Context) (platformSender, error) {
	stored, err := u.mail.PlatformSettings(ctx)
	if err != nil {
		return platformSender{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load platform sender").Err()
	}
	identity := mailModel.Overlay(u.envIdentity(), stored.Identity)
	if stored.Identity.FromAddress == "" {
		identity = identity.WithSendingDomain(stored.SendingDomain)
	}
	identity = identity.WithPlatformDefaults(u.domain)
	return platformSender{
		identity: identity, stored: stored,
		domain: mailModel.ResolveSendingDomain(stored.SendingDomain, identity.FromAddress),
	}, nil
}

// platformIdentity is the platform sender in effect.
func (u *MailUseCase) platformIdentity(ctx context.Context) (mailModel.Identity, error) {
	sender, err := u.platformSender(ctx)
	return sender.identity, err
}

// withSender sets the client's default sender; each message sets its own.
func withSender(conn email.Config, id mailModel.Identity) email.Config {
	if id.FromAddress != "" {
		conn.SenderName, conn.SenderEmail = id.FromName, id.FromAddress
	}
	conn.ReplyToName, conn.ReplyToEmail = id.ReplyToName, id.ReplyToAddress
	return conn
}

// connection turns a stored row into client settings, decrypting the
// password; plainPassword (a form value under test) wins when non-empty.
func (u *MailUseCase) connection(cfg mailModel.SMTPConfig, plainPassword string) (email.Config, error) {
	password := plainPassword
	if password == "" && cfg.PasswordCiphertext != "" {
		if u.cipher == nil {
			return email.Config{}, mailModel.ErrSecretsUnavailable.Err()
		}
		plain, err := u.cipher.DecryptWithContext(cfg.PasswordCiphertext, passwordContext(cfg.ID))
		if err != nil {
			return email.Config{}, model.ErrPlatform.WithError(err).WithMessage("Stored SMTP password cannot be decrypted").Err()
		}
		password = string(plain)
	}
	// The client needs a default sender; each message sets its own From.
	return email.Config{
		Host: cfg.Host, Port: cfg.Port, Username: cfg.Username, Password: password,
		TLS: email.TLSMode(cfg.TLSMode), SenderEmail: "notifications@" + cfg.Host,
	}, nil
}

// seal encrypts a new password bound to the row id.
func (u *MailUseCase) seal(id uuid.UUID, password string) (string, error) {
	if u.cipher == nil {
		return "", mailModel.ErrSecretsUnavailable.Err()
	}
	ciphertext, err := u.cipher.EncryptWithContext([]byte(password), passwordContext(id))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to seal SMTP password").Err()
	}
	return ciphertext, nil
}

// passwordContext binds ciphertext to its row: a copied value fails to open.
func passwordContext(id uuid.UUID) []byte {
	return []byte(fmt.Sprintf("mail.smtp:%s", id))
}

// send is the single exit of every email: it appends the standard footer to
// both parts, so no template (admin or organizer edited) can drop it. The
// footer's support address is the message Reply-To. msg is a copy, so a
// retry through another transport gets the footer once.
func (u *MailUseCase) send(ctx context.Context, t transport, msg email.Message) error {
	footer, err := u.footer(ctx, msg.ReplyTo.Email)
	if err != nil {
		return err
	}
	msg.HTML, msg.Text = footer.Append(msg.HTML, msg.Text)
	if err = u.throttle(ctx, t); err != nil {
		return err
	}
	if err = u.reserve(ctx, t); err != nil {
		return err
	}
	client, err := u.newSender(t.conn)
	if err != nil {
		u.release(ctx, t, err)
		return err
	}
	if err = client.SendMessage(ctx, msg); err != nil {
		u.release(ctx, t, err)
		return err
	}
	u.limiter.sent(t.limitKey())
	u.markUsed(ctx, t)
	return nil
}

// reserve claims one message of a provider's daily allowance. A used-up
// allowance defers the message; it is retried regularly, so a limit raised or
// a provider added meanwhile is picked up before the next UTC day.
func (u *MailUseCase) reserve(ctx context.Context, t transport) error {
	if t.providerID == nil {
		return nil
	}
	ok, err := u.mail.ReserveSend(ctx, *t.providerID, u.now())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to count the SMTP provider send").Err()
	}
	if !ok {
		return &dispatchModel.DeferredError{Message: dispatchModel.DeferredQuotaMessage, RetryAfter: quotaRetryAfter}
	}
	return nil
}

// release gives a claimed message back after a failed send. A provider failure
// (authentication, quota, outage) is kept on the provider as its last error.
// Bookkeeping never fails a send, and survives a cancelled request.
func (u *MailUseCase) release(ctx context.Context, t transport, sendErr error) {
	if t.providerID == nil {
		return
	}
	providerError := ""
	if mailModel.ProviderFailure(sendErr) {
		providerError = truncate(sendErr.Error(), maxStoredError)
	}
	_ = u.mail.ReleaseSend(context.WithoutCancel(ctx), *t.providerID, u.now(), providerError)
}

func (u *MailUseCase) markUsed(ctx context.Context, t transport) {
	if t.providerID != nil {
		_ = u.mail.MarkUsed(context.WithoutCancel(ctx), *t.providerID, u.now())
	}
}

// maxStoredError bounds the provider error text kept on its row.
const maxStoredError = 500

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// Footer is the footer send appends to mail routed as eventID (nil: the
// platform), for template previews. It resolves the Reply-To the way Deliver
// does, but needs no working transport: without one the platform defaults
// apply.
func (u *MailUseCase) Footer(ctx context.Context, eventID *uuid.UUID) (mailModel.Footer, error) {
	identity, err := u.platformIdentity(ctx)
	if err != nil {
		return mailModel.Footer{}, err
	}
	replyTo := identity.ReplyToAddress
	if eventID != nil {
		own, err := u.mail.EventIdentity(ctx, *eventID)
		if err != nil {
			return mailModel.Footer{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load event sender").Err()
		}
		replyTo = mailModel.Overlay(identity, own).ReplyToAddress
	}
	return u.footer(ctx, replyTo)
}

// footer is the one footer builder of send and Footer: the platform footer
// (saved, else the built-in default) with the message Reply-To.
func (u *MailUseCase) footer(ctx context.Context, supportEmail string) (mailModel.Footer, error) {
	stored, err := u.mail.PlatformSettings(ctx)
	if err != nil {
		return mailModel.Footer{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load email footer").Err()
	}
	doc := mailModel.StoredFooterDoc(stored.FooterContent, stored.FooterLegacy)
	return renderFooter(doc, supportEmail, mailModel.PlatformSite(u.domain)), nil
}
