package mailUseCase

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	"github.com/cybericebox/daemon/pkg/email"
	"github.com/cybericebox/daemon/pkg/secret"
)

// fakeSMTP records every send per host and fails the hosts listed in fail.
type fakeSMTP struct {
	fail  map[string]error
	sends []fakeSend
}

type fakeSend struct {
	conn email.Config
	msg  email.Message
}

type fakeClient struct {
	owner *fakeSMTP
	conn  email.Config
}

func (c fakeClient) SendMessage(_ context.Context, msg email.Message) error {
	c.owner.sends = append(c.owner.sends, fakeSend{conn: c.conn, msg: msg})
	return c.owner.fail[c.conn.Host]
}

// newTestUseCase is newBareTestUseCase with the provider daily counters stubbed
// to always have room.
func newTestUseCase(t *testing.T, env config.SMTPConfig, cipher *secret.Cipher) (*MailUseCase, *postgresMocks.MockQuerier, *fakeSMTP) {
	t.Helper()
	uc, repo, smtp := newBareTestUseCase(t, env, cipher)
	expectProviderSends(repo)
	return uc, repo, smtp
}

func newBareTestUseCase(t *testing.T, env config.SMTPConfig, cipher *secret.Cipher) (*MailUseCase, *postgresMocks.MockQuerier, *fakeSMTP) {
	t.Helper()
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	smtp := &fakeSMTP{fail: map[string]error{}}
	uc := NewMailUseCase(Dependencies{
		Repo: repo, Cipher: cipher, Env: env, Domain: "cybericebox.com", SupportEmail: "support@cybericebox.com",
		NewSender: func(cfg email.Config) (sender, error) { return fakeClient{owner: smtp, conn: cfg}, nil },
	})
	uc.now = func() time.Time { return time.Date(2026, 9, 29, 12, 0, 0, 0, time.UTC) }
	return uc, repo, smtp
}

var platformRow = postgres.MailSmtpConfig{
	ID: uuid.Must(uuid.NewV7()), Name: "Brevo", Enabled: true, Host: "smtp-relay.brevo.com", Port: 587, TlsMode: "starttls",
}

var platformIdentityRow = postgres.MailIdentity{
	FromName: "CyberICEBox", FromAddress: "notifications@mail.cybericebox.com", ReplyToAddress: "support@cybericebox.com",
}

// expectProviderSends stubs the daily counter of every stored provider: a send
// finds room and is recorded.
func expectProviderSends(repo *postgresMocks.MockQuerier) {
	repo.EXPECT().ReservePlatformSMTPSend(gomock.Any(), gomock.Any()).Return(int32(1), nil).AnyTimes()
	repo.EXPECT().MarkPlatformSMTPUsed(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
}

// expectPlatformIdentity stubs the saved platform sender for any number of reads.
func expectPlatformIdentity(repo *postgresMocks.MockQuerier, row *postgres.MailIdentity) {
	if row == nil {
		repo.EXPECT().GetPlatformMailIdentity(gomock.Any()).Return(postgres.MailIdentity{}, pgx.ErrNoRows).AnyTimes()
		return
	}
	repo.EXPECT().GetPlatformMailIdentity(gomock.Any()).Return(*row, nil).AnyTimes()
}

func expectEvent(repo *postgresMocks.MockQuerier, eventID uuid.UUID, own postgres.MailIdentity, eventSMTP *postgres.MailSmtpConfig) {
	repo.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "olymp", Name: "Кібер Олімпіада"}, nil)
	repo.EXPECT().GetEventMailIdentity(gomock.Any(), uuid.NullUUID{UUID: eventID, Valid: true}).Return(own, nil)
	if eventSMTP == nil {
		repo.EXPECT().GetEventSMTPConfig(gomock.Any(), uuid.NullUUID{UUID: eventID, Valid: true}).Return(postgres.MailSmtpConfig{}, pgx.ErrNoRows)
	} else {
		repo.EXPECT().GetEventSMTPConfig(gomock.Any(), uuid.NullUUID{UUID: eventID, Valid: true}).Return(*eventSMTP, nil)
	}
}

