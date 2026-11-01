package auth_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/oauth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

const (
	rawTicket = "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	account   = "owner@example.org"
)

func ticketHash(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

type linkFixture struct {
	uc       *auth.AuthUseCase
	repo     *postgresMocks.MockQuerier
	notifier *fakeNotifier
	pw       *password.Client
	uid      uuid.UUID
	ticketID uuid.UUID
}

func newLinkFixture(t *testing.T, gu *oauth.GoogleUser) *linkFixture {
	t.Helper()
	repo := postgresMocks.NewMockQuerier(gomock.NewController(t))
	allowSetupLinkIssue(repo)
	pw := password.New(password.Config{HashCost: 4})
	notifier := &fakeNotifier{}
	uc := auth.NewAuthUseCase(auth.Dependencies{Sessions: testSessions(t),
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: pw,
		Notifier: notifier,
		OAuth:    &fakeOAuth{user: gu},
		Config:   config.AuthConfig{SessionIdleTTL: time.Hour, Hosts: testHosts("test")},
	})
	return &linkFixture{uc: uc, repo: repo, notifier: notifier, pw: pw, uid: uuid.Must(uuid.NewV7()), ticketID: uuid.Must(uuid.NewV7())}
}

func (f *linkFixture) user(withPassword string) postgres.User {
	u := postgres.User{ID: f.uid, Email: account, FirstName: "Olena", Role: "user", Status: string(userModel.UserStatusActive), EmailConfirmed: true}
	if withPassword != "" {
		hashed, _ := f.pw.Hash(withPassword)
		u.HashedPassword = pgtype.Text{String: hashed, Valid: true}
	}
	return u
}

// expectTicket makes the ticket cookie value resolve to the pending link of the fixture's account.
func (f *linkFixture) expectTicket(codeHash string, attempts int32, acct postgres.User) {
	f.repo.EXPECT().GetGoogleLinkTicket(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.GetGoogleLinkTicketParams) (postgres.GoogleLinkTicket, error) {
			if arg.TicketHash != ticketHash(rawTicket) {
				return postgres.GoogleLinkTicket{}, pgx.ErrNoRows
			}
			return postgres.GoogleLinkTicket{ID: f.ticketID, TicketHash: arg.TicketHash, UserID: f.uid, GoogleID: "g-77", GoogleEmail: "owner@example.org", GoogleName: "Olena Owner", ReturnTo: "https://id.test/profile/",
				CodeHash: codeHash, CodeAttempts: attempts, CreatedAt: time.Now(), ExpiresAt: time.Now().Add(time.Minute)}, nil
		}).AnyTimes()
	f.repo.EXPECT().GetUserByID(gomock.Any(), f.uid).Return(acct, nil).AnyTimes()
	f.repo.EXPECT().GetUserProviders(gomock.Any(), f.uid).Return(nil, nil).AnyTimes()
}

func (f *linkFixture) expectLinkAndSession() {
	f.repo.EXPECT().ConsumeGoogleLinkTicket(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	f.repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	f.repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.CreateUserProviderParams) (postgres.UserProvider, error) {
			if arg.UserID != f.uid || arg.Provider != "google" || arg.ProviderUserID != "g-77" {
				return postgres.UserProvider{}, errors.New("the link must bind the ticket's Google identity to the ticket's account")
			}
			return postgres.UserProvider{}, nil
		})
	f.repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
		Return(postgres.Session{ID: uuid.Must(uuid.NewV7()), UserID: f.uid, ExpiresAt: time.Now().Add(time.Hour)}, nil)
}

// codeOf sends a code and returns it from the mail.
func (f *linkFixture) sendCode(t *testing.T) (string, string) {
	t.Helper()
	var codeHash string
	f.repo.EXPECT().SetGoogleLinkTicketCode(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, arg postgres.SetGoogleLinkTicketCodeParams) (int64, error) {
			codeHash = arg.CodeHash
			return 1, nil
		})
	if _, err := f.uc.SendGoogleLinkCode(context.Background(), rawTicket); err != nil {
		t.Fatalf("SendGoogleLinkCode: %v", err)
	}
	payload, ok := f.notifier.lastPayload.(notificationPayloads.GoogleLinkCodePayload)
	if !ok || !regexp.MustCompile(`^\d{6}$`).MatchString(payload.Code) {
		t.Fatalf("the mail carries a six-digit code, got %#v", f.notifier.lastPayload)
	}
	return payload.Code, codeHash
}

