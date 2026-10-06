package mailUseCase

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/cybericebox/daemon/pkg/secret"
)

func sealedEventRow(t *testing.T, cipher *secret.Cipher, password string) postgres.MailSmtpConfig {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	ct, err := cipher.EncryptWithContext([]byte(password), passwordContext(id))
	require.NoError(t, err)
	return postgres.MailSmtpConfig{ID: id, Host: "smtp.uni.example", Port: 587, TlsMode: "starttls", Username: "mailer", PasswordCiphertext: ct}
}

func testCipher(t *testing.T) *secret.Cipher {
	t.Helper()
	cipher, err := secret.New("00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff")
	require.NoError(t, err)
	return cipher
}

func storedConfig(row postgres.MailSmtpConfig) mailModel.SMTPConfig {
	return mailModel.SMTPConfig{ID: row.ID, Host: row.Host, Port: int(row.Port), TLSMode: mailModel.TLSMode(row.TlsMode), Username: row.Username, PasswordCiphertext: row.PasswordCiphertext}
}

// The reported PoC: the form names the attacker's host and leaves the password empty, hoping the stored
// password is sent there.
func TestAStoredPasswordIsNeverUsedAtAnotherHost(t *testing.T) {
	cipher := testCipher(t)
	uc, _, _ := newTestUseCase(t, config.SMTPConfig{}, cipher)
	stored := storedConfig(sealedEventRow(t, cipher, "mailbox-secret"))

	for name, in := range map[string]mailModel.SMTPInput{
		"another host":     {Host: "attacker.example", Port: 587, TLSMode: mailModel.TLSStartTLS, Username: "mailer"},
		"another port":     {Host: "smtp.uni.example", Port: 2525, TLSMode: mailModel.TLSStartTLS, Username: "mailer"},
		"another username": {Host: "smtp.uni.example", Port: 587, TLSMode: mailModel.TLSStartTLS, Username: "other"},
		"another TLS mode": {Host: "smtp.uni.example", Port: 587, TLSMode: mailModel.TLSImplicit, Username: "mailer"},
	} {
		_, err := uc.formTransport(stored, true, in, true)
		require.ErrorIs(t, err, mailModel.ErrSMTPPasswordRequired.Err(), name)
		_, err = uc.applyInput(stored, true, nil, in, uuid.Must(uuid.NewV7()))
		require.ErrorIs(t, err, mailModel.ErrSMTPPasswordRequired.Err(), name)
	}

	// With a new password the form is a new connection and the stored secret stays out of it.
	moved := mailModel.SMTPInput{Host: "attacker.example", Port: 587, TLSMode: mailModel.TLSStartTLS, Username: "mailer", Password: "typed-now"}
	tr, err := uc.formTransport(stored, true, moved, true)
	require.NoError(t, err)
	require.Equal(t, "typed-now", tr.conn.Password)
	// Clearing the password is allowed and sends none.
	moved.Password, moved.ClearPassword = "", true
	tr, err = uc.formTransport(stored, true, moved, true)
	require.NoError(t, err)
	require.Empty(t, tr.conn.Password)

	// The same connection still uses the stored password (the form leaves it blank on purpose).
	same := mailModel.SMTPInput{Host: "SMTP.uni.example", Port: 587, TLSMode: mailModel.TLSStartTLS, Username: "mailer"}
	tr, err = uc.formTransport(stored, true, same, true)
	require.NoError(t, err)
	require.Equal(t, "mailbox-secret", tr.conn.Password)
	cfg, err := uc.applyInput(stored, true, nil, same, uuid.Must(uuid.NewV7()))
	require.NoError(t, err)
	require.Equal(t, stored.PasswordCiphertext, cfg.PasswordCiphertext)
}

func TestSavingAnEventSMTPAtAnotherHostNeedsThePasswordAgain(t *testing.T) {
	cipher := testCipher(t)
	uc, repo, _ := newTestUseCase(t, config.SMTPConfig{}, cipher)
	eventID := uuid.Must(uuid.NewV7())
	row := sealedEventRow(t, cipher, "mailbox-secret")
	repo.EXPECT().GetEventByID(gomock.Any(), eventID).Return(postgres.Event{ID: eventID, Tag: "ctf", Name: "CTF"}, nil).AnyTimes()
	repo.EXPECT().GetEventMailIdentity(gomock.Any(), gomock.Any()).Return(postgres.MailIdentity{}, pgx.ErrNoRows).AnyTimes()
	expectPlatformIdentity(repo, &platformIdentityRow)
	repo.EXPECT().ListPlatformSMTPProviders(gomock.Any()).Return([]postgres.MailSmtpConfig{platformRow}, nil).AnyTimes()
	repo.EXPECT().GetEventSMTPConfig(gomock.Any(), gomock.Any()).Return(row, nil).AnyTimes()

	_, err := uc.UpdateEventSMTP(context.Background(), eventID, mailModel.SMTPInput{Host: "attacker.example", Port: 465, Username: "mailer"}, uuid.Must(uuid.NewV7()))
	require.ErrorIs(t, err, mailModel.ErrSMTPPasswordRequired.Err())
}

func TestAnEventSMTPCannotPointInsideTheNetwork(t *testing.T) {
	uc, _, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	for _, host := range []string{"127.0.0.1", "10.0.0.5", "169.254.169.254", "100.64.0.1", "::1", "[fd00::1]", "localhost", "postgres", "minio", "api.svc.cluster.local", "db.internal", "mail.local"} {
		_, err := uc.normalizeEventSMTP(mailModel.SMTPInput{Host: host, Port: 587})
		require.ErrorIs(t, err, mailModel.ErrSMTPSettingsInvalid.Err(), host)
	}
	for _, port := range []int{22, 80, 3306, 5432, 6379, 9000} {
		_, err := uc.normalizeEventSMTP(mailModel.SMTPInput{Host: "smtp.uni.example", Port: port})
		require.ErrorIs(t, err, mailModel.ErrSMTPSettingsInvalid.Err(), port)
	}
	for _, port := range []int{25, 465, 587, 2525} {
		_, err := uc.normalizeEventSMTP(mailModel.SMTPInput{Host: "email-smtp.eu-north-1.amazonaws.com", Port: port})
		require.NoError(t, err, port)
	}
	// The allow-list is configurable.
	uc.smtpPolicy = mailModel.SMTPPolicy{AllowedPorts: []int{587}}
	_, err := uc.normalizeEventSMTP(mailModel.SMTPInput{Host: "smtp.uni.example", Port: 2525})
	require.ErrorIs(t, err, mailModel.ErrSMTPSettingsInvalid.Err())
}

func TestAnEventConnectionIsGuardedAndAPlatformOneIsNot(t *testing.T) {
	uc, _, _ := newTestUseCase(t, config.SMTPConfig{}, nil)
	scope := uuid.Must(uuid.NewV7())
	event, err := uc.connection(mailModel.SMTPConfig{Host: "smtp.uni.example", Port: 587, ScopeEventID: &scope}, "")
	require.NoError(t, err)
	require.True(t, event.GuardDial, "an organizer-controlled SMTP never connects to an internal address")
	platform, err := uc.connection(mailModel.SMTPConfig{Host: "smtp.relay.example", Port: 587}, "")
	require.NoError(t, err)
	require.False(t, platform.GuardDial)
	require.False(t, platform.Insecure, "only the env transport may be insecure")
}
