package mailUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/pkg/email"
	liberr "github.com/cybericebox/daemon/pkg/err"
	"github.com/cybericebox/daemon/pkg/tools"
)

const (
	SourceDatabase = "database"
	SourceEnv      = "env"
	SourceNone     = "none"
)

// Where a sender value comes from: saved in the system, from the server config
// (SMTP_* env), derived from the saved sending domain, a built-in default, or
// not set at all.
const (
	FieldEvent    = "event"
	FieldPlatform = "platform"
	FieldSaved    = "saved"
	FieldEnv      = "env"
	FieldDerived  = "derived"
	FieldDefault  = "default"
	FieldNone     = "none"
)

type (
	// Party is one display name plus mailbox.
	Party struct {
		Name    string
		Address string
	}

	// IdentityView is a sender and Reply-To pair.
	IdentityView struct {
		Sender  Party
		ReplyTo Party
	}

	// SMTPView is a stored SMTP server. The password is never returned, only
	// whether one is stored.
	SMTPView struct {
		Host        string
		Port        int
		TLSMode     string
		Username    string
		PasswordSet bool
		UpdatedAt   time.Time
		// Saved send limits; nil = not set.
		MaxPerSecond *float64
		DailyQuota   *int
	}

	// LimitsView is the send limits in effect for a transport and where each
	// comes from (saved, env or none). Env* are the server-config values shown
	// as placeholders (platform only). Used24h is the number of messages
	// delivered through the transport in the last 24 hours.
	LimitsView struct {
		PerSecond        float64
		DailyQuota       int
		PerSecondSource  string
		DailyQuotaSource string
		EnvPerSecond     float64
		EnvDailyQuota    int
		Used24h          int64
	}

	// FooterView is the platform email footer, a rich-text document like the
	// email body blocks. Content is what was saved (null: the built-in
	// DefaultContent is sent); a footer saved as text is converted.
	FooterView struct {
		Content        json.RawMessage `swaggertype:"object"`
		DefaultContent json.RawMessage `swaggertype:"object"`
		Variables      []string
	}

	// FieldSources tells where each sender value in effect comes from.
	FieldSources struct {
		SenderName     string
		SenderAddress  string
		ReplyToName    string
		ReplyToAddress string
		SendingDomain  string
	}

	// FooterPreview is a footer rendered with the values of real mail.
	FooterPreview struct {
		HTML string
		Text string
	}

	// PlatformSettingsView is the admin view: the saved sender, the sender in
	// effect (saved, else env, else defaults) and the optional SMTP server.
	PlatformSettingsView struct {
		Identity  IdentityView
		Effective IdentityView
		Footer    FooterView
		// SendingDomain is the domain in effect: Events send from
		// <tag>@SendingDomain. SavedSendingDomain is what was saved (empty: the
		// domain of the server config sender applies); EnvSendingDomain is that
		// server config domain, shown as the placeholder.
		SendingDomain      string
		SavedSendingDomain string
		EnvSendingDomain   string
		Sources            FieldSources
		// Source of the SMTP transport in use: database (the providers below),
		// env (SMTP_*, used only while no provider is enabled) or none. EnvActive
		// means the env transport is the one in use.
		Source     string
		Configured bool
		EnvActive  bool
		Env        *EnvSummary
		Providers  []ProviderView
	}

	// ProviderView is a stored platform SMTP provider with its usage today
	// (UTC day). The password is never returned, only whether one is stored.
	ProviderView struct {
		ID          uuid.UUID
		Name        string
		Host        string
		Port        int
		TLSMode     string
		Username    string
		PasswordSet bool
		Priority    int
		Enabled     bool
		// Sender is the provider's own sender (empty fields use the platform one).
		Sender  Party
		ReplyTo Party
		// Limits; nil = not set.
		MaxPerSecond *float64
		DailyLimit   *int
		SentToday    int
		// Exhausted: the daily limit is used up until ResetsAt.
		Exhausted   bool
		ResetsAt    time.Time
		LastUsedAt  *time.Time
		LastError   string
		LastErrorAt *time.Time
		UpdatedAt   time.Time
	}

	// EnvSummary describes the SMTP_* bootstrap transport without secrets.
	EnvSummary struct {
		Host        string
		Port        int
		FromName    string
		FromAddress string
		ReplyTo     string
	}

	// EventSettingsView is the Event manage view: its own sender overrides,
	// what applies where they are empty, and the optional Event SMTP.
	EventSettingsView struct {
		Identity IdentityView
		// Inherited is what applies where the Event leaves a field empty, and
		// InheritedSources says where each value comes from: event (its name),
		// derived (<tag>@sending domain), platform, default or none. The
		// server-config fallback is never revealed to an Event: it is platform.
		Inherited          IdentityView
		InheritedSources   FieldSources
		SendingDomain      string
		SMTP               *SMTPView
		PlatformConfigured bool
		Limits             LimitsView
	}

	// TestResult reports a synchronous test send to the current user.
	TestResult struct {
		Sent      bool
		Recipient string
		Transport string
		Error     string
	}
)