// A Gmail address (any case, and googlemail.com) links at once; every other domain waits for the owner.
func TestGoogleAuth_OnlyGmailLinksAtOnce(t *testing.T) {
	for _, address := range []string{"owner@gmail.com", "Owner@GMAIL.COM", "owner@googlemail.com"} {
		t.Run(address, func(t *testing.T) {
			f := newLinkFixture(t, &oauth.GoogleUser{GoogleID: "g-77", Email: address, EmailVerified: true})
			f.repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows).Times(2)
			acct := f.user("")
			acct.Email = strings.ToLower(address)
			f.repo.EXPECT().GetUserByEmail(gomock.Any(), strings.ToLower(address)).Return(acct, nil)
			f.repo.EXPECT().GetUserProviders(gomock.Any(), f.uid).Return(nil, nil)
			f.repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).Return(postgres.UserProvider{}, nil)
			f.repo.EXPECT().CreateSession(gomock.Any(), gomock.Any()).
				Return(postgres.Session{ID: uuid.Must(uuid.NewV7()), UserID: f.uid, ExpiresAt: time.Now().Add(time.Hour)}, nil)
			cookie, _, err := f.uc.GoogleAuth(context.Background(), "code", "state", authModel.SessionMetadata{})
			if err != nil || cookie == "" {
				t.Fatalf("GoogleAuth: %q %v", cookie, err)
			}
			if f.notifier.calls != 0 {
				t.Fatal("no mail announces the link")
			}
		})
	}
}

// Another domain (even one that merely contains gmail.com): no link, no session, a ticket for the browser.
func TestGoogleAuth_OtherDomainNeedsConfirmation(t *testing.T) {
	for _, address := range []string{"owner@example.org", "owner@gmail.com.evil.test", "owner@notgmail.com", "gmail.com@example.org"} {
		t.Run(address, func(t *testing.T) {
			f := newLinkFixture(t, &oauth.GoogleUser{GoogleID: "g-77", Email: address, Name: "Olena Owner", EmailVerified: true})
			f.repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
			acct := f.user("Secret!1")
			acct.Email = address
			f.repo.EXPECT().GetUserByEmail(gomock.Any(), strings.ToLower(address)).Return(acct, nil)
			f.repo.EXPECT().GetUserProviders(gomock.Any(), f.uid).Return(nil, nil)
			var stored postgres.ReplaceGoogleLinkTicketParams
			f.repo.EXPECT().DeleteExpiredGoogleLinkTickets(gomock.Any(), gomock.Any()).Return(nil)
			f.repo.EXPECT().ReplaceGoogleLinkTicket(gomock.Any(), gomock.Any()).
				DoAndReturn(func(_ context.Context, arg postgres.ReplaceGoogleLinkTicketParams) error {
					stored = arg
					return nil
				})
			// no CreateUserProvider, no CreateSession expected

			cookie, _, err := f.uc.GoogleAuth(context.Background(), "code", "state", authModel.SessionMetadata{})
			var confirm *auth.GoogleLinkConfirmError
			if !errors.As(err, &confirm) || !errors.Is(err, authModel.ErrAuthGoogleLinkConfirm.Err()) || cookie != "" {
				t.Fatalf("want GOOGLE_LINK_CONFIRM, got cookie %q err %v", cookie, err)
			}
			if stored.ID == uuid.Nil || stored.UserID != f.uid || stored.GoogleID != "g-77" || stored.TicketHash != ticketHash(confirm.Ticket) || stored.TicketHash == confirm.Ticket {
				t.Fatalf("the ticket is bound to the account and the Google identity and stored hashed: %+v", stored)
			}
			if stored.GoogleEmail != strings.ToLower(address) || stored.GoogleName != "Olena Owner" {
				t.Fatalf("the screen shows the Google account to be linked: %+v", stored)
			}
			if got := stored.ExpiresAt.Sub(stored.CreatedAt); got != 10*time.Minute {
				t.Fatalf("the ticket lives 10 minutes, got %s", got)
			}
		})
	}
}

