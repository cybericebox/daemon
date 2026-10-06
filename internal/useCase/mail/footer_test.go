package mailUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/cybericebox/daemon/pkg/email"
)

func requireFooter(t *testing.T, msg email.Message, supportEmail string) {
	t.Helper()
	require.Contains(t, msg.HTML, `href="mailto:`+supportEmail+`"`)
	require.Contains(t, msg.HTML, `href="https://cybericebox.com/privacy"`)
	require.True(t, strings.HasSuffix(msg.Text, "—\n"+
		"Цей лист надіслано автоматично. З питань відповідайте на нього або пишіть на "+supportEmail+".\n"+
		"Cyber\u00a0ICE\u00a0Box · cybericebox.com · Політика конфіденційності: cybericebox.com/privacy"), msg.Text)
	require.Equal(t, 1, strings.Count(msg.HTML, "Cyber&nbsp;ICE&nbsp;Box ·"), "footer appended once")
}

func TestDeliver_PlatformMailEndsWithFooter(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org", HTML: "<p>Привіт</p>", Text: "Привіт"}))
	require.Len(t, smtp.sends, 1)
	sent := smtp.sends[0].msg
	require.True(t, strings.HasPrefix(sent.HTML, "<p>Привіт</p>"))
	require.True(t, strings.HasPrefix(sent.Text, "Привіт\n\n—\n"))
	requireFooter(t, sent, "support@cybericebox.com")
}

// The product name never breaks across lines: whatever a stored or edited
// template wrote, the sent parts join «Cyber ICE Box» with no-break spaces,
// (the From display name is a header, not rendered text, and keeps its spaces).
func TestDeliver_BrandNameNeverBreaksInSentParts(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{
		To: "u@example.org", HTML: "<p>Ласкаво просимо до Cyber ICE Box</p>", Text: "Ласкаво просимо до Cyber ICE Box",
	}))
	sent := smtp.sends[0].msg
	breakable := regexp.MustCompile(`Cyber\s+(ICE|Ice)\s+Box`)
	require.False(t, breakable.MatchString(sent.HTML), sent.HTML)
	require.False(t, breakable.MatchString(sent.Text), sent.Text)
	require.Contains(t, sent.HTML, "до Cyber&nbsp;ICE&nbsp;Box</p>")
	require.Contains(t, sent.Text, "до Cyber\u00a0ICE\u00a0Box")
}

func TestDeliver_EventMailFooterUsesEventReplyTo(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	expectEvent(repo, eventID, postgres.MailIdentity{ReplyToAddress: "org@uni.edu"}, nil)

	require.NoError(t, uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org", HTML: "<p>x</p>", Text: "x"}))
	require.Len(t, smtp.sends, 1)
	sent := smtp.sends[0].msg
	require.Equal(t, email.Address{Name: "Кібер Олімпіада", Email: "olymp@mail.cybericebox.com"}, sent.From)
	require.Equal(t, "org@uni.edu", sent.ReplyTo.Email)
	requireFooter(t, sent, "org@uni.edu")
}

func TestDeliver_ReplyToFallsBackToSupport(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil).Times(2)
	expectPlatformIdentity(repo, &postgres.MailIdentity{FromAddress: platformIdentityRow.FromAddress})
	expectEvent(repo, eventID, postgres.MailIdentity{}, nil)

	require.NoError(t, uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org"}))
	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org"}))
	require.Len(t, smtp.sends, 2)

	event, platform := smtp.sends[0].msg, smtp.sends[1].msg
	require.Equal(t, "support@cybericebox.com", event.ReplyTo.Email, "no contact, no platform Reply-To → SUPPORT_EMAIL")
	requireFooter(t, event, "support@cybericebox.com")
	require.Equal(t, email.Address{Name: "Cyber ICE Box", Email: "notifications@mail.cybericebox.com"}, platform.From)
	require.Equal(t, "support@cybericebox.com", platform.ReplyTo.Email)
	requireFooter(t, platform, "support@cybericebox.com")
}

func TestDeliver_EventFallbackAppendsFooterOnce(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)
	expectEvent(repo, eventID, postgres.MailIdentity{ReplyToAddress: "org@uni.edu"}, &postgres.MailSmtpConfig{ID: uuid.Must(uuid.NewV7()), Host: "event.smtp", Port: 587, TlsMode: "starttls"})
	smtp.fail["event.smtp"] = errors.New("535 authentication failed")

	require.NoError(t, uc.Deliver(context.Background(), &eventID, email.Message{To: "p@example.org", HTML: "<p>x</p>", Text: "x"}))
	require.Len(t, smtp.sends, 2)
	requireFooter(t, smtp.sends[0].msg, "org@uni.edu")
	requireFooter(t, smtp.sends[1].msg, "org@uni.edu")
}