func identityView(id mailModel.Identity) IdentityView {
	return IdentityView{
		Sender:  Party{Name: id.FromName, Address: id.FromAddress},
		ReplyTo: Party{Name: id.ReplyToName, Address: id.ReplyToAddress},
	}
}

func smtpView(cfg mailModel.SMTPConfig) *SMTPView {
	return &SMTPView{
		Host: cfg.Host, Port: cfg.Port, TLSMode: string(cfg.TLSMode), Username: cfg.Username,
		PasswordSet: cfg.PasswordCiphertext != "", UpdatedAt: cfg.UpdatedAt,
		MaxPerSecond: cfg.MaxPerSecond, DailyQuota: cfg.DailyQuota,
	}
}

// limitsView resolves the limits in effect. env is the server-config fallback
// (zero for an Event: its own provider account, no platform values).
func (u *MailUseCase) limitsView(ctx context.Context, cfg *mailModel.SMTPConfig, env mailModel.Limits, source string, eventID *uuid.UUID) (LimitsView, error) {
	var saved mailModel.Limits
	if cfg != nil {
		saved = cfg.StoredLimits()
	}
	eff, src := mailModel.ResolveLimits(saved, env)
	view := LimitsView{
		PerSecond: eff.PerSecond, DailyQuota: eff.DailyQuota,
		PerSecondSource: string(src.PerSecond), DailyQuotaSource: string(src.DailyQuota),
		EnvPerSecond: env.PerSecond, EnvDailyQuota: env.DailyQuota,
	}
	if eff.DailyQuota > 0 && source != "" {
		used, err := u.dispatches.CountEmailDelivered(ctx, source, eventID, u.now().Add(-quotaWindow))
		if err != nil {
			return LimitsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to count sent email").Err()
		}
		view.Used24h = used
	}
	return view, nil
}

// --- platform ---

func (u *MailUseCase) GetPlatformMailSettings(ctx context.Context) (PlatformSettingsView, error) {
	providers, err := u.mail.PlatformProviders(ctx)
	if err != nil {
		return PlatformSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load SMTP providers").Err()
	}
	sender, err := u.platformSender(ctx)
	if err != nil {
		return PlatformSettingsView{}, err
	}
	view := PlatformSettingsView{
		Source: SourceNone, Identity: identityView(sender.stored.Identity), Effective: identityView(sender.identity),
		Footer: FooterView{
			Content:        mailModel.StoredFooterDoc(sender.stored.FooterContent, sender.stored.FooterLegacy),
			DefaultContent: mailModel.DefaultFooterDoc(mailModel.LanguageUK),
			Variables:      mailModel.FooterVariables,
		},
		SendingDomain:      sender.domain,
		SavedSendingDomain: sender.stored.SendingDomain,
		EnvSendingDomain:   mailModel.AddressDomain(u.env.SenderEmail),
		Sources:            u.fieldSources(sender),
		Providers:          u.providerViews(providers),
	}
	if anyEnabled(providers) {
		view.Source, view.Configured = SourceDatabase, true
	}
	if u.envActive() {
		view.Env = &EnvSummary{Host: u.env.Host, Port: u.env.Port, FromName: u.env.SenderName, FromAddress: u.env.SenderEmail, ReplyTo: u.env.ReplyToEmail}
		if !anyEnabled(providers) {
			view.Source, view.Configured, view.EnvActive = SourceEnv, true, true
		}
	}
	return view, nil
}