func TestDeliver_EventMailUsesEventSMTPAndSender(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	expectEvent(repo, eventID, postgres.MailIdentity{ReplyToAddress: "org@uni.edu"}, &postgres.MailSmtpConfig{ID: uuid.Must(uuid.NewV7()), Host: "email-smtp.eu-north-1.amazonaws.com", Port: 587, TlsMode: "starttls"})

	note := &dispatchModel.DeliveryNote{}
	err := uc.Deliver(dispatchModel.WithDeliveryNote(context.Background(), note), &eventID, email.Message{To: "p@example.org"})
	require.NoError(t, err)
	require.Len(t, smtp.sends, 1)
	sent := smtp.sends[0]
	require.Equal(t, "email-smtp.eu-north-1.amazonaws.com", sent.conn.Host)
	require.Equal(t, email.Address{Name: "Кібер Олімпіада", Email: "olymp@mail.cybericebox.com"}, sent.msg.From)
	require.Equal(t, "org@uni.edu", sent.msg.ReplyTo.Email)
	require.Equal(t, dispatchModel.DeliveryNote{Transport: "event", Recipient: "p@example.org"}, *note)
}

func TestDeliver_EventSMTPFailureFallsBackToPlatform(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	expectEvent(repo, eventID, postgres.MailIdentity{}, &postgres.MailSmtpConfig{ID: uuid.Must(uuid.NewV7()), Host: "event.smtp", Port: 587, TlsMode: "starttls"})
	smtp.fail["event.smtp"] = errors.New("535 authentication failed")

	note := &dispatchModel.DeliveryNote{}
	err := uc.Deliver(dispatchModel.WithDeliveryNote(context.Background(), note), &eventID, email.Message{To: "p@example.org"})
	require.NoError(t, err)
	require.Len(t, smtp.sends, 2)
	require.Equal(t, "smtp-relay.brevo.com", smtp.sends[1].conn.Host)
	require.Equal(t, "olymp@mail.cybericebox.com", smtp.sends[1].msg.From.Email, "fallback keeps the Event sender")
	require.Equal(t, "support@cybericebox.com", smtp.sends[1].msg.ReplyTo.Email, "no contact → platform Reply-To")
	require.Equal(t, "platform", note.Transport)
	require.Equal(t, "535 authentication failed", note.FallbackError)
	require.Equal(t, int32(1), note.ExtraAttempts)
}

func TestDeliver_EnvTransportIsUsedWhenNoProvidersAreSaved(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderName: "CyberICEBox", SenderEmail: "notifications@cybericebox.com"}
	uc, repo, smtp := newTestUseCase(t, env, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil)
	expectPlatformIdentity(repo, nil)

	note := &dispatchModel.DeliveryNote{}
	require.NoError(t, uc.Deliver(dispatchModel.WithDeliveryNote(context.Background(), note), nil, email.Message{To: "u@example.org"}))
	require.Len(t, smtp.sends, 1)
	require.Equal(t, "env.smtp", smtp.sends[0].conn.Host)
	require.Equal(t, email.Address{Name: "CyberICEBox", Email: "notifications@cybericebox.com"}, smtp.sends[0].msg.From)
	require.Equal(t, "env", note.Transport)
}

func TestDeliver_NothingConfigured(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil)
	expectPlatformIdentity(repo, nil)
	require.ErrorIs(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}), ErrNotConfigured)
	require.Empty(t, smtp.sends)
}

