package auth

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// fakeHashedPassword is a valid bcrypt hash used for constant-time comparison
// when no user (or no password) is found, so sign-in timing does not leak
// account existence. It is the bcrypt hash of a random string.
const fakeHashedPassword = "$2a$10$N9qo8uLOickgx2ZMRZoMyeIjZAgcfl7p92ldGxad68LJZdL17lhWy"

// SignIn validates credentials and, on success, creates a session and returns
// the session cookie plus the resolved safe redirect (falls back to the
// profile page for an empty or untrusted redirect).
func (u *AuthUseCase) SignIn(
	ctx context.Context,
	emailAddr, plainPassword, redirect string,
	meta authModel.SessionMetadata,
) (sessionCookie, safeRedirect string, err error) {
	safeRedirect = u.resolveRedirect(redirect)

	account := normalizeEmail(emailAddr)
	// Throttled BEFORE any lookup or hashing: a locked address costs the server
	// nothing and answers the same whether or not the account exists.
	if wait := u.limits.signInAccount.Locked(account); wait > 0 {
		return "", "", tooManyRequests(wait)
	}
	defer func() {
		switch {
		case err == nil:
			u.limits.signInAccount.Reset(account)
		case errors.Is(err, authModel.ErrAuthInvalidUserCredentials.Err()):
			u.limits.signInAccount.Fail(account)
		}
	}()

	user, dbErr := u.users.GetByEmail(ctx, account)
	if dbErr != nil && !repositoryTools.IsObjectNotFoundError(dbErr) {
		return "", "", model.ErrPlatform.WithError(dbErr).WithMessage("Failed to get user by email").Err()
	}

	// timing-safe: always run bcrypt even when the user is absent or has no hash.
	if repositoryTools.IsObjectNotFoundError(dbErr) || !user.HasPassword() {
		_, _ = u.password.Matches(plainPassword, fakeHashedPassword)
		return "", "", authModel.ErrAuthInvalidUserCredentials.Err()
	}

	matches, mErr := u.password.Matches(plainPassword, user.HashedPassword)
	if mErr != nil {
		return "", "", model.ErrPlatform.WithError(mErr).WithMessage("Failed to check password").Err()
	}
	if !matches {
		return "", "", authModel.ErrAuthInvalidUserCredentials.Err()
	}
	// Only after the password matched: revealing "blocked" earlier would let
	// anyone probe account status by email.
	if err = refuseBlocked(&user); err != nil {
		return "", "", err
	}

	cookie, err := u.createSession(ctx, user.ID, meta)
	if err != nil {
		return "", "", err
	}
	return cookie, safeRedirect, nil
}

// createSession persists a new domain session (all defaults from the factory)
// and mints the session cookie.
func (u *AuthUseCase) createSession(
	ctx context.Context,
	userID uuid.UUID,
	meta authModel.SessionMetadata,
) (string, error) {
	session, err := u.sessions.Create(ctx, authModel.NewSession(userID, meta, u.cfg.SessionIdleTTL, time.Now()))
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to create session").Err()
	}

	// The cookie lives for the absolute lifetime; the idle deadline slides in the database.
	cookieExpires := session.ExpiresAt
	if u.cfg.SessionAbsoluteTTL > 0 {
		cookieExpires = session.CreatedAt.Add(u.cfg.SessionAbsoluteTTL)
	}
	u.evictOldestSessions(ctx, userID, session.ID)
	cookie, err := u.token.GenerateSessionCookie(session.ID, cookieExpires)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to generate session cookie").Err()
	}
	return cookie, nil
}

// resolveRedirect returns raw if it is a trusted platform URL, else the
// account's default landing page.
func (u *AuthUseCase) resolveRedirect(raw string) string {
	if raw != "" && u.IsTrustedRedirect(raw) {
		return raw
	}
	return u.cfg.Hosts.IDURL("/profile")
}

// refuseBlocked is the single "account is blocked" guard shared by every path
// that grants or resolves a session (password and Google sign-in, session
// validation).
func refuseBlocked(user *userModel.User) error {
	if user.IsBlocked() {
		return authModel.ErrAuthAccountBlocked.Err()
	}
	return nil
}

// trustedReturnTo returns raw when it is a trusted platform URL, else "" (no
// default: an absent return_to lets the frontend pick its own landing).
func (u *AuthUseCase) trustedReturnTo(raw string) string {
	if raw != "" && u.IsTrustedRedirect(raw) {
		return raw
	}
	return ""
}

// ValidateSessionCookie parses the session cookie (Stage 1) then loads the
// session and user from the DB (Stage 2).
func (u *AuthUseCase) ValidateSessionCookie(ctx context.Context, cookieValue string) (*SessionAuthResult, error) {
	sessionID, err := u.token.ParseSessionCookie(cookieValue)
	if err != nil {
		return nil, authModel.ErrAuthInvalidSession.Err()
	}
	return u.resolveSession(ctx, sessionID)
}

// resolveSession loads the session + user, checks expiry, and builds claims.
func (u *AuthUseCase) resolveSession(ctx context.Context, sessionID uuid.UUID) (*SessionAuthResult, error) {
	session, err := u.sessions.GetByID(ctx, sessionID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			// 401, not 404: a valid cookie whose session row is gone is a dead
			// credential — the client must be sent to sign-in. Deliberately the
			// same client-facing error as an unparseable cookie (category A):
			// a distinct code would confirm the session once existed. The real
			// reason goes to server logs via WithError.
			return nil, authModel.ErrAuthInvalidSession.
				WithError(errors.New("session row not found (revoked or deleted)")).Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get session").Err()
	}
	if session.IsExpired(time.Now()) || session.ExceedsAbsoluteLifetime(u.cfg.SessionAbsoluteTTL, time.Now()) {
		return nil, authModel.ErrAuthSessionExpired.Err()
	}
	user, err := u.users.GetByID(ctx, session.UserID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user for session").Err()
	}
	if err = refuseBlocked(&user); err != nil {
		return nil, err
	}

	return &SessionAuthResult{
		Claims: authModel.AuthClaims{
			SessionID: session.ID,
			UserID:    session.UserID,
			Role:      rbac.Role(user.Role),
		},
		Session: &session,
	}, nil
}

// evictOldestSessions keeps the account within SESSION_MAX_PER_USER: the sessions beyond the cap,
// oldest first, are ended (never the one just created). Best effort: a failure here must not fail
// the sign-in, and the next sign-in trims again.
func (u *AuthUseCase) evictOldestSessions(ctx context.Context, userID, keep uuid.UUID) {
	limit := u.cfg.SessionMaxPerUser
	if limit <= 0 {
		return
	}
	sessions, err := u.sessions.ListByUser(ctx, userID)
	if err != nil {
		log.Warn().Err(err).Str("user_id", userID.String()).Msg("Failed to list sessions for the cap")
		return
	}
	if len(sessions) <= limit {
		return
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].CreatedAt.Before(sessions[j].CreatedAt) })
	for _, s := range sessions[:len(sessions)-limit] {
		if s.ID == keep {
			continue
		}
		if _, err = u.sessions.DeleteForUser(ctx, s.ID, userID); err != nil {
			log.Warn().Err(err).Str("user_id", userID.String()).Msg("Failed to evict a session over the cap")
		}
	}
}