func (u *MailUseCase) providerViews(providers []mailModel.SMTPConfig) []ProviderView {
	now := u.now()
	out := make([]ProviderView, 0, len(providers))
	for _, p := range providers {
		out = append(out, ProviderView{
			ID: p.ID, Name: p.Name, Host: p.Host, Port: p.Port, TLSMode: string(p.TLSMode), Username: p.Username,
			PasswordSet: p.PasswordCiphertext != "", Priority: p.Priority, Enabled: p.Enabled,
			Sender:       Party{Name: p.Identity.FromName, Address: p.Identity.FromAddress},
			ReplyTo:      Party{Name: p.Identity.ReplyToName, Address: p.Identity.ReplyToAddress},
			MaxPerSecond: p.MaxPerSecond, DailyLimit: p.DailyQuota,
			SentToday: p.Usage.SentOn(now), Exhausted: !p.HasCapacity(now), ResetsAt: mailModel.NextReset(now),
			LastUsedAt: p.Usage.LastUsedAt, LastError: p.Usage.LastError, LastErrorAt: p.Usage.LastErrorAt,
			UpdatedAt: p.UpdatedAt,
		})
	}
	return out
}

// fieldSources names where each value of the platform sender in effect comes
// from, following the same precedence as platformSender.
func (u *MailUseCase) fieldSources(sender platformSender) FieldSources {
	saved, env := sender.stored.Identity, u.envIdentity()
	pick := func(saved, env string) string {
		switch {
		case saved != "":
			return FieldSaved
		case env != "":
			return FieldEnv
		}
		return FieldNone
	}
	src := FieldSources{
		SenderName:     pick(saved.FromName, env.FromName),
		SenderAddress:  pick(saved.FromAddress, env.FromAddress),
		ReplyToAddress: pick(saved.ReplyToAddress, env.ReplyToAddress),
		SendingDomain:  pick(sender.stored.SendingDomain, mailModel.AddressDomain(env.FromAddress)),
	}
	if src.SenderName == FieldNone {
		src.SenderName = FieldDefault
	}
	if saved.FromAddress == "" && sender.stored.SendingDomain != "" {
		src.SenderAddress = FieldDerived
	}
	if src.ReplyToAddress == FieldNone && sender.identity.ReplyToAddress != "" {
		src.ReplyToAddress = FieldDefault
	}
	// A Reply-To name belongs to its address: an address of its own never takes
	// the server config name.
	src.ReplyToName = pick(saved.ReplyToName, env.ReplyToName)
	if saved.ReplyToAddress != "" && saved.ReplyToName == "" {
		src.ReplyToName = FieldNone
	}
	return src
}

// UpdatePlatformIdentity saves the platform sender, Reply-To and sending
// domain; empty fields fall back to the env bootstrap and the built-in
// defaults.
func (u *MailUseCase) UpdatePlatformIdentity(ctx context.Context, in mailModel.Identity, sendingDomain string, by uuid.UUID) (PlatformSettingsView, error) {
	in, err := in.Normalize()
	if err != nil {
		return PlatformSettingsView{}, err
	}
	sendingDomain, err = mailModel.NormalizeSendingDomain(sendingDomain)
	if err != nil {
		return PlatformSettingsView{}, err
	}
	if err = u.mail.SavePlatformIdentity(ctx, in, sendingDomain, by, u.now().UTC()); err != nil {
		return PlatformSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save platform sender").Err()
	}
	return u.GetPlatformMailSettings(ctx)
}

