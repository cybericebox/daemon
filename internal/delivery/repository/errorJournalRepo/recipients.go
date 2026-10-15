package errorJournalRepo

import (
	"context"
	"errors"
	"strings"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
	"github.com/jackc/pgx/v5"
)

// RecipientUsers reuses the identity repository's canonical normalized lookup.
// This read model deliberately omits credentials and deleted users.
func (r *Repository) RecipientUsers(ctx context.Context, emails []string) ([]errorJournalUseCase.RecipientUser, error) {
	out := make([]errorJournalUseCase.RecipientUser, 0, len(emails))
	for _, email := range emails {
		u, err := r.q.GetUserByEmail(ctx, strings.ToLower(strings.TrimSpace(email)))
		if errors.Is(err, pgx.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		out = append(out, recipientOf(u))
	}
	return out, nil
}
func recipientOf(u postgres.User) errorJournalUseCase.RecipientUser {
	return errorJournalUseCase.RecipientUser{Email: u.Email, UserID: u.ID, FirstName: u.FirstName, LastName: u.LastName, Role: u.Role, Status: u.Status, EmailConfirmed: u.EmailConfirmed}
}