func TestTestPlatformSMTP_EndsWithFooter(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	userID := uuid.Must(uuid.NewV7())
	repo.EXPECT().GetUserByID(gomock.Any(), userID).Return(postgres.User{ID: userID, Email: "admin@example.org"}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, nil)
	uc.env = config.SMTPConfig{Host: "env.smtp", Port: 587, SenderEmail: "notifications@mail.cybericebox.com"}
	repo.EXPECT().CreateDispatch(gomock.Any(), gomock.Any()).Return(postgres.NotificationDispatch{}, nil)
	repo.EXPECT().UpsertDispatchTarget(gomock.Any(), gomock.Any()).Return(nil)
	repo.EXPECT().SetDispatchStatus(gomock.Any(), gomock.Any()).Return(nil)

	res, err := uc.TestPlatformProvider(context.Background(), nil, nil, userID)
	require.NoError(t, err)
	require.True(t, res.Sent, res.Error)
	require.Len(t, smtp.sends, 1)
	requireFooter(t, smtp.sends[0].msg, "support@cybericebox.com")
}

func TestFooter_MatchesWhatSendAppends(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)

	footer, err := uc.Footer(context.Background(), nil)
	require.NoError(t, err)
	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org", HTML: "<p>x</p>", Text: "x"}))
	require.Equal(t, "<p>x</p>"+footer.HTML, smtp.sends[0].msg.HTML)
	require.Equal(t, "x\n\n"+footer.Text, smtp.sends[0].msg.Text)
}

func TestFooter_EventUsesOwnReplyToThenPlatformThenSupport(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	eventID := uuid.Must(uuid.NewV7())
	expectPlatformIdentity(repo, &postgres.MailIdentity{FromAddress: platformIdentityRow.FromAddress})
	repo.EXPECT().GetEventMailIdentity(gomock.Any(), gomock.Any()).Return(postgres.MailIdentity{ReplyToAddress: "org@uni.edu"}, nil)
	repo.EXPECT().GetEventMailIdentity(gomock.Any(), gomock.Any()).Return(postgres.MailIdentity{}, pgx.ErrNoRows)

	footer, err := uc.Footer(context.Background(), &eventID)
	require.NoError(t, err)
	require.Contains(t, footer.Text, "пишіть на org@uni.edu.")

	footer, err = uc.Footer(context.Background(), &eventID)
	require.NoError(t, err)
	require.Contains(t, footer.Text, "пишіть на support@cybericebox.com.")
}

func TestFooter_WithoutPlatformTransportStillUsesSupport(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, nil)

	footer, err := uc.Footer(context.Background(), nil)
	require.NoError(t, err)
	require.Contains(t, footer.Text, "пишіть на support@cybericebox.com.")
}

const savedFooterDoc = `{"root":{"type":"root","children":[{"type":"paragraph","children":[` +
	`{"type":"text","text":"Дякуємо! ","format":1},{"type":"variable","varName":"reply_to"},` +
	`{"type":"text","text":" · "},{"type":"variable","varName":"site_url"}]}]}}`

func TestDeliver_SavedFooterReplacesTheDefault(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	row := platformIdentityRow
	row.FooterContent = []byte(savedFooterDoc)
	expectPlatformIdentity(repo, &row)

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org", HTML: "<p>x</p>", Text: "x"}))
	sent := smtp.sends[0].msg
	require.True(t, strings.HasSuffix(sent.Text, "—\nДякуємо! support@cybericebox.com · cybericebox.com"), sent.Text)
	require.Contains(t, sent.HTML, "<strong>Дякуємо! </strong>", "formatting comes through the body renderer")
	require.Contains(t, sent.HTML, `href="mailto:support@cybericebox.com"`)
	require.NotContains(t, sent.HTML, "Політика")
}

func TestDeliver_FooterSavedAsTextIsStillRendered(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	row := platformIdentityRow
	row.FooterText = "Дякуємо! {reply_to} · [Політика]({privacy_url})"
	expectPlatformIdentity(repo, &row)

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org", HTML: "<p>x</p>", Text: "x"}))
	sent := smtp.sends[0].msg
	require.True(t, strings.HasSuffix(sent.Text, "—\nДякуємо! support@cybericebox.com · Політика (https://cybericebox.com/privacy)"), sent.Text)
	require.Contains(t, sent.HTML, `href="https://cybericebox.com/privacy"`)
}

