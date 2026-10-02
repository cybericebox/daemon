package jobsModel

// ErrorJournalPurgeArgs is the empty payload of the daily purge of the error journal (groups, samples and 404
// counters past the retention).
type ErrorJournalPurgeArgs struct{}

func (ErrorJournalPurgeArgs) Kind() string { return "error_journal_purge" }
