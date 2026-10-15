package errorJournalUseCase

import (
	"context"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func (r *fakeRepo) SMTPProviderStates(context.Context) ([]SMTPProviderState, error) {
	return r.smtpProviders, nil
}

func TestSMTPProviders_BackfillRecoveryAndNewFailure(t *testing.T) {
	h := newHarness(DefaultConfig())
	failedAt := h.now.Add(-time.Hour)
	usedAt := failedAt.Add(time.Minute)
	a, b := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	h.repo.smtpProviders = []SMTPProviderState{
		{ID: a, Name: "Brevo", Usage: mailModel.Usage{LastError: "SMTP timeout", LastErrorAt: &failedAt, LastUsedAt: &usedAt}},
		{ID: b, Name: "AWS", Usage: mailModel.Usage{LastError: "SMTP timeout", LastErrorAt: &failedAt}},
	}
	h.j.CheckSMTPProviders(ctx)
	require.Len(t, h.repo.groups, 2)
	require.Equal(t, 1, openGroups(h, errorJournal.KindMail), "Brevo success does not recover AWS")
	for _, g := range h.repo.groups {
		require.Equal(t, failedAt, g.LastSeenAt)
		if g.Source == "smtp-provider:"+a.String() {
			require.Equal(t, errorJournal.StatusResolved, g.Status)
		}
	}
	h.j.CheckSMTPProviders(ctx)
	for _, g := range h.repo.groups {
		require.Equal(t, int64(1), g.Occurrences, "a snapshot is not another failed send")
	}
	h.repo.smtpProviders[1].Usage.LastUsedAt = &usedAt
	h.j.CheckSMTPProviders(ctx)
	require.Equal(t, 0, openGroups(h, errorJournal.KindMail))
	newFailure := usedAt.Add(time.Second)
	h.repo.smtpProviders[0].Usage.LastErrorAt = &newFailure
	h.j.CheckSMTPProviders(ctx)
	require.Equal(t, 1, openGroups(h, errorJournal.KindMail))
}

func TestSMTPProviders_OldSuccessCannotResolveNewerFailure(t *testing.T) {
	h := newHarness(DefaultConfig())
	id := uuid.Must(uuid.NewV7())
	oldFailure := h.now.Add(-time.Hour)
	oldSuccess := oldFailure.Add(time.Minute)
	newFailure := oldSuccess.Add(time.Minute)
	rec, err := h.j.Record(ctx, errorJournal.SMTPProviderFailure(id, "Brevo", "new timeout", newFailure))
	require.NoError(t, err)
	h.repo.smtpProviders = []SMTPProviderState{{ID: id, Name: "Brevo", Usage: mailModel.Usage{LastError: "old timeout", LastErrorAt: &oldFailure, LastUsedAt: &oldSuccess}}}
	h.j.CheckSMTPProviders(ctx)
	current, err := h.repo.GetGroup(ctx, rec.Group.ID)
	require.NoError(t, err)
	require.Equal(t, errorJournal.StatusOpen, current.Status)
	require.Equal(t, newFailure, current.LastSeenAt)
}

func TestSMTPProviders_RecoveryPublishesLiveUpdate(t *testing.T) {
	h := newHarness(DefaultConfig())
	id := uuid.Must(uuid.NewV7())
	failedAt := h.now.Add(-time.Minute)
	successAt := h.now
	h.repo.smtpProviders = []SMTPProviderState{{ID: id, Name: "Brevo", Usage: mailModel.Usage{LastError: "timeout", LastErrorAt: &failedAt}}}
	h.j.CheckSMTPProviders(ctx)
	events, unsubscribe := h.j.hub.Subscribe()
	defer unsubscribe()
	h.repo.smtpProviders[0].Usage.LastUsedAt = &successAt
	h.j.CheckSMTPProviders(ctx)
	select {
	case event := <-events:
		require.Equal(t, errorJournal.StatusResolved, event.Group.Status)
	default:
		t.Fatal("recovery must update an open live journal")
	}
	h.j.CheckSMTPProviders(ctx)
	require.Empty(t, events, "unchanged recovered snapshots do not publish again")
}