func TestDeliver_CorruptSavedFooterFallsBackToTheDefault(t *testing.T) {
	uc, repo, smtp := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil)
	row := platformIdentityRow
	row.FooterContent = []byte(`{"not":"a document"}`)
	expectPlatformIdentity(repo, &row)

	require.NoError(t, uc.Deliver(context.Background(), nil, email.Message{To: "u@example.org", HTML: "<p>x</p>", Text: "x"}))
	requireFooter(t, smtp.sends[0].msg, "support@cybericebox.com")
}

func TestFooterHTML_NoShadowsAndEscapesValues(t *testing.T) {
	footer := renderFooter(nil, `a"<b>@x.y`, "https://x.y")
	require.NotContains(t, footer.HTML, "shadow")
	require.NotContains(t, footer.HTML, `a"<b>`)
	require.Contains(t, footer.HTML, ">—</p>", "the fold line opens the footer")
}

func TestRenderFooter_NothingToShowIsEmpty(t *testing.T) {
	only := []byte(`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"variable","varName":"reply_to"}]}]}}`)
	require.Equal(t, mailModel.Footer{}, renderFooter(only, "", "https://x.y"), "the only paragraph needs a Reply-To that is missing")
}

func TestUpdatePlatformFooter_ValidatesSavesAndReturnsSettings(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	by := uuid.Must(uuid.NewV7())

	_, err := uc.UpdatePlatformFooter(context.Background(), []byte(`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"variable","varName":"nope"}]}]}}`), by)
	require.ErrorIs(t, err, mailModel.ErrFooterInvalid.Err())

	repo.EXPECT().SetPlatformMailFooter(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.SetPlatformMailFooterParams) error {
			require.JSONEq(t, savedFooterDoc, string(arg.FooterContent))
			require.Equal(t, uuid.NullUUID{UUID: by, Valid: true}, arg.UpdatedBy)
			return nil
		})
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, &postgres.MailIdentity{FooterContent: []byte(savedFooterDoc)})

	view, err := uc.UpdatePlatformFooter(context.Background(), []byte(savedFooterDoc), by)
	require.NoError(t, err)
	require.JSONEq(t, savedFooterDoc, string(view.Footer.Content))
	require.JSONEq(t, string(mailModel.DefaultFooterDoc(mailModel.LanguageUK)), string(view.Footer.DefaultContent))
	require.Equal(t, mailModel.FooterVariables, view.Footer.Variables)
}

func TestUpdatePlatformFooter_DefaultContentIsStoredAsNull(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().SetPlatformMailFooter(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.SetPlatformMailFooterParams) error {
			require.Nil(t, arg.FooterContent, "the default keeps following the platform")
			return nil
		})
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, nil)

	view, err := uc.UpdatePlatformFooter(context.Background(), mailModel.DefaultFooterDoc(mailModel.LanguageUK), uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	raw, err := json.Marshal(view.Footer)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"Content":null`)
}

func TestGetPlatformMailSettings_LegacyTextFooterIsConverted(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{}, nil).AnyTimes()
	expectPlatformIdentity(repo, &postgres.MailIdentity{FooterText: "Дякуємо! {site_url}"})

	view, err := uc.GetPlatformMailSettings(context.Background())
	require.NoError(t, err)
	require.Contains(t, string(view.Footer.Content), `"varName":"site_url"`)
	require.Contains(t, string(view.Footer.Content), "Дякуємо! ")
}

func TestPreviewPlatformFooter_RendersUnsavedDocWithPlatformValues(t *testing.T) {
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	expectPlatformIdentity(repo, &platformIdentityRow)

	preview, err := uc.PreviewPlatformFooter(context.Background(), []byte(savedFooterDoc))
	require.NoError(t, err)
	require.Equal(t, "—\nДякуємо! support@cybericebox.com · cybericebox.com", preview.Text)
	require.Contains(t, preview.HTML, `href="mailto:support@cybericebox.com"`)

	_, err = uc.PreviewPlatformFooter(context.Background(), []byte(`{"root":{"type":"root","children":[{"type":"paragraph","children":[{"type":"variable","varName":"nope"}]}]}}`))
	require.ErrorIs(t, err, mailModel.ErrFooterInvalid.Err())

	preview, err = uc.PreviewPlatformFooter(context.Background(), nil)
	require.NoError(t, err)
	require.Contains(t, preview.Text, "Політика конфіденційності")
}