// UpdatePlatformFooter saves the platform email footer document; an empty (or
// default) document restores the built-in default. Events cannot edit it.
func (u *MailUseCase) UpdatePlatformFooter(ctx context.Context, content json.RawMessage, by uuid.UUID) (PlatformSettingsView, error) {
	doc, err := mailModel.NormalizeFooterDoc(content)
	if err != nil {
		return PlatformSettingsView{}, err
	}
	if err = u.mail.SaveFooter(ctx, doc, by, u.now().UTC()); err != nil {
		return PlatformSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save email footer").Err()
	}
	return u.GetPlatformMailSettings(ctx)
}

// PreviewPlatformFooter renders an unsaved footer document with the platform
// Reply-To and site, exactly as send would append it.
func (u *MailUseCase) PreviewPlatformFooter(ctx context.Context, content json.RawMessage) (FooterPreview, error) {
	doc, err := mailModel.NormalizeFooterDoc(content)
	if err != nil {
		return FooterPreview{}, err
	}
	identity, err := u.platformIdentity(ctx)
	if err != nil {
		return FooterPreview{}, err
	}
	footer := renderFooter(doc, identity.ReplyToAddress, mailModel.PlatformSite(u.domain))
	return FooterPreview{HTML: footer.HTML, Text: footer.Text}, nil
}

// --- platform providers ---

// CreatePlatformProvider adds an SMTP provider at the end of the priority order.
func (u *MailUseCase) CreatePlatformProvider(ctx context.Context, in mailModel.ProviderInput, by uuid.UUID) (PlatformSettingsView, error) {
	in, err := in.Normalize()
	if err != nil {
		return PlatformSettingsView{}, err
	}
	cfg, err := u.applyProviderInput(mailModel.SMTPConfig{}, false, in, by)
	if err != nil {
		return PlatformSettingsView{}, err
	}
	if _, err = u.mail.CreateProvider(ctx, cfg); err != nil {
		return PlatformSettingsView{}, providerWriteError(err, "Failed to save SMTP provider")
	}
	return u.GetPlatformMailSettings(ctx)
}

// UpdatePlatformProvider rewrites a provider. An empty password keeps the
// stored one unless it is cleared; the order and the counters are kept.
func (u *MailUseCase) UpdatePlatformProvider(ctx context.Context, id uuid.UUID, in mailModel.ProviderInput, by uuid.UUID) (PlatformSettingsView, error) {
	in, err := in.Normalize()
	if err != nil {
		return PlatformSettingsView{}, err
	}
	existing, ok, err := u.mail.PlatformProvider(ctx, id)
	if err != nil {
		return PlatformSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load SMTP provider").Err()
	}
	if !ok {
		return PlatformSettingsView{}, mailModel.ErrProviderNotFound.Err()
	}
	cfg, err := u.applyProviderInput(existing, true, in, by)
	if err != nil {
		return PlatformSettingsView{}, err
	}
	if _, ok, err = u.mail.UpdateProvider(ctx, cfg); err != nil {
		return PlatformSettingsView{}, providerWriteError(err, "Failed to save SMTP provider")
	}
	if !ok {
		return PlatformSettingsView{}, mailModel.ErrProviderNotFound.Err()
	}
	return u.GetPlatformMailSettings(ctx)
}

// SetPlatformProviderEnabled switches a provider on or off.
func (u *MailUseCase) SetPlatformProviderEnabled(ctx context.Context, id uuid.UUID, enabled bool, by uuid.UUID) (PlatformSettingsView, error) {
	_, ok, err := u.mail.SetProviderEnabled(ctx, id, enabled, by, u.now().UTC())
	if err != nil {
		return PlatformSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to switch SMTP provider").Err()
	}
	if !ok {
		return PlatformSettingsView{}, mailModel.ErrProviderNotFound.Err()
	}
	return u.GetPlatformMailSettings(ctx)
}

