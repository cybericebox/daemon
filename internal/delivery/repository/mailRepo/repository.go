// Package mailRepo stores SMTP transports and Event mail settings, and the
// narrow job-state queries of the Event mail emitters (reminder, finished,
// expired invitations): domain shapes in and out, sqlc rows only inside.
package mailRepo

import (
	"context"
	"errors"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/cybericebox/daemon/pkg/tools"
)

type Queries interface {
	ListPlatformSMTPProviders(ctx context.Context) ([]postgres.MailSmtpConfig, error)
	GetPlatformSMTPProvider(ctx context.Context, id uuid.UUID) (postgres.MailSmtpConfig, error)
	InsertPlatformSMTPProvider(ctx context.Context, arg postgres.InsertPlatformSMTPProviderParams) (postgres.MailSmtpConfig, error)
	UpdatePlatformSMTPProvider(ctx context.Context, arg postgres.UpdatePlatformSMTPProviderParams) (postgres.MailSmtpConfig, error)
	SetPlatformSMTPProviderEnabled(ctx context.Context, arg postgres.SetPlatformSMTPProviderEnabledParams) (postgres.MailSmtpConfig, error)
	DeletePlatformSMTPProvider(ctx context.Context, id uuid.UUID) (int64, error)
	ReorderPlatformSMTPProviders(ctx context.Context, ids []uuid.UUID) error
	ReservePlatformSMTPSend(ctx context.Context, arg postgres.ReservePlatformSMTPSendParams) (int32, error)
	ReleasePlatformSMTPSend(ctx context.Context, arg postgres.ReleasePlatformSMTPSendParams) error
	MarkPlatformSMTPUsed(ctx context.Context, arg postgres.MarkPlatformSMTPUsedParams) error
	GetEventSMTPConfig(ctx context.Context, scopeEventID uuid.NullUUID) (postgres.MailSmtpConfig, error)
	UpsertEventSMTPConfig(ctx context.Context, arg postgres.UpsertEventSMTPConfigParams) (postgres.MailSmtpConfig, error)
	DeleteEventSMTPConfig(ctx context.Context, scopeEventID uuid.NullUUID) error
	GetPlatformMailIdentity(ctx context.Context) (postgres.MailIdentity, error)
	GetEventMailIdentity(ctx context.Context, scopeEventID uuid.NullUUID) (postgres.MailIdentity, error)
	UpsertPlatformMailIdentity(ctx context.Context, arg postgres.UpsertPlatformMailIdentityParams) (postgres.MailIdentity, error)
	UpsertEventMailIdentity(ctx context.Context, arg postgres.UpsertEventMailIdentityParams) (postgres.MailIdentity, error)
	SetPlatformMailFooter(ctx context.Context, arg postgres.SetPlatformMailFooterParams) error
	ListEventsDueStartReminder(ctx context.Context, nowAt time.Time) ([]postgres.ListEventsDueStartReminderRow, error)
	MarkEventStartReminderSent(ctx context.Context, arg postgres.MarkEventStartReminderSentParams) error
	ListEventsDueFinishedNotice(ctx context.Context, nowAt pgtype.Timestamptz) ([]postgres.ListEventsDueFinishedNoticeRow, error)
	MarkEventFinishedNotified(ctx context.Context, arg postgres.MarkEventFinishedNotifiedParams) error
	ListInvitationExpiryCandidates(ctx context.Context, nowAt time.Time) ([]postgres.ListInvitationExpiryCandidatesRow, error)
	MarkInvitationExpiredNotified(ctx context.Context, arg postgres.MarkInvitationExpiredNotifiedParams) (int64, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

// PlatformProviders returns every platform SMTP provider, by priority.
func (r *Repository) PlatformProviders(ctx context.Context) ([]mailModel.SMTPConfig, error) {
	rows, err := r.q.ListPlatformSMTPProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]mailModel.SMTPConfig, 0, len(rows))
	for _, row := range rows {
		out = append(out, toSMTP(row))
	}
	return out, nil
}