func TestGoogleLinkConfirm_PasswordLinksAndSignsIn(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user("Secret!1"))
	f.expectLinkAndSession()
	cookie, redirect, err := f.uc.ConfirmGoogleLink(context.Background(), rawTicket, "Secret!1", "", authModel.SessionMetadata{})
	if err != nil || cookie == "" || redirect != "https://id.test/profile/" {
		t.Fatalf("ConfirmGoogleLink: %q %q %v", cookie, redirect, err)
	}
	if f.notifier.calls != 0 {
		t.Fatal("no mail announces the link")
	}
}

func TestGoogleLinkConfirm_WrongPasswordLinksNothing(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user("Secret!1"))
	// no ConsumeGoogleLinkTicket, CreateUserProvider or CreateSession expected
	_, _, err := f.uc.ConfirmGoogleLink(context.Background(), rawTicket, "wrong", "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthInvalidUserCredentials.Err()) {
		t.Fatalf("want invalid credentials, got %v", err)
	}
}

// An account without a password (passkey-only or Google-only) confirms with the mailed code only.
func TestGoogleLinkConfirm_PasswordlessAccountNeedsTheMailedCode(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user(""))
	_, _, err := f.uc.ConfirmGoogleLink(context.Background(), rawTicket, "anything", "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthPasswordRequired.Err()) {
		t.Fatalf("want ErrAuthPasswordRequired, got %v", err)
	}
	view, err := f.uc.GoogleLinkPendingOf(context.Background(), rawTicket)
	if err != nil || view.HasPassword || view.Email != "o***@example.org" || view.GoogleEmail != "owner@example.org" || view.GoogleName != "Olena Owner" {
		t.Fatalf("pending view %+v %v", view, err)
	}
}

func TestGoogleLinkConfirm_MailedCodeLinksAndSignsIn(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user(""))
	code, codeHash := f.sendCode(t)
	if f.notifier.calls != 1 {
		t.Fatalf("one mail, the code: %d", f.notifier.calls)
	}

	f2 := newLinkFixture(t, &oauth.GoogleUser{})
	f2.uid, f2.ticketID = f.uid, f.ticketID
	f2.expectTicket(codeHash, 0, f2.user(""))
	f2.repo.EXPECT().CountGoogleLinkTicketCodeAttempt(gomock.Any(), gomock.Any()).Return(int32(1), nil)
	f2.expectLinkAndSession()
	cookie, _, err := f2.uc.ConfirmGoogleLink(context.Background(), rawTicket, "", " "+code+" ", authModel.SessionMetadata{})
	if err != nil || cookie == "" {
		t.Fatalf("ConfirmGoogleLink: %q %v", cookie, err)
	}
}

func TestGoogleLinkConfirm_WrongCodeAndExhaustedAttempts(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user(""))
	_, codeHash := f.sendCode(t)

	g := newLinkFixture(t, &oauth.GoogleUser{})
	g.uid, g.ticketID = f.uid, f.ticketID
	g.expectTicket(codeHash, 0, g.user(""))
	g.repo.EXPECT().CountGoogleLinkTicketCodeAttempt(gomock.Any(), gomock.Any()).Return(int32(1), nil)
	_, _, err := g.uc.ConfirmGoogleLink(context.Background(), rawTicket, "", "000000", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthGoogleLinkCodeInvalid.Err()) {
		t.Fatalf("a wrong code: %v", err)
	}

	// The fifth wrong code was counted; the next attempt finds no attempts left and the ticket is dropped.
	h := newLinkFixture(t, &oauth.GoogleUser{})
	h.uid, h.ticketID = f.uid, f.ticketID
	h.expectTicket(codeHash, 5, h.user(""))
	h.repo.EXPECT().CountGoogleLinkTicketCodeAttempt(gomock.Any(), gomock.Any()).Return(int32(0), pgx.ErrNoRows)
	h.repo.EXPECT().DeleteGoogleLinkTicket(gomock.Any(), f.ticketID).Return(int64(1), nil)
	_, _, err = h.uc.ConfirmGoogleLink(context.Background(), rawTicket, "", "123456", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthGoogleLinkTicketInvalid.Err()) {
		t.Fatalf("exhausted attempts: %v", err)
	}
}