func (u *MailUseCase) DeletePlatformProvider(ctx context.Context, id uuid.UUID) (PlatformSettingsView, error) {
	deleted, err := u.mail.DeleteProvider(ctx, id)
	if err != nil {
		return PlatformSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to delete SMTP provider").Err()
	}
	if !deleted {
		return PlatformSettingsView{}, mailModel.ErrProviderNotFound.Err()
	}
	return u.GetPlatformMailSettings(ctx)
}

// ReorderPlatformProviders sets the try order: the listed providers first, in
// that order, then any not listed in their current order.
func (u *MailUseCase) ReorderPlatformProviders(ctx context.Context, ids []uuid.UUID) (PlatformSettingsView, error) {
	providers, err := u.mail.PlatformProviders(ctx)
	if err != nil {
		return PlatformSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load SMTP providers").Err()
	}
	if err = u.mail.ReorderProviders(ctx, mailModel.ProviderOrder(providers, ids)); err != nil {
		return PlatformSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to reorder SMTP providers").Err()
	}
	return u.GetPlatformMailSettings(ctx)
}

// TestPlatformProvider sends a test email to the current user through the form
// values (in != nil; an empty password uses the stored one of provider id, when
// given), the stored provider id, or, with neither, the transport platform mail
// goes out through now (the SMTP_* env one, or the first available provider). A test through a stored provider counts
// against its daily limit and updates its last error like any other message.
func (u *MailUseCase) TestPlatformProvider(ctx context.Context, id *uuid.UUID, in *mailModel.SMTPInput, userID uuid.UUID) (TestResult, error) {
	res, err := u.testPlatformProvider(ctx, id, in, userID)
	if err == nil {
		u.journalTest(ctx, userID, nil, dispatchModel.TransportPlatform, res)
	}
	return res, err
}

func (u *MailUseCase) testPlatformProvider(ctx context.Context, id *uuid.UUID, in *mailModel.SMTPInput, userID uuid.UUID) (TestResult, error) {
	recipient, err := u.recipient(ctx, userID)
	if err != nil {
		return TestResult{}, err
	}
	sender, err := u.platformSender(ctx)
	if err != nil {
		return TestResult{}, err
	}
	var stored mailModel.SMTPConfig
	var found bool
	if id != nil {
		if stored, found, err = u.mail.PlatformProvider(ctx, *id); err != nil {
			return TestResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load SMTP provider").Err()
		}
	}
	var t transport
	switch {
	case in != nil:
		normalized, err := in.Normalize()
		if err != nil {
			return TestResult{}, err
		}
		if t, err = u.formTransport(stored, found, normalized); err != nil {
			return TestResult{}, err
		}
		t.identity = mailModel.Overlay(sender.identity, stored.Identity)
		t.conn = withSender(t.conn, t.identity)
		t.source = dispatchModel.TransportPlatform
	case found:
		if t, err = u.providerTransport(stored, sender); err != nil {
			return TestResult{}, err
		}
	case id != nil:
		return TestResult{}, mailModel.ErrProviderNotFound.Err()
	default:
		// No provider named: the transport platform mail goes out through now.
		route, err := u.platformRoute(ctx)
		if err != nil {
			return TestResult{}, err
		}
		if len(route.transports) == 0 {
			return TestResult{Recipient: recipient, Error: ErrNotConfigured.Error()}, nil
		}
		t = route.transports[0]
	}
	return u.testSend(ctx, t, t.identity, recipient, "платформи"), nil
}

// --- Event ---