// PlatformProvider returns one provider; ok=false when it does not exist.
func (r *Repository) PlatformProvider(ctx context.Context, id uuid.UUID) (mailModel.SMTPConfig, bool, error) {
	return found(r.q.GetPlatformSMTPProvider(ctx, id))
}

// CreateProvider stores a new provider at the end of the priority order; a
// taken name is mailModel.ErrProviderNameTaken.
func (r *Repository) CreateProvider(ctx context.Context, c mailModel.SMTPConfig) (mailModel.SMTPConfig, error) {
	row, err := r.q.InsertPlatformSMTPProvider(ctx, postgres.InsertPlatformSMTPProviderParams{
		ID: c.ID, Name: c.Name, Enabled: c.Enabled, Host: c.Host, Port: int32(c.Port), TlsMode: string(c.TLSMode),
		Username: c.Username, PasswordCiphertext: c.PasswordCiphertext,
		FromName: c.Identity.FromName, FromAddress: c.Identity.FromAddress,
		ReplyToName: c.Identity.ReplyToName, ReplyToAddress: c.Identity.ReplyToAddress,
		MaxPerSecond: float8(c.MaxPerSecond), DailyQuota: int4(c.DailyQuota),
		UpdatedBy: nullable(c.UpdatedBy), UpdatedAt: c.UpdatedAt,
	})
	if err != nil {
		return mailModel.SMTPConfig{}, nameTaken(err)
	}
	return toSMTP(row), nil
}

// UpdateProvider rewrites the editable fields of a provider (not its priority
// or counters); ok=false when it does not exist.
func (r *Repository) UpdateProvider(ctx context.Context, c mailModel.SMTPConfig) (mailModel.SMTPConfig, bool, error) {
	row, err := r.q.UpdatePlatformSMTPProvider(ctx, postgres.UpdatePlatformSMTPProviderParams{
		ID: c.ID, Name: c.Name, Enabled: c.Enabled, Host: c.Host, Port: int32(c.Port), TlsMode: string(c.TLSMode),
		Username: c.Username, PasswordCiphertext: c.PasswordCiphertext,
		FromName: c.Identity.FromName, FromAddress: c.Identity.FromAddress,
		ReplyToName: c.Identity.ReplyToName, ReplyToAddress: c.Identity.ReplyToAddress,
		MaxPerSecond: float8(c.MaxPerSecond), DailyQuota: int4(c.DailyQuota),
		UpdatedBy: nullable(c.UpdatedBy), UpdatedAt: c.UpdatedAt,
	})
	cfg, ok, err := found(row, err)
	return cfg, ok, nameTaken(err)
}

func (r *Repository) SetProviderEnabled(ctx context.Context, id uuid.UUID, enabled bool, by uuid.UUID, now time.Time) (mailModel.SMTPConfig, bool, error) {
	return found(r.q.SetPlatformSMTPProviderEnabled(ctx, postgres.SetPlatformSMTPProviderEnabledParams{
		ID: id, Enabled: enabled, UpdatedBy: nullable(&by), UpdatedAt: now,
	}))
}

// DeleteProvider removes a provider; false when it did not exist.
func (r *Repository) DeleteProvider(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q.DeletePlatformSMTPProvider(ctx, id)
	return n > 0, err
}

// ReorderProviders sets the priority of each provider to its position in ids.
func (r *Repository) ReorderProviders(ctx context.Context, ids []uuid.UUID) error {
	return r.q.ReorderPlatformSMTPProviders(ctx, ids)
}

