package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"math/big"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/delivery/repository/googleLinkTicketRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/limits"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/oauth"
	"github.com/cybericebox/daemon/pkg/tools"
)

// A Google identity that signed in with the verified address of an existing account is linked and signed in at once
// only for a Gmail address (Google is the authority on those). For any other address the owner confirms first, with
// the account password or a code mailed to the address; until then nothing is linked and nobody is signed in. The
// wait is a short-lived, single-use ticket kept on the server, bound to the Google identity and the account; the
// browser holds only its opaque value in a cookie.

const (
	// googleLinkTicketTTL is the life of a pending link and of a mailed code.
	googleLinkTicketTTL = 10 * time.Minute
	// googleLinkCodeMaxAttempts is how many wrong codes drop the pending link.
	googleLinkCodeMaxAttempts = 5
	// mailKindGoogleLink is the mail kind whose quota (the gap between mails and the hourly count) the codes use.
	mailKindGoogleLink = "google-link"
)

// GoogleLinkConfirmError is ErrAuthGoogleLinkConfirm with the ticket the callback hands to the browser.
type GoogleLinkConfirmError struct {
	// Ticket is the opaque value of the pending link (the cookie), ReturnTo the trusted page to land on afterwards.
	Ticket, ReturnTo string
}

func (e *GoogleLinkConfirmError) Error() string {
	return authModel.ErrAuthGoogleLinkConfirm.Err().Error()
}

func (e *GoogleLinkConfirmError) Unwrap() error { return authModel.ErrAuthGoogleLinkConfirm.Err() }

// GoogleLinkPending is what the confirmation page shows of a pending link.
type GoogleLinkPending struct {
	// Email is the address of the account, masked.
	Email string
	// HasPassword: the owner can confirm with the password; false: only the mailed code works.
	HasPassword bool
	// GoogleEmail and GoogleName are the Google account that will be linked: the page shows them, so an owner who
	// did not start this sign-in sees that the account is not theirs.
	GoogleEmail, GoogleName string
	// CodeSentAt is when the last code was mailed, nil if none was.
	CodeSentAt *time.Time
	ExpiresAt  time.Time
}

// GoogleLinkCodeSent is the answer to a code request.
type GoogleLinkCodeSent struct {
	// ResendAfter is the least wait before the next code can be asked for.
	ResendAfter time.Duration
}

// isGmailAddress tells whether the address is exactly a Gmail one (gmail.com or googlemail.com), compared
// case-insensitively on the domain after the last «@».
func isGmailAddress(address string) bool {
	at := strings.LastIndexByte(address, '@')
	if at < 1 {
		return false
	}
	switch strings.ToLower(address[at+1:]) {
	case "gmail.com", "googlemail.com":
		return true
	}
	return false
}

// maskEmail keeps the first character of the name and the whole domain: a***@example.org.
func maskEmail(address string) string {
	at := strings.LastIndexByte(address, '@')
	if at < 1 {
		return "***"
	}
	return address[:1] + "***" + address[at:]
}

func googleLinkCodeHash(ticketID uuid.UUID, code string) string {
	sum := sha256.Sum256([]byte("google-link-code:" + ticketID.String() + ":" + code))
	return hex.EncodeToString(sum[:])
}

// issueGoogleLinkTicket stores a pending link of googleUser to account and returns the opaque ticket value.
func (u *AuthUseCase) issueGoogleLinkTicket(ctx context.Context, account userModel.User, google *oauth.GoogleUser, returnTo string) (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to generate the Google link ticket").Err()
	}
	raw := hex.EncodeToString(buf)
	now := time.Now()
	if err := u.googleTickets.Create(ctx, googleLinkTicketRepo.Ticket{
		ID: tools.NewUUIDv7(), UserID: account.ID, GoogleID: google.GoogleID, GoogleEmail: normalizeEmail(google.Email), GoogleName: google.Name, ReturnTo: returnTo,
		CreatedAt: now, ExpiresAt: now.Add(googleLinkTicketTTL),
	}, hashTemporalCode(raw)); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to store the Google link ticket").Err()
	}
	log.Info().Str("event", "google_link_pending").Str("user_id", account.ID.String()).Msg("A Google link waits for the owner's confirmation")
	return raw, nil
}

