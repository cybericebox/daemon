package errorJournalUseCase

import (
	"context"
	"strings"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/gofrs/uuid"
)

// RecipientUser is notification-settings metadata, not an authentication claim.
// Invited accounts may have EmailConfirmed already, so activation is also required.
type RecipientUser struct {
	Email          string
	UserID         uuid.UUID
	FirstName      string
	LastName       string
	Role           string
	Status         string
	EmailConfirmed bool
	Eligible       bool
}

func recipientEligible(u RecipientUser) bool {
	return u.Role == "super_admin" && u.Status == "active" && u.EmailConfirmed
}

func (j *Journal) recipientMetadata(ctx context.Context, emails []string) ([]RecipientUser, error) {
	users, err := j.repo.RecipientUsers(ctx, emails)
	if err != nil {
		return nil, err
	}
	byEmail := map[string]RecipientUser{}
	for _, u := range users {
		u.Email = strings.ToLower(u.Email)
		u.Eligible = recipientEligible(u)
		byEmail[u.Email] = u
	}
	out := make([]RecipientUser, 0, len(emails))
	for _, email := range emails {
		email = strings.ToLower(strings.TrimSpace(email))
		u, ok := byEmail[email]
		if !ok {
			u = RecipientUser{Email: email, Status: "unavailable"}
		}
		out = append(out, u)
	}
	return out, nil
}

func (j *Journal) validateRecipientUsers(ctx context.Context, emails []string) error {
	users, err := j.recipientMetadata(ctx, emails)
	if err != nil {
		return err
	}
	for _, u := range users {
		if u.Status != "active" && u.Status != "incomplete" {
			return errorJournal.ErrRecipientUnavailable.Err()
		}
	}
	return nil
}

func (j *Journal) recipientsEligible(ctx context.Context, s errorJournal.Settings) ([]string, error) {
	emails := append([]string{}, s.Emails...)
	if s.EmailToSuperAdmins {
		admins, err := j.repo.SuperAdminEmails(ctx)
		if err != nil {
			return nil, err
		}
		emails = append(emails, admins...)
	}
	users, err := j.recipientMetadata(ctx, emails)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	out := []string{}
	for _, u := range users {
		if u.Eligible && !seen[u.Email] {
			seen[u.Email] = true
			out = append(out, u.Email)
		}
	}
	return out, nil
}

// Recheck at execution time: removing/blocking a recipient cancels queued alerts.
func (j *Journal) deliveryEmailEligible(ctx context.Context, email string) (bool, error) {
	settings, err := j.repo.GetSettings(ctx)
	if err != nil {
		return false, err
	}
	recipients, err := j.recipientsEligible(ctx, settings)
	if err != nil {
		return false, err
	}
	for _, to := range recipients {
		if strings.EqualFold(to, email) {
			return true, nil
		}
	}
	return false, nil
}