// ReserveSend claims one message of the provider's allowance for the UTC day
// of now; false when its daily limit is used up.
func (r *Repository) ReserveSend(ctx context.Context, id uuid.UUID, now time.Time) (bool, error) {
	_, err := r.q.ReservePlatformSMTPSend(ctx, postgres.ReservePlatformSMTPSendParams{Day: date(now), ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ReleaseSend returns a claimed message that was not sent. A non-empty
// providerError is kept as the provider's last error.
func (r *Repository) ReleaseSend(ctx context.Context, id uuid.UUID, now time.Time, providerError string) error {
	return r.q.ReleasePlatformSMTPSend(ctx, postgres.ReleasePlatformSMTPSendParams{
		Day: date(now), Error: providerError, At: now, ID: id,
	})
}

// MarkUsed records a delivered message.
func (r *Repository) MarkUsed(ctx context.Context, id uuid.UUID, now time.Time) error {
	return r.q.MarkPlatformSMTPUsed(ctx, postgres.MarkPlatformSMTPUsedParams{At: now, ID: id})
}

// EventSMTP returns the Event transport; ok=false when the Event uses the
// platform one.
func (r *Repository) EventSMTP(ctx context.Context, eventID uuid.UUID) (mailModel.SMTPConfig, bool, error) {
	return found(r.q.GetEventSMTPConfig(ctx, uuid.NullUUID{UUID: eventID, Valid: true}))
}

// SaveEventSMTP upserts the Event transport (c.ScopeEventID is set).
func (r *Repository) SaveEventSMTP(ctx context.Context, c mailModel.SMTPConfig) (mailModel.SMTPConfig, error) {
	row, err := r.q.UpsertEventSMTPConfig(ctx, postgres.UpsertEventSMTPConfigParams{
		ID: c.ID, ScopeEventID: nullable(c.ScopeEventID), Host: c.Host, Port: int32(c.Port),
		TlsMode: string(c.TLSMode), Username: c.Username, PasswordCiphertext: c.PasswordCiphertext,
		UpdatedBy: nullable(c.UpdatedBy), UpdatedAt: c.UpdatedAt,
		MaxPerSecond: float8(c.MaxPerSecond), DailyQuota: int4(c.DailyQuota),
	})
	if err != nil {
		return mailModel.SMTPConfig{}, err
	}
	return toSMTP(row), nil
}

func (r *Repository) DeleteEventSMTP(ctx context.Context, eventID uuid.UUID) error {
	return r.q.DeleteEventSMTPConfig(ctx, uuid.NullUUID{UUID: eventID, Valid: true})
}

// PlatformIdentity returns the stored platform sender; empty when none saved.
func (r *Repository) PlatformIdentity(ctx context.Context) (mailModel.Identity, error) {
	row, err := r.q.GetPlatformMailIdentity(ctx)
	return toIdentity(row, err)
}

// EventIdentity returns the Event's own sender overrides; empty when none.
func (r *Repository) EventIdentity(ctx context.Context, eventID uuid.UUID) (mailModel.Identity, error) {
	row, err := r.q.GetEventMailIdentity(ctx, uuid.NullUUID{UUID: eventID, Valid: true})
	return toIdentity(row, err)
}

// PlatformSettings returns everything saved for the platform mail identity
// from one read; empty fields where nothing is saved.
func (r *Repository) PlatformSettings(ctx context.Context) (mailModel.PlatformSettings, error) {
	row, err := r.q.GetPlatformMailIdentity(ctx)
	id, err := toIdentity(row, err)
	if err != nil {
		return mailModel.PlatformSettings{}, err
	}
	return mailModel.PlatformSettings{
		Identity: id, SendingDomain: row.SendingDomain,
		FooterContent: row.FooterContent, FooterLegacy: row.FooterText,
	}, nil
}

// SavePlatformIdentity upserts the platform sender and sending domain.
func (r *Repository) SavePlatformIdentity(ctx context.Context, id mailModel.Identity, sendingDomain string, by uuid.UUID, now time.Time) error {
	_, err := r.q.UpsertPlatformMailIdentity(ctx, postgres.UpsertPlatformMailIdentityParams{
		ID: tools.NewUUIDv7(), FromName: id.FromName, FromAddress: id.FromAddress,
		ReplyToName: id.ReplyToName, ReplyToAddress: id.ReplyToAddress, SendingDomain: sendingDomain,
		UpdatedBy: uuid.NullUUID{UUID: by, Valid: true}, UpdatedAt: now,
	})
	return err
}

// SaveEventIdentity upserts the Event's own sender overrides.
func (r *Repository) SaveEventIdentity(ctx context.Context, eventID uuid.UUID, id mailModel.Identity, by uuid.UUID, now time.Time) error {
	_, err := r.q.UpsertEventMailIdentity(ctx, postgres.UpsertEventMailIdentityParams{
		ID: tools.NewUUIDv7(), ScopeEventID: nullable(&eventID), FromName: id.FromName, FromAddress: id.FromAddress,
		ReplyToName: id.ReplyToName, ReplyToAddress: id.ReplyToAddress,
		UpdatedBy: uuid.NullUUID{UUID: by, Valid: true}, UpdatedAt: now,
	})
	return err
}

// SaveFooter stores the platform footer document (nil: the built-in default),
// keeping the sender. It also retires a footer saved in the older text form.
func (r *Repository) SaveFooter(ctx context.Context, content []byte, by uuid.UUID, now time.Time) error {
	return r.q.SetPlatformMailFooter(ctx, postgres.SetPlatformMailFooterParams{
		ID: tools.NewUUIDv7(), FooterContent: content, UpdatedBy: uuid.NullUUID{UUID: by, Valid: true}, UpdatedAt: now,
	})
}

func toIdentity(row postgres.MailIdentity, err error) (mailModel.Identity, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return mailModel.Identity{}, nil
	}
	if err != nil {
		return mailModel.Identity{}, err
	}
	return mailModel.Identity{
		FromName: row.FromName, FromAddress: row.FromAddress,
		ReplyToName: row.ReplyToName, ReplyToAddress: row.ReplyToAddress,
	}, nil
}

// DueEvent is an Event whose reminder or finish notice is due at At (the
// start or effective finish it is announced for).
type DueEvent struct {
	ID   uuid.UUID
	Tag  string
	Name string
	At   time.Time
	// Days is the reminder lead time (start reminders only).
	Days int
}

func (r *Repository) DueStartReminders(ctx context.Context, now time.Time) ([]DueEvent, error) {
	rows, err := r.q.ListEventsDueStartReminder(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]DueEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, DueEvent{ID: row.ID, Tag: row.Tag, Name: row.Name, At: row.StartAt, Days: int(row.DaysBeforeStart)})
	}
	return out, nil
}

