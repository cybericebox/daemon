package auth

import (
	"context"

	"github.com/cybericebox/daemon/internal/model"
)

// SessionEmail is the address of the account behind a session cookie that was just issued: what the browser
// remembers as the last sign-in address (the sign-in page prefills it, and Google's chooser is hinted with it).
func (u *AuthUseCase) SessionEmail(ctx context.Context, sessionCookie string) (string, error) {
	pass, err := u.OpenSession(sessionCookie)
	if err != nil {
		return "", err
	}
	user, err := u.users.GetByID(ctx, pass.Ticket.UserID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to read the account of the session").Err()
	}
	return user.Email, nil
}
