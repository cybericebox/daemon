package auth

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	sessionRepo "github.com/cybericebox/daemon/internal/delivery/repository/sessionRepo"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/session"
)

// SignOut ends the session: its row goes and the revocation is written.
func (u *AuthUseCase) SignOut(ctx context.Context, sessionID uuid.UUID) error {
	if sessionID == uuid.Nil {
		return nil // already signed out
	}
	if _, err := u.revokeOne(ctx, sessionID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete session").Err()
	}
	return nil
}

// WriteSeen stores the last activity of a session and of its user (session.SeenWriter). The idle deadline of
// the row follows it; neither moves backward.
func (u *AuthUseCase) WriteSeen(ctx context.Context, sessionID, userID uuid.UUID, at time.Time) error {
	if _, err := u.sessions.Touch(ctx, sessionID, at, at.Add(u.cfg.SessionIdleTTL)); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to touch session").Err()
	}
	if _, err := u.users.TouchLastSeen(ctx, userID, at); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to touch user last_seen").Err()
	}
	return nil
}

// ListSessions returns the user's active sessions, flagging the current one.
func (u *AuthUseCase) ListSessions(ctx context.Context, userID, currentSessionID uuid.UUID) ([]SessionInfo, error) {
	sessions, err := u.sessions.ListByUser(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list sessions").Err()
	}
	out := make([]SessionInfo, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, SessionInfo{
			ID:        s.ID,
			UserAgent: s.Metadata.UserAgent,
			IP:        s.Metadata.IP,
			LastSeen:  s.LastSeen,
			IsCurrent: s.ID == currentSessionID,
			CreatedAt: s.CreatedAt,
		})
	}
	return out, nil
}

// RevokeSession revokes one of the user's sessions; ownership is enforced in SQL.
func (u *AuthUseCase) RevokeSession(ctx context.Context, userID, sessionID uuid.UUID) error {
	affected, err := u.revokeOwned(ctx, sessionID, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to revoke session").Err()
	}
	if affected == 0 {
		return authModel.ErrAuthSessionNotFound.Err()
	}
	return nil
}

// RevokeOtherSessions revokes all of the user's sessions except the current one.
func (u *AuthUseCase) RevokeOtherSessions(ctx context.Context, userID, currentSessionID uuid.UUID) error {
	// Guard: a nil current id would make the "<>" predicate wipe every session.
	if currentSessionID == uuid.Nil {
		return model.ErrPlatform.WithMessage("Session ID missing from context").Err()
	}
	if _, err := u.revokeAllExcept(ctx, userID, currentSessionID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to revoke other sessions").Err()
	}
	return nil
}

// The ways a session ends. Each deletes the session rows and writes their revocation rows in one statement; the
// revocations are also added to this replica's set at once, the others see them within the poll.

func (u *AuthUseCase) noted(rows []sessionRepo.Revoked, err error) (int64, error) {
	if u.rt != nil {
		for _, row := range rows {
			u.rt.Revocations.Add(row.SessionID, row.ExpiresAt)
		}
	}
	return int64(len(rows)), err
}

func (u *AuthUseCase) revokeOne(ctx context.Context, sessionID uuid.UUID) (int64, error) {
	return u.noted(u.sessions.Revoke(ctx, sessionID, u.lifetimes()))
}

func (u *AuthUseCase) revokeOwned(ctx context.Context, sessionID, userID uuid.UUID) (int64, error) {
	return u.noted(u.sessions.RevokeForUser(ctx, sessionID, userID, u.lifetimes()))
}

func (u *AuthUseCase) revokeAll(ctx context.Context, userID uuid.UUID) (int64, error) {
	return u.noted(u.sessions.RevokeAllForUser(ctx, userID, u.lifetimes()))
}

func (u *AuthUseCase) revokeAllExcept(ctx context.Context, userID, keep uuid.UUID) (int64, error) {
	return u.noted(u.sessions.RevokeForUserExcept(ctx, userID, keep, u.lifetimes()))
}

// lifetimes are the TTLs the revocation expiry is computed from.
func (u *AuthUseCase) lifetimes() session.Lifetimes {
	return session.Lifetimes{Idle: u.cfg.SessionIdleTTL, Absolute: u.cfg.SessionAbsoluteTTL}
}