func (u *MailUseCase) GetEventMailSettings(ctx context.Context, eventID uuid.UUID) (EventSettingsView, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventSettingsView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	own, err := u.mail.EventIdentity(ctx, eventID)
	if err != nil {
		return EventSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load event sender").Err()
	}
	platform, err := u.platformRoute(ctx)
	if err != nil {
		return EventSettingsView{}, err
	}
	platformOK := platform.configured
	sender, err := u.platformSender(ctx)
	if err != nil {
		return EventSettingsView{}, err
	}
	inherited, idErr := mailModel.EventIdentity(platform.identity, platform.domain, e.Name, e.Tag, mailModel.Identity{})
	if idErr != nil {
		// No sending domain: there is no default sender address.
		inherited = mailModel.Identity{FromName: e.Name, ReplyToName: platform.identity.ReplyToName, ReplyToAddress: platform.identity.ReplyToAddress}
	}
	view := EventSettingsView{
		Identity: identityView(own), Inherited: identityView(inherited), PlatformConfigured: platformOK,
		SendingDomain: platform.domain, InheritedSources: eventSources(u.fieldSources(sender), idErr == nil),
	}
	cfg, ok, err := u.mail.EventSMTP(ctx, eventID)
	if err != nil {
		return EventSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load event SMTP settings").Err()
	}
	var stored *mailModel.SMTPConfig
	if ok {
		view.SMTP, stored = smtpView(cfg), &cfg
	}
	if view.Limits, err = u.limitsView(ctx, stored, mailModel.Limits{}, dispatchModel.TransportEvent, &eventID); err != nil {
		return EventSettingsView{}, err
	}
	return view, nil
}

// eventSources is the view of the platform field sources an Event sees: its
// own defaults, the platform values (saved or server config alike) and the
// sender address derived from the sending domain.
func eventSources(platform FieldSources, hasDomain bool) FieldSources {
	fold := func(src string) string {
		if src == FieldSaved || src == FieldEnv || src == FieldDerived {
			return FieldPlatform
		}
		return src
	}
	out := FieldSources{
		SenderName: FieldEvent, SenderAddress: FieldNone,
		ReplyToName: fold(platform.ReplyToName), ReplyToAddress: fold(platform.ReplyToAddress),
		SendingDomain: fold(platform.SendingDomain),
	}
	if hasDomain {
		out.SenderAddress = FieldDerived
	}
	return out
}

// UpdateEventIdentity saves the Event sender and Reply-To overrides; empty
// fields inherit.
func (u *MailUseCase) UpdateEventIdentity(ctx context.Context, eventID uuid.UUID, in mailModel.Identity, by uuid.UUID) (EventSettingsView, error) {
	in, err := in.Normalize()
	if err != nil {
		return EventSettingsView{}, err
	}
	if _, err = u.GetEventMailSettings(ctx, eventID); err != nil {
		return EventSettingsView{}, err
	}
	if err = u.mail.SaveEventIdentity(ctx, eventID, in, by, u.now().UTC()); err != nil {
		return EventSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save event sender").Err()
	}
	return u.GetEventMailSettings(ctx, eventID)
}

func (u *MailUseCase) UpdateEventSMTP(ctx context.Context, eventID uuid.UUID, in mailModel.SMTPInput, by uuid.UUID) (EventSettingsView, error) {
	in, err := in.Normalize()
	if err != nil {
		return EventSettingsView{}, err
	}
	if _, err = u.GetEventMailSettings(ctx, eventID); err != nil {
		return EventSettingsView{}, err
	}
	existing, ok, err := u.mail.EventSMTP(ctx, eventID)
	if err != nil {
		return EventSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load event SMTP settings").Err()
	}
	cfg, err := u.applyInput(existing, ok, &eventID, in, by)
	if err != nil {
		return EventSettingsView{}, err
	}
	if _, err = u.mail.SaveEventSMTP(ctx, cfg); err != nil {
		return EventSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save event SMTP settings").Err()
	}
	return u.GetEventMailSettings(ctx, eventID)
}

func (u *MailUseCase) DeleteEventSMTP(ctx context.Context, eventID uuid.UUID) (EventSettingsView, error) {
	if err := u.mail.DeleteEventSMTP(ctx, eventID); err != nil {
		return EventSettingsView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to delete event SMTP settings").Err()
	}
	return u.GetEventMailSettings(ctx, eventID)
}

// TestEventSMTP sends a test email as the Event through the form values
// (in != nil) or the stored Event transport. No platform fallback: the point
// is to check the Event transport itself.
func (u *MailUseCase) TestEventSMTP(ctx context.Context, eventID uuid.UUID, in *mailModel.SMTPInput, userID uuid.UUID) (TestResult, error) {
	res, err := u.testEventSMTP(ctx, eventID, in, userID)
	if err == nil {
		u.journalTest(ctx, userID, &eventID, dispatchModel.TransportEvent, res)
	}
	return res, err
}