func TestPlatformProvider_SealsKeepsAndClearsPassword(t *testing.T) {
	cipher, err := secret.New("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	require.NoError(t, err)
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, cipher)
	by := uuid.Must(uuid.NewV7())
	input := mailModel.SMTPInput{Host: "smtp-relay.brevo.com", Port: 587, Username: "u", Password: "s3cret"}
	expectPlatformIdentity(repo, &platformIdentityRow)

	var created postgres.InsertPlatformSMTPProviderParams
	var updated postgres.UpdatePlatformSMTPProviderParams
	stored := func() postgres.MailSmtpConfig {
		row := postgres.MailSmtpConfig{ID: created.ID, Name: created.Name, Enabled: created.Enabled, Host: created.Host, Port: created.Port,
			TlsMode: created.TlsMode, Username: created.Username, PasswordCiphertext: created.PasswordCiphertext}
		if updated.ID != uuid.Nil {
			row.PasswordCiphertext, row.Enabled = updated.PasswordCiphertext, updated.Enabled
		}
		return row
	}
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).DoAndReturn(func(context.Context) ([]postgres.MailSmtpConfig, error) {
		if created.ID == uuid.Nil {
			return nil, nil
		}
		return []postgres.MailSmtpConfig{stored()}, nil
	}).AnyTimes()
	repo.EXPECT().GetPlatformSMTPProvider(gomock.Any(), gomock.Any()).DoAndReturn(func(context.Context, uuid.UUID) (postgres.MailSmtpConfig, error) {
		return stored(), nil
	}).AnyTimes()
	repo.EXPECT().InsertPlatformSMTPProvider(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.InsertPlatformSMTPProviderParams) (postgres.MailSmtpConfig, error) {
		created = p
		return postgres.MailSmtpConfig{}, nil
	})
	repo.EXPECT().UpdatePlatformSMTPProvider(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpdatePlatformSMTPProviderParams) (postgres.MailSmtpConfig, error) {
		updated = p
		return stored(), nil
	}).AnyTimes()

	view, err := uc.CreatePlatformProvider(context.Background(), mailModel.ProviderInput{SMTPInput: input, Name: "Brevo"}, by)
	require.NoError(t, err)
	require.Len(t, view.Providers, 1)
	require.True(t, view.Providers[0].PasswordSet)
	require.True(t, created.Enabled, "a new provider is enabled")
	require.Equal(t, "database", view.Source)
	require.Equal(t, "mail.cybericebox.com", view.SendingDomain)
	require.NotContains(t, created.PasswordCiphertext, "s3cret")

	// The stored password decrypts for delivery (row-bound context).
	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Equal(t, "s3cret", smtp.sends[0].conn.Password)

	// Empty password keeps the ciphertext and the enabled state.
	firstCiphertext := created.PasswordCiphertext
	input.Password = ""
	_, err = uc.UpdatePlatformProvider(context.Background(), created.ID, mailModel.ProviderInput{SMTPInput: input, Name: "Brevo"}, by)
	require.NoError(t, err)
	require.Equal(t, firstCiphertext, updated.PasswordCiphertext)
	require.True(t, updated.Enabled)

	// ClearPassword removes it.
	input.ClearPassword = true
	view, err = uc.UpdatePlatformProvider(context.Background(), created.ID, mailModel.ProviderInput{SMTPInput: input, Name: "Brevo"}, by)
	require.NoError(t, err)
	require.Empty(t, updated.PasswordCiphertext)
	require.False(t, view.Providers[0].PasswordSet)
}

