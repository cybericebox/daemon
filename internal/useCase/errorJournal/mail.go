package errorJournalUseCase

import (
	"context"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"
)

type SMTPProviderState struct {
	ID    uuid.UUID
	Name  string
	Usage mailModel.Usage
}

type smtpProviderReader interface {
	SMTPProviderStates(context.Context) ([]SMTPProviderState, error)
}

// Reconcile persisted provider status, including failures from before this
// process started. Re-reading the same snapshot never counts as another send.
func (j *Journal) CheckSMTPProviders(ctx context.Context) {
	reader, ok := j.repo.(smtpProviderReader)
	if !ok {
		return
	}
	providers, err := reader.SMTPProviderStates(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read SMTP provider status")
		return
	}
	for _, p := range providers {
		if p.Usage.LastError == "" || p.Usage.LastErrorAt == nil {
			continue
		}
		e := errorJournal.SMTPProviderFailure(p.ID, p.Name, p.Usage.LastError, *p.Usage.LastErrorAt)
		fp := errorJournal.Fingerprint(e)
		groups, _, err := j.repo.ListGroups(ctx, GroupFilter{Kinds: []errorJournal.Kind{errorJournal.KindMail}, Query: e.Source, Limit: 100})
		if err != nil {
			log.Warn().Err(err).Msg("Failed to read SMTP provider error history")
			continue
		}
		var existing *errorJournal.Group
		for _, g := range groups {
			if g.Fingerprint == fp {
				copy := g
				existing = &copy
				break
			}
		}
		active := p.Usage.ErrorActive()
		if existing == nil || existing.LastSeenAt.Before(e.At) {
			// Old recovered failures outside retention must not be recreated after every purge.
			if !active && e.At.Before(j.now().Add(-j.cfg.Retention)) {
				continue
			}
			if !active {
				e.Notify = errorJournal.NotifyNever
			}
			if rec, err := j.Record(ctx, e); err != nil {
				log.Warn().Err(err).Msg("Failed to register SMTP provider error")
				continue
			} else {
				existing = &rec.Group
			}
		}
		if !active && p.Usage.LastUsedAt != nil {
			// The cutoff prevents an old successful snapshot from closing a newer failure.
			if err := j.repo.ResolveByFingerprint(ctx, fp, *p.Usage.LastUsedAt); err != nil {
				log.Warn().Err(err).Msg("Failed to resolve SMTP provider error")
			} else if existing != nil && existing.Status == errorJournal.StatusOpen {
				if current, err := j.repo.GetGroup(ctx, existing.ID); err == nil && current.Status == errorJournal.StatusResolved {
					j.hub.Publish(StreamEvent{Group: current})
				}
			}
		}
	}
}
