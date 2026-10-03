package auth

import (
	"context"
	"errors"
	"time"

	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/session"
)

// SessionPass is a cookie that passed the checks that need no database: it decrypts, it is not expired and its
// session is not in the revocation set.
type SessionPass struct {
	Ticket session.Ticket
}

// OpenSession is steps 1 and 2 of a request: decrypt the cookie and check the expiry (garbage or expired: 401), then
// look the session id up in the in-memory revocation set (revoked: 401). The database is never touched. A replica
// whose revocation poll has been failing for too long refuses instead of trusting a stale list.
func (u *AuthUseCase) OpenSession(cookieValue string) (SessionPass, error) {
	ticket, err := u.rt.Codec.Open(cookieValue, time.Now())
	switch {
	case errors.Is(err, session.ErrExpiredTicket):
		return SessionPass{}, authModel.ErrAuthSessionExpired.Err()
	case err != nil:
		return SessionPass{}, authModel.ErrAuthInvalidSession.WithError(err).Err()
	}
	if !u.rt.Revocations.Healthy() {
		return SessionPass{}, authModel.ErrAuthSessionsUnavailable.Err()
	}
	if u.rt.Revocations.Revoked(ticket.SessionID) {
		// Category A: the same client-facing error as an unparseable cookie.
		return SessionPass{}, authModel.ErrAuthInvalidSession.WithError(errors.New("session revoked")).Err()
	}
	return SessionPass{Ticket: ticket}, nil
}

// LoadCaller is step 4: the one query that loads the caller's global role and status. A blocked or removed
// account is refused with the 401 of a dead session (and its cookie is cleared by the middleware).
func (u *AuthUseCase) LoadCaller(ctx context.Context, pass SessionPass) (authModel.AuthClaims, error) {
	access, err := u.users.GetAccess(ctx, pass.Ticket.UserID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return authModel.AuthClaims{}, authModel.ErrAuthInvalidSession.
				WithError(errors.New("user of the session is gone")).Err()
		}
		return authModel.AuthClaims{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get user for session").Err()
	}
	if access.Status == userModel.UserStatusBlocked {
		return authModel.AuthClaims{}, authModel.ErrAuthInvalidSession.
			WithError(errors.New("user of the session is blocked")).Err()
	}
	u.rt.Seen.Touch(pass.Ticket.SessionID, pass.Ticket.UserID)
	return authModel.AuthClaims{SessionID: pass.Ticket.SessionID, UserID: pass.Ticket.UserID, Role: rbac.Role(access.Role)}, nil
}

// ReissueCookie returns a new cookie with a new expiry once 1% of the idle TTL has passed since this one was
// issued; ok is false on every other request (no Set-Cookie). The frontend knows nothing about it.
func (u *AuthUseCase) ReissueCookie(pass SessionPass) (value string, ok bool) {
	now := time.Now()
	if !pass.Ticket.ReissueDue(now, u.lifetimes()) {
		return "", false
	}
	sealed, err := u.rt.Codec.Seal(pass.Ticket.Reissue(now, u.lifetimes()))
	if err != nil {
		log.Warn().Err(err).Msg("Failed to re-issue the session cookie")
		return "", false
	}
	return sealed, true
}

// ValidateSessionCookie is the whole check for a caller outside the permission gate (the Google link callback):
// the cookie, the revocation set and the user.
func (u *AuthUseCase) ValidateSessionCookie(ctx context.Context, cookieValue string) (*SessionAuthResult, error) {
	pass, err := u.OpenSession(cookieValue)
	if err != nil {
		return nil, err
	}
	claims, err := u.LoadCaller(ctx, pass)
	if err != nil {
		return nil, err
	}
	return &SessionAuthResult{Claims: claims}, nil
}