func TestPlatformProvider_UnknownIDIsNotFound(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().GetPlatformSMTPProvider(gomock.Any(), gomock.Any()).Return(postgres.MailSmtpConfig{}, pgx.ErrNoRows)
	_, err := uc.UpdatePlatformProvider(context.Background(), uuid.Must(uuid.NewV7()),
		mailModel.ProviderInput{SMTPInput: mailModel.SMTPInput{Host: "h.example", Port: 587}, Name: "x"}, uuid.Must(uuid.NewV7()))
	require.ErrorIs(t, err, mailModel.ErrProviderNotFound.Err())

	repo.EXPECT().DeletePlatformSMTPProvider(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	_, err = uc.DeletePlatformProvider(context.Background(), uuid.Must(uuid.NewV7()))
	require.ErrorIs(t, err, mailModel.ErrProviderNotFound.Err())
}

func TestUpdateEventSMTP_PasswordNeedsSecretsKey(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF"}, nil)
	repo.EXPECT().GetEventMailIdentity(gomock.Any(), gomock.Any()).Return(postgres.MailIdentity{}, pgx.ErrNoRows)
	expectPlatformIdentity(repo, &platformIdentityRow)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	repo.EXPECT().GetEventSMTPConfig(gomock.Any(), gomock.Any()).Return(postgres.MailSmtpConfig{}, pgx.ErrNoRows).Times(2)

	_, err := uc.UpdateEventSMTP(context.Background(), eventID, mailModel.SMTPInput{Host: "email-smtp.eu-north-1.amazonaws.com", Port: 587, Password: "x"}, uuid.Must(uuid.NewV7()))
	require.ErrorIs(t, err, mailModel.ErrSecretsUnavailable.Err())
}

func TestDeliver_EventOwnSenderAndReplyToApplyWithNames(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	expectEvent(repo, eventID, postgres.MailIdentity{
		FromName: "Оргкомітет", FromAddress: "org@mail.cybericebox.com", ReplyToName: "Штаб", ReplyToAddress: "hq@uni.edu",
	}, nil)

	require.NoError(t, uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org"}))
	require.Equal(t, email.Address{Name: "Оргкомітет", Email: "org@mail.cybericebox.com"}, smtp.sends[0].msg.From)
	require.Equal(t, email.Address{Name: "Штаб", Email: "hq@uni.edu"}, smtp.sends[0].msg.ReplyTo)
}

func TestDeliver_PlatformSenderOverridesEnvFieldByField(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderName: "Env", SenderEmail: "env@cybericebox.com", ReplyToName: "EnvHelp", ReplyToEmail: "help@cybericebox.com"}
	uc, repo, smtp := newTestUseCase(t, env, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil)
	expectPlatformIdentity(repo, &postgres.MailIdentity{FromName: "Saved"})

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Equal(t, email.Address{Name: "Saved", Email: "env@cybericebox.com"}, smtp.sends[0].msg.From, "empty saved address falls back to env")
	require.Equal(t, email.Address{Name: "EnvHelp", Email: "help@cybericebox.com"}, smtp.sends[0].msg.ReplyTo)
}

func TestUpdatePlatformIdentity_SavesNormalizedAndRejectsBadInput(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	by := uuid.Must(uuid.NewV7())

	_, err := uc.UpdatePlatformIdentity(context.Background(), mailModel.Identity{FromName: strings.Repeat("x", 65)}, "", by)
	require.ErrorIs(t, err, mailModel.ErrIdentityInvalid.Err())
	_, err = uc.UpdatePlatformIdentity(context.Background(), mailModel.Identity{ReplyToAddress: "nope"}, "", by)
	require.ErrorIs(t, err, mailModel.ErrIdentityInvalid.Err())

	var saved postgres.UpsertPlatformMailIdentityParams
	repo.EXPECT().UpsertPlatformMailIdentity(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpsertPlatformMailIdentityParams) (postgres.MailIdentity, error) {
		saved = p
		return postgres.MailIdentity{}, nil
	})
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil)
	repo.EXPECT().GetPlatformMailIdentity(gomock.Any()).DoAndReturn(func(context.Context) (postgres.MailIdentity, error) {
		return postgres.MailIdentity{FromName: saved.FromName, FromAddress: saved.FromAddress, ReplyToName: saved.ReplyToName, ReplyToAddress: saved.ReplyToAddress}, nil
	})
	view, err := uc.UpdatePlatformIdentity(context.Background(), mailModel.Identity{FromName: " CIB ", FromAddress: "n@mail.cybericebox.com", ReplyToName: "Help", ReplyToAddress: "h@cybericebox.com"}, "", by)
	require.NoError(t, err)
	require.Equal(t, "CIB", saved.FromName)
	require.Equal(t, Party{Name: "CIB", Address: "n@mail.cybericebox.com"}, view.Identity.Sender)
	require.Equal(t, Party{Name: "Help", Address: "h@cybericebox.com"}, view.Effective.ReplyTo)
	require.Equal(t, "mail.cybericebox.com", view.SendingDomain)
	require.Empty(t, view.Providers)
}