func (r *Repository) MarkStartReminderSent(ctx context.Context, eventID uuid.UUID, startAt, now time.Time) error {
	return r.q.MarkEventStartReminderSent(ctx, postgres.MarkEventStartReminderSentParams{
		EventID: eventID, StartReminderSentFor: pgtype.Timestamptz{Time: startAt, Valid: true}, UpdatedAt: now,
	})
}

func (r *Repository) DueFinishedNotices(ctx context.Context, now time.Time) ([]DueEvent, error) {
	rows, err := r.q.ListEventsDueFinishedNotice(ctx, pgtype.Timestamptz{Time: now, Valid: true})
	if err != nil {
		return nil, err
	}
	out := make([]DueEvent, 0, len(rows))
	for _, row := range rows {
		out = append(out, DueEvent{ID: row.ID, Tag: row.Tag, Name: row.Name, At: row.FinishedAt})
	}
	return out, nil
}

func (r *Repository) MarkFinishedNotified(ctx context.Context, eventID uuid.UUID, finishedAt, now time.Time) error {
	return r.q.MarkEventFinishedNotified(ctx, postgres.MarkEventFinishedNotifiedParams{
		EventID: eventID, FinishedNotifiedFor: pgtype.Timestamptz{Time: finishedAt, Valid: true}, UpdatedAt: now,
	})
}

// ExpiryCandidate is a pending invitation of a started Event that was not yet
// announced as expired.
type ExpiryCandidate struct {
	EventID       uuid.UUID
	UserID        uuid.UUID
	InvitedToTeam bool
}