// A code that was never sent confirms nothing.
func TestGoogleLinkConfirm_CodeBeforeAnyWasSent(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user(""))
	_, _, err := f.uc.ConfirmGoogleLink(context.Background(), rawTicket, "", "123456", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthGoogleLinkCodeInvalid.Err()) {
		t.Fatalf("got %v", err)
	}
}

// Expired, unknown, malformed or absent ticket: one answer.
func TestGoogleLinkTicket_ExpiredOrUnknownIsNotFound(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.repo.EXPECT().GetGoogleLinkTicket(gomock.Any(), gomock.Any()).Return(postgres.GoogleLinkTicket{}, pgx.ErrNoRows)
	for _, raw := range []string{rawTicket, "", "short", strings.Repeat("Z", 64)} {
		_, _, err := f.uc.ConfirmGoogleLink(context.Background(), raw, "Secret!1", "", authModel.SessionMetadata{})
		if !errors.Is(err, authModel.ErrAuthGoogleLinkTicketInvalid.Err()) {
			t.Fatalf("ticket %q: %v", raw, err)
		}
	}
	if _, err := f.uc.GoogleLinkPendingOf(context.Background(), "short"); !errors.Is(err, authModel.ErrAuthGoogleLinkTicketInvalid.Err()) {
		t.Fatalf("pending: %v", err)
	}
	if _, err := f.uc.SendGoogleLinkCode(context.Background(), ""); !errors.Is(err, authModel.ErrAuthGoogleLinkTicketInvalid.Err()) {
		t.Fatalf("send: %v", err)
	}
}

// A second confirmation with the same ticket (a replay, or a concurrent one) finds the ticket claimed: nothing is
// linked, nobody is signed in.
func TestGoogleLinkConfirm_ReplayAfterTheTicketWasClaimed(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user("Secret!1"))
	f.repo.EXPECT().ConsumeGoogleLinkTicket(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	_, _, err := f.uc.ConfirmGoogleLink(context.Background(), rawTicket, "Secret!1", "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthGoogleLinkTicketInvalid.Err()) {
		t.Fatalf("got %v", err)
	}
}

// The unique index refuses a concurrent link of a second Google identity: the ticket reads as gone.
func TestGoogleLinkConfirm_LosingTheLinkRace(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user("Secret!1"))
	f.repo.EXPECT().ConsumeGoogleLinkTicket(gomock.Any(), gomock.Any()).Return(int64(1), nil)
	f.repo.EXPECT().GetUserByProvider(gomock.Any(), gomock.Any()).Return(postgres.User{}, pgx.ErrNoRows)
	f.repo.EXPECT().CreateUserProvider(gomock.Any(), gomock.Any()).
		Return(postgres.UserProvider{}, &pgconn.PgError{Code: "23505", Detail: "Key (user_id, provider)=(x, google) already exists."})
	cookie, _, err := f.uc.ConfirmGoogleLink(context.Background(), rawTicket, "Secret!1", "", authModel.SessionMetadata{})
	if !errors.Is(err, authModel.ErrAuthGoogleLinkTicketInvalid.Err()) || cookie != "" {
		t.Fatalf("got %q %v", cookie, err)
	}
}