func TestGetEventMailSettings_InheritedShowsPlatformDerivedValues(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	expectEvent(repo, eventID, postgres.MailIdentity{ReplyToAddress: "org@uni.edu"}, nil)

	view, err := uc.GetEventMailSettings(context.Background(), eventID)
	require.NoError(t, err)
	require.Equal(t, Party{Address: "org@uni.edu"}, view.Identity.ReplyTo)
	require.Empty(t, view.Identity.Sender, "own sender stays empty so it inherits")
	require.Equal(t, Party{Name: "Кібер Олімпіада", Address: "olymp@mail.cybericebox.com"}, view.Inherited.Sender)
	require.Equal(t, Party{Address: "support@cybericebox.com"}, view.Inherited.ReplyTo)
	require.True(t, view.PlatformConfigured)
}

func TestUpdatePlatformIdentity_SavesNormalizedSendingDomainAndRejectsBadOnes(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	by := uuid.Must(uuid.NewV7())

	for _, bad := range []string{"localhost", "a@b.co", "https://mail.example.com", "mail example.com"} {
		_, err := uc.UpdatePlatformIdentity(context.Background(), mailModel.Identity{}, bad, by)
		require.ErrorIs(t, err, mailModel.ErrSendingDomainInvalid.Err(), bad)
	}

	var saved postgres.UpsertPlatformMailIdentityParams
	repo.EXPECT().UpsertPlatformMailIdentity(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.UpsertPlatformMailIdentityParams) (postgres.MailIdentity, error) {
		saved = p
		return postgres.MailIdentity{}, nil
	})
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil)
	repo.EXPECT().GetPlatformMailIdentity(gomock.Any()).DoAndReturn(func(context.Context) (postgres.MailIdentity, error) {
		return postgres.MailIdentity{SendingDomain: saved.SendingDomain}, nil
	})
	view, err := uc.UpdatePlatformIdentity(context.Background(), mailModel.Identity{}, " Mail.CyberICEBox.com ", by)
	require.NoError(t, err)
	require.Equal(t, "mail.cybericebox.com", saved.SendingDomain)
	require.Equal(t, "mail.cybericebox.com", view.SendingDomain)
	require.Equal(t, "mail.cybericebox.com", view.SavedSendingDomain)
}

// The owner's case: SMTP_SENDER_EMAIL is on the apex, mail leaves from a subdomain.
func TestSavedSendingDomainOverridesTheEnvDomainForPlatformAndEvents(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderName: "Env", SenderEmail: "support@cybericebox.com"}
	uc, repo, smtp := newTestUseCase(t, env, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).Times(2)
	expectPlatformIdentity(repo, &postgres.MailIdentity{SendingDomain: "mail.cybericebox.com"})
	expectEvent(repo, eventID, postgres.MailIdentity{}, nil)

	require.NoError(t, uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org"}))
	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	event, platform := smtp.sends[0].msg, smtp.sends[1].msg
	require.Equal(t, "olymp@mail.cybericebox.com", event.From.Email, "Event sender uses the saved sending domain")
	require.Equal(t, "support@mail.cybericebox.com", platform.From.Email, "platform sender keeps the env mailbox on the saved domain")
}

func TestSavedSenderAddressStaysButEventsStillUseTheSendingDomain(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil).Times(2)
	expectPlatformIdentity(repo, &postgres.MailIdentity{FromAddress: "hello@cybericebox.com", SendingDomain: "mail.cybericebox.com"})
	expectEvent(repo, eventID, postgres.MailIdentity{}, nil)

	require.NoError(t, uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org"}))
	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Equal(t, "olymp@mail.cybericebox.com", smtp.sends[0].msg.From.Email)
	require.Equal(t, "hello@cybericebox.com", smtp.sends[1].msg.From.Email, "an explicit sender address is never rewritten")
}

