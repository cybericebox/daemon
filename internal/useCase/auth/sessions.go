package auth

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
)

// SignOut deletes the master session.
func (u *AuthUseCase) SignOut(ctx context.Context, sessionID uuid.UUID) error {
	if sessionID == uuid.Nil {
		return nil // already signed out
	}
	if _, err := u.sessions.Delete(ctx, sessionID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete session").Err()
	}
	return nil
}

// UpdateLastSeen records activity and extends the idle TTL. It touches both the
// session (per-session last_seen + idle TTL) and the user row's last_seen.
// The user-level last_seen is a durable activity marker: sessions are deleted on
// sign-out/revoke, so it cannot be reliably derived from the sessions table
// alone — it is updated in lock-step here.
func (u *AuthUseCase) UpdateLastSeen(ctx context.Context, sessionID, userID uuid.UUID) error {
	if _, err := u.sessions.Touch(ctx, sessionID, time.Now().Add(u.cfg.SessionIdleTTL)); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to touch session").Err()
	}
	if _, err := u.users.TouchLastSeen(ctx, userID); err != nil {
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
	affected, err := u.sessions.DeleteForUser(ctx, sessionID, userID)
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
	if _, err := u.sessions.DeleteForUserExcept(ctx, userID, currentSessionID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to revoke other sessions").Err()
	}
	return nil
}