// loadGoogleLinkTicket returns the live ticket and its account. A missing, malformed, expired or used ticket, and a
// ticket whose account is gone, blocked or has a Google identity by now are one answer (and the last two drop it).
func (u *AuthUseCase) loadGoogleLinkTicket(ctx context.Context, raw string) (googleLinkTicketRepo.Ticket, userModel.User, error) {
	invalid := authModel.ErrAuthGoogleLinkTicketInvalid.Err()
	if !validTemporalCode(raw) {
		return googleLinkTicketRepo.Ticket{}, userModel.User{}, invalid
	}
	ticket, err := u.googleTickets.Get(ctx, hashTemporalCode(raw), time.Now())
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return googleLinkTicketRepo.Ticket{}, userModel.User{}, invalid
		}
		return googleLinkTicketRepo.Ticket{}, userModel.User{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the Google link ticket").Err()
	}
	account, err := u.users.GetByID(ctx, ticket.UserID)
	if err != nil && !repositoryTools.IsObjectNotFoundError(err) {
		return googleLinkTicketRepo.Ticket{}, userModel.User{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read the account of the Google link").Err()
	}
	if err == nil && account.Status == userModel.UserStatusActive {
		providers, listErr := u.users.ListProviders(ctx, account.ID)
		if listErr != nil {
			return googleLinkTicketRepo.Ticket{}, userModel.User{}, model.ErrPlatform.WithError(listErr).WithMessage("Failed to list user providers").Err()
		}
		if !slices.Contains(providers, userModel.GoogleProvider) {
			return ticket, account, nil
		}
	}
	_ = u.googleTickets.Delete(ctx, ticket.ID)
	return googleLinkTicketRepo.Ticket{}, userModel.User{}, invalid
}

// GoogleLinkPendingOf describes a pending link for the confirmation page.
func (u *AuthUseCase) GoogleLinkPendingOf(ctx context.Context, rawTicket string) (GoogleLinkPending, error) {
	ticket, account, err := u.loadGoogleLinkTicket(ctx, rawTicket)
	if err != nil {
		return GoogleLinkPending{}, err
	}
	return GoogleLinkPending{Email: maskEmail(account.Email), HasPassword: account.HasPassword(), GoogleEmail: ticket.GoogleEmail, GoogleName: ticket.GoogleName, CodeSentAt: ticket.CodeSentAt, ExpiresAt: ticket.ExpiresAt}, nil
}

// SendGoogleLinkCode mails a one-time code to the address of the account, within the mail quota of the address
// (a gap between mails and a count per hour), and keeps only its hash. A new code replaces the old one.
func (u *AuthUseCase) SendGoogleLinkCode(ctx context.Context, rawTicket string) (GoogleLinkCodeSent, error) {
	ticket, account, err := u.loadGoogleLinkTicket(ctx, rawTicket)
	if err != nil {
		return GoogleLinkCodeSent{}, err
	}
	allowed, release := u.limits.mail.Reserve(mailKindGoogleLink, normalizeEmail(account.Email))
	if !allowed {
		return GoogleLinkCodeSent{}, tooManyRequests(limits.Get().AccountMailGap)
	}
	sent := false
	defer func() {
		if !sent {
			release()
		}
	}()
	n, err := rand.Int(rand.Reader, big.NewInt(1_000_000))
	if err != nil {
		return GoogleLinkCodeSent{}, model.ErrPlatform.WithError(err).WithMessage("Failed to generate the confirmation code").Err()
	}
	code := fmt.Sprintf("%06d", n.Int64())
	ok, err := u.googleTickets.SetCode(ctx, ticket.ID, googleLinkCodeHash(ticket.ID, code), time.Now())
	if err != nil {
		return GoogleLinkCodeSent{}, model.ErrPlatform.WithError(err).WithMessage("Failed to keep the confirmation code").Err()
	}
	if !ok {
		return GoogleLinkCodeSent{}, authModel.ErrAuthGoogleLinkTicketInvalid.Err()
	}
	if err = u.notifier.Notify(ctx, account.ID, notificationPayloads.GoogleLinkCodePayload{
		Name: account.FirstName, Code: code, Minutes: fmt.Sprint(int(googleLinkTicketTTL / time.Minute)),
	}); err != nil {
		return GoogleLinkCodeSent{}, model.ErrPlatform.WithError(err).WithMessage("Failed to send the confirmation code").Err()
	}
	sent = true
	return GoogleLinkCodeSent{ResendAfter: limits.Get().AccountMailGap}, nil
}