func TestSendingDomainOnlyBuildsTheDefaultSenderAddress(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &postgres.MailIdentity{SendingDomain: "mail.cybericebox.com"})

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Equal(t, "notifications@mail.cybericebox.com", smtp.sends[0].msg.From.Email)
}

func TestGetPlatformMailSettings_TellsWhereEachValueComesFrom(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderName: "Env", SenderEmail: "support@cybericebox.com", ReplyToName: "EnvHelp"}
	uc, repo, _ := newTestUseCase(t, env, nil)
	uc.domain = "cybericebox.com"
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, &postgres.MailIdentity{FromName: "Saved", SendingDomain: "mail.cybericebox.com"})

	view, err := uc.GetPlatformMailSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, FieldSources{
		SenderName: FieldSaved, SenderAddress: FieldDerived, ReplyToName: FieldEnv,
		ReplyToAddress: FieldDefault, SendingDomain: FieldSaved,
	}, view.Sources)
	require.Equal(t, "mail.cybericebox.com", view.SendingDomain)
	require.Equal(t, "mail.cybericebox.com", view.SavedSendingDomain)
	require.Equal(t, "cybericebox.com", view.EnvSendingDomain, "the server config domain is the placeholder")
	require.Equal(t, "support@mail.cybericebox.com", view.Effective.Sender.Address)
}

func TestGetPlatformMailSettings_NothingSavedFallsBackToEnvThenDefaults(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "support@cybericebox.com"}
	uc, repo, _ := newTestUseCase(t, env, nil)
	uc.domain = "cybericebox.com"
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, nil)

	view, err := uc.GetPlatformMailSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, FieldSources{
		SenderName: FieldDefault, SenderAddress: FieldEnv, ReplyToName: FieldNone,
		ReplyToAddress: FieldDefault, SendingDomain: FieldEnv,
	}, view.Sources)
	require.Equal(t, "cybericebox.com", view.SendingDomain)
	require.Empty(t, view.SavedSendingDomain)

	uc.env, uc.domain, uc.support = config.SMTPConfig{}, "", ""
	view, err = uc.GetPlatformMailSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, FieldNone, view.Sources.SenderAddress)
	require.Equal(t, FieldNone, view.Sources.SendingDomain)
	require.Equal(t, FieldNone, view.Sources.ReplyToAddress)
	require.Empty(t, view.SendingDomain)
}

func TestListEventMailJournal_EnvDeliveriesCountAsPlatform(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	dispatchID := uuid.Must(uuid.NewV7())

	// The platform filter of an Event also matches the server-config route.
	repo.EXPECT().ListDispatches(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.ListDispatchesParams) ([]postgres.ListDispatchesRow, error) {
		require.Equal(t, "platform,env", p.TransportFilter)
		require.Equal(t, eventID.String(), p.EventFilter)
		return []postgres.ListDispatchesRow{{ID: dispatchID}}, nil
	})
	repo.EXPECT().CountDispatches(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.CountDispatchesParams) (int64, error) {
		require.Equal(t, "platform,env", p.TransportFilter)
		return 1, nil
	})
	repo.EXPECT().ListDispatchTargetsByDispatches(gomock.Any(), gomock.Any()).Return([]postgres.NotificationDispatchTarget{
		{DispatchID: dispatchID, Channel: "email", Status: "done", Transport: "env"},
	}, nil)

	rows, total, err := uc.ListEventMailJournal(context.Background(), eventID, dispatchModel.ListDispatchesFilter{Transport: "platform"})
	require.NoError(t, err)
	require.EqualValues(t, 1, total)
	require.Equal(t, "platform", rows[0].Targets[0].Transport, "an Event never sees the env route")
}

func TestListEventMailJournal_EventTransportFilterIsUntouched(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListDispatches(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, p postgres.ListDispatchesParams) ([]postgres.ListDispatchesRow, error) {
		require.Equal(t, "event", p.TransportFilter)
		return nil, nil
	})
	repo.EXPECT().CountDispatches(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	_, _, err := uc.ListEventMailJournal(context.Background(), uuid.Must(uuid.NewV7()), dispatchModel.ListDispatchesFilter{Transport: "event"})
	require.NoError(t, err)
}