// An account blocked, or given its own Google, while the ticket waited cannot be linked: the ticket is dropped.
func TestGoogleLinkConfirm_AccountChangedWhileWaiting(t *testing.T) {
	blocked := func(f *linkFixture) postgres.User {
		u := f.user("Secret!1")
		u.Status = string(userModel.UserStatusBlocked)
		return u
	}
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.repo.EXPECT().GetGoogleLinkTicket(gomock.Any(), gomock.Any()).Return(postgres.GoogleLinkTicket{ID: f.ticketID, UserID: f.uid, GoogleID: "g-77", ExpiresAt: time.Now().Add(time.Minute)}, nil)
	f.repo.EXPECT().GetUserByID(gomock.Any(), f.uid).Return(blocked(f), nil)
	f.repo.EXPECT().DeleteGoogleLinkTicket(gomock.Any(), f.ticketID).Return(int64(1), nil)
	if _, _, err := f.uc.ConfirmGoogleLink(context.Background(), rawTicket, "Secret!1", "", authModel.SessionMetadata{}); !errors.Is(err, authModel.ErrAuthGoogleLinkTicketInvalid.Err()) {
		t.Fatalf("blocked: %v", err)
	}

	g := newLinkFixture(t, &oauth.GoogleUser{})
	g.repo.EXPECT().GetGoogleLinkTicket(gomock.Any(), gomock.Any()).Return(postgres.GoogleLinkTicket{ID: g.ticketID, UserID: g.uid, GoogleID: "g-77", ExpiresAt: time.Now().Add(time.Minute)}, nil)
	g.repo.EXPECT().GetUserByID(gomock.Any(), g.uid).Return(g.user("Secret!1"), nil)
	g.repo.EXPECT().GetUserProviders(gomock.Any(), g.uid).Return([]postgres.UserProvider{{UserID: g.uid, Provider: "google", ProviderUserID: "g-old"}}, nil)
	g.repo.EXPECT().DeleteGoogleLinkTicket(gomock.Any(), g.ticketID).Return(int64(1), nil)
	if _, _, err := g.uc.ConfirmGoogleLink(context.Background(), rawTicket, "Secret!1", "", authModel.SessionMetadata{}); !errors.Is(err, authModel.ErrAuthGoogleLinkTicketInvalid.Err()) {
		t.Fatalf("already has Google: %v", err)
	}
}

// The mail quota: a second code inside the gap is refused with a wait, and the first stays valid.
func TestSendGoogleLinkCode_ResendIsLimited(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user(""))
	f.sendCode(t)
	_, err := f.uc.SendGoogleLinkCode(context.Background(), rawTicket)
	if !errors.Is(err, authModel.ErrAuthTooManyRequests.Err()) {
		t.Fatalf("a second code inside the gap: %v", err)
	}
	if f.notifier.calls != 1 {
		t.Fatalf("only one mail went out: %d", f.notifier.calls)
	}
}

func TestGoogleLinkConfirm_NeedsExactlyOneProof(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	for _, c := range [][2]string{{"", ""}, {"pw", "123456"}} {
		if _, _, err := f.uc.ConfirmGoogleLink(context.Background(), rawTicket, c[0], c[1], authModel.SessionMetadata{}); err == nil {
			t.Fatalf("password %q and code %q must be refused", c[0], c[1])
		}
	}
}

func TestCancelGoogleLink(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user("Secret!1"))
	f.repo.EXPECT().DeleteGoogleLinkTicket(gomock.Any(), f.ticketID).Return(int64(1), nil)
	if err := f.uc.CancelGoogleLink(context.Background(), rawTicket); err != nil {
		t.Fatal(err)
	}
	if err := f.uc.CancelGoogleLink(context.Background(), "garbage"); err != nil {
		t.Fatalf("an unknown ticket is silently done: %v", err)
	}
}

// The address behind a fresh session cookie is what the browser remembers as its last sign-in address.
func TestSessionEmail(t *testing.T) {
	f := newLinkFixture(t, &oauth.GoogleUser{})
	f.expectTicket("", 0, f.user("Secret!1"))
	f.expectLinkAndSession()
	cookie, _, err := f.uc.ConfirmGoogleLink(context.Background(), rawTicket, "Secret!1", "", authModel.SessionMetadata{})
	if err != nil {
		t.Fatal(err)
	}
	got, err := f.uc.SessionEmail(context.Background(), cookie)
	if err != nil || got != account {
		t.Fatalf("SessionEmail = %q %v", got, err)
	}
	if _, err = f.uc.SessionEmail(context.Background(), "garbage"); err == nil {
		t.Fatal("a cookie that does not open gives no address")
	}
}
