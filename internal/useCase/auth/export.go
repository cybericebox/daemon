package auth

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// dataExportInterval is how often one person may download their data.
const dataExportInterval = 10 * time.Minute

// requireRecentSignIn is the "fresh session" check of a sensitive action that has no password to send (a plain
// download link): the sign-in must be from the last minutes, otherwise the client sends the person through
// sign-in again.
func requireRecentSignIn(ctx context.Context) error {
	claims, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok || claims.SessionID == uuid.Nil {
		return authModel.ErrAuthReauthRequired.Err()
	}
	if claims.SignedInAt.IsZero() || time.Since(claims.SignedInAt) > recentSignInWindow {
		return authModel.ErrAuthReauthRequired.Err()
	}
	return nil
}

// ExportMyData collects what the platform stores about the signed-in person: profile, the ways to sign in (kind,
// name, dates; never a hash or key), active sessions, event participations, teams (by title), their own flag
// submissions and form answers, invitations and the inbox. Nothing about other people is read. At most once in
// dataExportInterval per person, and only on a fresh sign-in.
func (u *AuthUseCase) ExportMyData(
	ctx context.Context,
	userID uuid.UUID,
) (*userModel.DataExport, error) {
	if err := requireRecentSignIn(ctx); err != nil {
		return nil, err
	}
	if ok, wait := u.limits.dataExport.Allow(userID.String()); !ok {
		return nil, tooManyRequests(wait)
	}

	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	out := &userModel.DataExport{
		ExportedAt: time.Now().UTC(),
		Profile: userModel.ExportProfile{
			ID:        user.ID,
			Email:     user.Email,
			FirstName: user.FirstName,
			LastName:  user.LastName,
			Role: string(
				user.Role,
			),
			Status:         string(user.Status),
			EmailConfirmed: user.EmailConfirmed,
			CreatedAt:      user.CreatedAt,
			LastSeen:       user.LastSeen,
			TosAcceptedAt:  user.TosAcceptedAt,
			LegalDocuments: user.LegalDocuments,
		},
		SignInMethods: []userModel.ExportSignInMethod{},
	}
	if user.HasPassword() {
		out.SignInMethods = append(
			out.SignInMethods,
			userModel.ExportSignInMethod{Type: "password"},
		)
	}
	providers, err := u.users.ListProviders(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to get user providers").
			Err()
	}
	for _, provider := range providers {
		out.SignInMethods = append(out.SignInMethods, userModel.ExportSignInMethod{Type: provider})
	}
	passkeys, err := u.passkeys.ListByUser(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list passkeys").Err()
	}
	for _, p := range passkeys {
		created := p.CreatedAt
		out.SignInMethods = append(out.SignInMethods, userModel.ExportSignInMethod{
			Type: "passkey", Name: p.Name, CreatedAt: &created, LastUsedAt: p.LastUsedAt,
		})
	}

	sessions, err := u.sessions.ListByUser(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list sessions").Err()
	}
	out.Sessions = make([]userModel.ExportSession, 0, len(sessions))
	for _, s := range sessions {
		out.Sessions = append(out.Sessions, userModel.ExportSession{
			UserAgent: s.Metadata.UserAgent, IP: authModel.SessionIP(s.Metadata.IP),
			CreatedAt: s.CreatedAt, LastSeen: s.LastSeen,
		})
	}

	if err = u.exports.Fill(ctx, userID, out); err != nil {
		return nil, model.ErrPlatform.WithError(err).
			WithMessage("Failed to collect user data").
			Err()
	}
	return out, nil
}