// CancelGoogleLink drops a pending link; it reports nothing about a ticket that is not there.
func (u *AuthUseCase) CancelGoogleLink(ctx context.Context, rawTicket string) error {
	ticket, _, err := u.loadGoogleLinkTicket(ctx, rawTicket)
	if err != nil {
		if errors.Is(err, authModel.ErrAuthGoogleLinkTicketInvalid.Err()) {
			return nil
		}
		return err
	}
	if err = u.googleTickets.Delete(ctx, ticket.ID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to drop the Google link ticket").Err()
	}
	return nil
}

// ConfirmGoogleLink checks the owner (exactly one of password and code), then links the Google identity of the
// ticket to the account, signs the account in and returns the session cookie and the page to land on. The ticket is
// claimed first, so a replay finds nothing; the unique index on the account's providers guards a concurrent link.
func (u *AuthUseCase) ConfirmGoogleLink(
	ctx context.Context,
	rawTicket, plainPassword, code string,
	meta authModel.SessionMetadata,
) (sessionCookie, redirect string, err error) {
	if (plainPassword == "") == (code == "") {
		return "", "", authModel.ErrAuthGoogleLinkCodeInvalid.WithError(errors.New("google-link: exactly one of password and code is required")).Err()
	}
	ticket, account, err := u.loadGoogleLinkTicket(ctx, rawTicket)
	if err != nil {
		return "", "", err
	}
	method := "code"
	if plainPassword != "" {
		method = "password"
		if err = u.checkGoogleLinkPassword(account, plainPassword); err != nil {
			return "", "", err
		}
	} else if err = u.checkGoogleLinkCode(ctx, ticket, code); err != nil {
		return "", "", err
	}
	claimed, err := u.googleTickets.Consume(ctx, ticket.ID, time.Now())
	if err != nil {
		return "", "", model.ErrPlatform.WithError(err).WithMessage("Failed to claim the Google link ticket").Err()
	}
	if !claimed {
		return "", "", authModel.ErrAuthGoogleLinkTicketInvalid.Err()
	}
	if err = u.linkGoogleProvider(ctx, account.ID, ticket.GoogleID); err != nil {
		if errors.Is(err, userModel.ErrUserExists.Err()) {
			return "", "", authModel.ErrAuthGoogleLinkTicketInvalid.WithError(err).Err()
		}
		return "", "", err
	}
	sessionCookie, err = u.createSession(ctx, account.ID, meta)
	if err != nil {
		return "", "", err
	}
	log.Info().Str("event", "google_link_confirmed").Str("user_id", account.ID.String()).Str("method", method).Msg("A Google account was linked after the owner's confirmation")
	return sessionCookie, u.resolveRedirect(ticket.ReturnTo), nil
}

// checkGoogleLinkPassword is the password sign-in check (the same per-account lockout) for the owner of the ticket.
func (u *AuthUseCase) checkGoogleLinkPassword(account userModel.User, plain string) error {
	key := normalizeEmail(account.Email)
	if wait := u.limits.signInAccount.Locked(key); wait > 0 {
		return tooManyRequests(wait)
	}
	if !account.HasPassword() {
		// Passkey-only and Google-only accounts confirm with the mailed code.
		return authModel.ErrAuthPasswordRequired.Err()
	}
	matches, err := u.password.Matches(plain, account.HashedPassword)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check password").Err()
	}
	if !matches {
		u.limits.signInAccount.Fail(key)
		return authModel.ErrAuthInvalidUserCredentials.Err()
	}
	u.limits.signInAccount.Reset(key)
	return nil
}

// checkGoogleLinkCode counts the attempt, then compares the code with the mailed one in constant time. Too many wrong
// codes drop the ticket.
func (u *AuthUseCase) checkGoogleLinkCode(ctx context.Context, ticket googleLinkTicketRepo.Ticket, code string) error {
	if ticket.CodeHash == "" {
		return authModel.ErrAuthGoogleLinkCodeInvalid.WithError(errors.New("google-link: no code was sent")).Err()
	}
	counted, err := u.googleTickets.CountCodeAttempt(ctx, ticket.ID, time.Now(), googleLinkCodeMaxAttempts)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to count the code attempt").Err()
	}
	if !counted {
		_ = u.googleTickets.Delete(ctx, ticket.ID)
		return authModel.ErrAuthGoogleLinkTicketInvalid.Err()
	}
	if subtle.ConstantTimeCompare([]byte(googleLinkCodeHash(ticket.ID, strings.TrimSpace(code))), []byte(ticket.CodeHash)) != 1 {
		return authModel.ErrAuthGoogleLinkCodeInvalid.Err()
	}
	return nil
}