func (u *MailUseCase) testEventSMTP(ctx context.Context, eventID uuid.UUID, in *mailModel.SMTPInput, userID uuid.UUID) (TestResult, error) {
	recipient, err := u.recipient(ctx, userID)
	if err != nil {
		return TestResult{}, err
	}
	platform, err := u.platformRoute(ctx)
	if err != nil {
		return TestResult{}, err
	}
	stored, route, identity, err := u.eventRoute(ctx, eventID, platform)
	if err != nil {
		return TestResult{Recipient: recipient, Error: err.Error()}, nil
	}
	t := stored
	if in != nil {
		normalized, err := in.Normalize()
		if err != nil {
			return TestResult{}, err
		}
		existing, ok, err := u.mail.EventSMTP(ctx, eventID)
		if err != nil {
			return TestResult{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load event SMTP settings").Err()
		}
		if t, err = u.formTransport(existing, ok, normalized); err != nil {
			return TestResult{}, err
		}
	} else if !route {
		return TestResult{Recipient: recipient, Error: "Event SMTP is not configured"}, nil
	}
	t.source = dispatchModel.TransportEvent
	return u.testSend(ctx, t, identity, recipient, "заходу"), nil
}

// ListEventMailJournal is the delivery journal restricted to one Event.
func (u *MailUseCase) ListEventMailJournal(ctx context.Context, eventID uuid.UUID, f dispatchModel.ListDispatchesFilter) ([]dispatchModel.DispatchDetail, int64, error) {
	f.Event = eventID.String()
	// An Event knows two routes, its own SMTP and the platform: a delivery
	// through the server-config fallback counts as the platform, in the filter
	// and in the rows.
	if f.Transport == dispatchModel.TransportPlatform || f.Transport == dispatchModel.TransportEnv {
		f.Transport = dispatchModel.TransportPlatform + "," + dispatchModel.TransportEnv
	}
	rows, total, err := u.dispatches.List(ctx, f)
	if err != nil {
		return nil, 0, model.ErrPlatform.WithError(err).WithMessage("Failed to list event mail journal").Err()
	}
	for i := range rows {
		for j := range rows[i].Targets {
			if rows[i].Targets[j].Transport == dispatchModel.TransportEnv {
				rows[i].Targets[j].Transport = dispatchModel.TransportPlatform
			}
		}
	}
	return rows, total, nil
}

// --- helpers ---

// applyInput builds the row to save: the id of an existing row is kept (the
// password ciphertext is bound to it), the stored password is kept unless a
// new one is given or it is cleared.
func (u *MailUseCase) applyInput(existing mailModel.SMTPConfig, ok bool, eventID *uuid.UUID, in mailModel.SMTPInput, by uuid.UUID) (mailModel.SMTPConfig, error) {
	cfg := mailModel.SMTPConfig{ID: tools.NewUUIDv7()}
	if ok {
		cfg.ID, cfg.PasswordCiphertext = existing.ID, existing.PasswordCiphertext
	}
	if in.ClearPassword {
		cfg.PasswordCiphertext = ""
	}
	if in.Password != "" {
		sealed, err := u.seal(cfg.ID, in.Password)
		if err != nil {
			return mailModel.SMTPConfig{}, err
		}
		cfg.PasswordCiphertext = sealed
	}
	cfg.ScopeEventID = eventID
	cfg.Host, cfg.Port, cfg.TLSMode, cfg.Username = in.Host, in.Port, in.TLSMode, in.Username
	cfg.MaxPerSecond, cfg.DailyQuota = in.MaxPerSecond, in.DailyQuota
	cfg.UpdatedBy, cfg.UpdatedAt = &by, u.now().UTC()
	return cfg, nil
}

// applyProviderInput builds the provider row to save. The id and password
// ciphertext of an existing provider are kept (the ciphertext is bound to the
// id); a new password replaces it, ClearPassword removes it.
func (u *MailUseCase) applyProviderInput(existing mailModel.SMTPConfig, ok bool, in mailModel.ProviderInput, by uuid.UUID) (mailModel.SMTPConfig, error) {
	cfg, err := u.applyInput(existing, ok, nil, in.SMTPInput, by)
	if err != nil {
		return cfg, err
	}
	cfg.Name, cfg.Identity = in.Name, in.Identity
	switch {
	case in.Enabled != nil:
		cfg.Enabled = *in.Enabled
	case ok:
		cfg.Enabled = existing.Enabled
	default:
		cfg.Enabled = true
	}
	return cfg, nil
}

// providerWriteError keeps a domain error from the repository (a taken name)
// and wraps the rest.
func providerWriteError(err error, message string) error {
	if _, ok := errors.AsType[liberr.Error](err); ok {
		return err
	}
	return model.ErrPlatform.WithError(err).WithMessage(message).Err()
}

// formTransport is the connection described by unsaved form values; an empty
// password falls back to the stored one unless it is being cleared.
func (u *MailUseCase) formTransport(existing mailModel.SMTPConfig, ok bool, in mailModel.SMTPInput) (transport, error) {
	cfg := mailModel.SMTPConfig{Host: in.Host, Port: in.Port, TLSMode: in.TLSMode, Username: in.Username}
	if ok && !in.ClearPassword {
		cfg.ID, cfg.PasswordCiphertext = existing.ID, existing.PasswordCiphertext
	}
	conn, err := u.connection(cfg, in.Password)
	if err != nil {
		return transport{}, err
	}
	return transport{conn: conn}, nil
}

func (u *MailUseCase) recipient(ctx context.Context, userID uuid.UUID) (string, error) {
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get current user").Err()
	}
	return user.Email, nil
}

