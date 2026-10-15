package errorJournalRepo

import (
	"context"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
)

func (r *Repository) SMTPProviderStates(ctx context.Context) ([]errorJournalUseCase.SMTPProviderState, error) {
	rows, err := r.q.ListPlatformSMTPProviders(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]errorJournalUseCase.SMTPProviderState, 0, len(rows))
	for _, row := range rows {
		u := mailModel.Usage{LastError: row.LastError}
		if row.LastErrorAt.Valid {
			at := row.LastErrorAt.Time
			u.LastErrorAt = &at
		}
		if row.LastUsedAt.Valid {
			at := row.LastUsedAt.Time
			u.LastUsedAt = &at
		}
		out = append(out, errorJournalUseCase.SMTPProviderState{ID: row.ID, Name: row.Name, Usage: u})
	}
	return out, nil
}
