package errorJournalUseCase

import (
	"context"
	"errors"
	"testing"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
)

// Existing journal fixtures represent registered, confirmed operators.
func (r *fakeRepo) RecipientUsers(_ context.Context, emails []string) ([]RecipientUser, error) {
	out := make([]RecipientUser, 0, len(emails))
	for _, email := range emails {
		out = append(out, RecipientUser{Email: email, Role: "super_admin", Status: "active", EmailConfirmed: true})
	}
	return out, nil
}

type recipientRepo struct {
	*fakeRepo
	users     []RecipientUser
	lookupErr error
}

func (r *recipientRepo) RecipientUsers(context.Context, []string) ([]RecipientUser, error) {
	return r.users, r.lookupErr
}

func TestRecipientEligibilityRequiresActiveAndConfirmed(t *testing.T) {
	h := newHarness(DefaultConfig())
	r := &recipientRepo{fakeRepo: h.repo, users: []RecipientUser{
		{Email: "ok@example.org", Role: "super_admin", Status: "active", EmailConfirmed: true},
		{Email: "invited@example.org", Status: "incomplete", EmailConfirmed: true},
		{Email: "unconfirmed@example.org", Role: "super_admin", Status: "active"},
		{Email: "blocked@example.org", Role: "super_admin", Status: "blocked", EmailConfirmed: true},
		{Email: "ordinary@example.org", Role: "user", Status: "active", EmailConfirmed: true},
	}}
	h.j.repo = r
	got, err := h.j.recipientsEligible(context.Background(), errorJournal.Settings{Emails: []string{"ok@example.org", "invited@example.org", "unconfirmed@example.org", "blocked@example.org", "ordinary@example.org", "missing@example.org"}})
	if err != nil || len(got) != 1 || got[0] != "ok@example.org" {
		t.Fatalf("eligible=%v err=%v", got, err)
	}
	r.lookupErr = errors.New("database unavailable")
	if _, err = h.j.recipientsEligible(context.Background(), errorJournal.Settings{Emails: []string{"ok@example.org"}}); err == nil {
		t.Fatal("lookup failure must retry, not erase recipients")
	}
}

func TestSettingsRejectUnknownAndBlockedButKeepPending(t *testing.T) {
	for _, tc := range []struct {
		name   string
		users  []RecipientUser
		reject bool
	}{
		{"unknown", nil, true},
		{"blocked", []RecipientUser{{Email: "new@example.org", Status: "blocked", EmailConfirmed: true}}, true},
		{"invited", []RecipientUser{{Email: "new@example.org", Status: "incomplete", EmailConfirmed: true}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(DefaultConfig())
			h.j.repo = &recipientRepo{fakeRepo: h.repo, users: tc.users}
			got, err := h.j.SaveErrorJournalSettings(context.Background(), SettingsInput{Emails: []string{"new@example.org"}})
			if (err != nil) != tc.reject {
				t.Fatalf("err=%v reject=%v", err, tc.reject)
			}
			if !tc.reject && (len(got.EmailRecipients) != 1 || got.EmailRecipients[0].Eligible) {
				t.Fatalf("pending metadata=%+v", got.EmailRecipients)
			}
		})
	}
}

func TestQueuedRecipientRechecksRoleAtDeliveryTime(t *testing.T) {
	h := newHarness(DefaultConfig())
	r := &recipientRepo{fakeRepo: h.repo, users: []RecipientUser{{Email: "ops@example.org", Role: "super_admin", Status: "active", EmailConfirmed: true}}}
	h.j.repo = r
	eligible, err := h.j.deliveryEmailEligible(context.Background(), "ops@example.org")
	if err != nil || !eligible {
		t.Fatalf("initial recipient eligible=%v err=%v", eligible, err)
	}
	r.users[0].Role = "user"
	eligible, err = h.j.deliveryEmailEligible(context.Background(), "ops@example.org")
	if err != nil || eligible {
		t.Fatalf("demoted recipient eligible=%v err=%v", eligible, err)
	}
	r.users[0].Role = "super_admin"
	r.users[0].Status = "incomplete"
	eligible, err = h.j.deliveryEmailEligible(context.Background(), "ops@example.org")
	if err != nil || eligible {
		t.Fatalf("unaccepted invitation eligible=%v err=%v", eligible, err)
	}
}