func TestGetEventMailSettings_DefaultsComeFromTheSendingDomainWithSources(t *testing.T) {
	env := config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "support@cybericebox.com", ReplyToEmail: "help@cybericebox.com"}
	uc, repo, _ := newTestUseCase(t, env, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, &postgres.MailIdentity{SendingDomain: "mail.cybericebox.com"})
	expectEvent(repo, eventID, postgres.MailIdentity{}, nil)

	view, err := uc.GetEventMailSettings(context.Background(), eventID)
	require.NoError(t, err)
	require.Equal(t, Party{Name: "Кібер Олімпіада", Address: "olymp@mail.cybericebox.com"}, view.Inherited.Sender)
	require.Equal(t, Party{Address: "help@cybericebox.com"}, view.Inherited.ReplyTo)
	require.Equal(t, "mail.cybericebox.com", view.SendingDomain)
	require.Equal(t, FieldSources{
		SenderName: FieldEvent, SenderAddress: FieldDerived, ReplyToName: FieldNone,
		ReplyToAddress: FieldPlatform, SendingDomain: FieldPlatform,
	}, view.InheritedSources, "the env fallback shows as the platform")
}

func TestGetEventMailSettings_NoSendingDomainMeansNoDefaultSenderAddress(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, nil)
	expectEvent(repo, eventID, postgres.MailIdentity{}, nil)

	view, err := uc.GetEventMailSettings(context.Background(), eventID)
	require.NoError(t, err)
	require.Equal(t, Party{Name: "Кібер Олімпіада"}, view.Inherited.Sender, "never the platform address")
	require.Equal(t, FieldNone, view.InheritedSources.SenderAddress)
}

// The reported PoC: an event manager sets From to a platform mailbox (or another domain) and sends through the
// platform's own providers.
func TestDeliver_AnEventCannotSpoofThePlatformOrAnotherDomainThroughPlatformMail(t *testing.T) {
	for name, from := range map[string]string{
		"platform security mailbox": "security@mail.cybericebox.com",
		"platform support mailbox":  "Support@mail.cybericebox.com",
		"the platform sender":       "notifications@mail.cybericebox.com",
		"another domain":            "ceo@bank.example",
		"no domain":                 "ceo",
	} {
		uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
		eventID := uuid.Must(uuid.NewV7())
		repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
		expectPlatformIdentity(repo, &platformIdentityRow)
		expectEvent(repo, eventID, postgres.MailIdentity{FromName: "Оргкомітет", FromAddress: from, ReplyToAddress: "hq@uni.edu"}, nil)

		require.NoError(t, uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org"}), name)
		require.Equal(t, email.Address{Name: "Оргкомітет", Email: "olymp@mail.cybericebox.com"}, smtp.sends[0].msg.From, name)
		require.Equal(t, "hq@uni.edu", smtp.sends[0].msg.ReplyTo.Email, name+": the event keeps its Reply-To")
	}
}

func TestDeliver_AnEventKeepsItsOwnSenderThroughItsOwnSMTPButNotOnTheFallback(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	smtp.fail["smtp.uni.example"] = errors.New("event smtp down")
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	expectEvent(repo, eventID, postgres.MailIdentity{FromName: "Оргкомітет", FromAddress: "ctf@uni.edu"},
		&postgres.MailSmtpConfig{ID: uuid.Must(uuid.NewV7()), Host: "smtp.uni.example", Port: 587, TlsMode: "starttls"})

	require.NoError(t, uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org"}))
	require.Len(t, smtp.sends, 2)
	require.Equal(t, "ctf@uni.edu", smtp.sends[0].msg.From.Email, "through its own SMTP the event sends as it wishes")
	require.Equal(t, "olymp@mail.cybericebox.com", smtp.sends[1].msg.From.Email, "the platform fallback only sends as the event on the platform domain")
}