// journalTest records a finished test send in the delivery journal as a
// dispatch of kind dispatchModel.TypeSMTPTest: the platform journal for
// scope == nil, that Event's journal otherwise. The dispatch belongs to the
// user who triggered the test (who is also its recipient). The test result
// matters more than its record, so a journal failure is not returned.
// fallbackTransport names the route when the send never got as far as
// resolving one (nothing configured).
func (u *MailUseCase) journalTest(ctx context.Context, userID uuid.UUID, scope *uuid.UUID, fallbackTransport string, res TestResult) {
	if res.Recipient == "" {
		return
	}
	transport := res.Transport
	if transport == "" {
		transport = fallbackTransport
	}
	target := dispatchRepo.TargetResult{
		Status: dispatchModel.TargetStatusDone, Attempts: 1,
		Note: dispatchModel.DeliveryNote{Transport: transport, Recipient: res.Recipient},
	}
	if !res.Sent {
		target.Status, target.Error = dispatchModel.TargetStatusError, res.Error
	}
	id := tools.NewUUIDv7()
	if err := u.dispatches.Create(ctx, id, dispatchModel.TypeSMTPTest, userID, scope); err != nil {
		return
	}
	if err := u.dispatches.UpsertTarget(ctx, id, notificationTypes.NotificationChannelEmail, target); err != nil {
		return
	}
	_ = u.dispatches.SetStatus(ctx, id, dispatchModel.DispatchStatusDone)
}

func (u *MailUseCase) testSend(ctx context.Context, t transport, identity mailModel.Identity, recipient, label string) TestResult {
	result := TestResult{Recipient: recipient, Transport: t.source}
	msg := withIdentity(email.Message{
		To:      recipient,
		Subject: "Перевірка пошти Cyber ICE Box",
		HTML:    "<p>Це тестовий лист: налаштування SMTP " + label + " працюють.</p>",
		Text:    "Це тестовий лист: налаштування SMTP " + label + " працюють.",
	}, identity)
	if err := u.send(ctx, t, msg); err != nil {
		result.Error = err.Error()
		return result
	}
	result.Sent = true
	return result
}