func (r *Repository) InvitationExpiryCandidates(ctx context.Context, now time.Time) ([]ExpiryCandidate, error) {
	rows, err := r.q.ListInvitationExpiryCandidates(ctx, now)
	if err != nil {
		return nil, err
	}
	out := make([]ExpiryCandidate, 0, len(rows))
	for _, row := range rows {
		out = append(out, ExpiryCandidate{EventID: row.EventID, UserID: row.UserID, InvitedToTeam: row.InvitedToTeam})
	}
	return out, nil
}

// MarkInvitationExpiredNotified claims the invitation; false when another
// pass already did.
func (r *Repository) MarkInvitationExpiredNotified(ctx context.Context, eventID, userID uuid.UUID, now time.Time) (bool, error) {
	n, err := r.q.MarkInvitationExpiredNotified(ctx, postgres.MarkInvitationExpiredNotifiedParams{
		EventID: eventID, UserID: userID, InvitationExpiredNotifiedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
	return n > 0, err
}

func found(row postgres.MailSmtpConfig, err error) (mailModel.SMTPConfig, bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return mailModel.SMTPConfig{}, false, nil
	}
	if err != nil {
		return mailModel.SMTPConfig{}, false, err
	}
	return toSMTP(row), true, nil
}

func toSMTP(row postgres.MailSmtpConfig) mailModel.SMTPConfig {
	c := mailModel.SMTPConfig{
		ID: row.ID, Host: row.Host, Port: int(row.Port), TLSMode: mailModel.TLSMode(row.TlsMode),
		Username: row.Username, PasswordCiphertext: row.PasswordCiphertext, UpdatedAt: row.UpdatedAt,
	}
	if row.ScopeEventID.Valid {
		c.ScopeEventID = new(row.ScopeEventID.UUID)
	}
	if row.UpdatedBy.Valid {
		c.UpdatedBy = new(row.UpdatedBy.UUID)
	}
	if row.MaxPerSecond.Valid {
		c.MaxPerSecond = new(row.MaxPerSecond.Float64)
	}
	if row.DailyQuota.Valid {
		c.DailyQuota = new(int(row.DailyQuota.Int32))
	}
	c.Name, c.Priority, c.Enabled, c.CreatedAt = row.Name, int(row.Priority), row.Enabled, row.CreatedAt
	c.Identity = mailModel.Identity{
		FromName: row.FromName, FromAddress: row.FromAddress,
		ReplyToName: row.ReplyToName, ReplyToAddress: row.ReplyToAddress,
	}
	c.Usage = mailModel.Usage{SentToday: int(row.SentToday), LastError: row.LastError}
	if row.UsageDay.Valid {
		c.Usage.Day = row.UsageDay.Time
	}
	if row.LastUsedAt.Valid {
		c.Usage.LastUsedAt = new(row.LastUsedAt.Time)
	}
	if row.LastErrorAt.Valid {
		c.Usage.LastErrorAt = new(row.LastErrorAt.Time)
	}
	return c
}

// date is the UTC calendar day of t, the usage counter day.
func date(t time.Time) pgtype.Date {
	return pgtype.Date{Time: mailModel.Today(t), Valid: true}
}

// nameTaken maps the unique provider name violation to its domain error.
func nameTaken(err error) error {
	if err == nil {
		return nil
	}
	if creator, ok := repositoryTools.UniqueViolationError(err, mailModel.ErrProviderNameTaken); ok {
		return creator.Err()
	}
	return err
}

func float8(v *float64) pgtype.Float8 {
	if v == nil {
		return pgtype.Float8{}
	}
	return pgtype.Float8{Float64: *v, Valid: true}
}

func int4(v *int) pgtype.Int4 {
	if v == nil {
		return pgtype.Int4{}
	}
	return pgtype.Int4{Int32: int32(*v), Valid: true}
}

func nullable(v *uuid.UUID) uuid.NullUUID {
	if v == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *v, Valid: true}
}
