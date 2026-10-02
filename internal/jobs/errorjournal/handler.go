// Package errorjournalJob connects the job queue to the platform error journal: every failed attempt, discarded job
// and panic of a River job is recorded, and the purge of old journal rows runs as a periodic job.
package errorjournalJob

import (
	"context"
	"fmt"
	"strconv"

	"github.com/riverqueue/river"
	"github.com/riverqueue/river/rivertype"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

// ErrorHandler is River's error handler: it reports to the installed error journal reporter. It never changes how a
// job is retried. Job arguments are NOT recorded: they hold addresses, names and links.
type ErrorHandler struct{}

func (ErrorHandler) HandleError(_ context.Context, job *rivertype.JobRow, err error) *river.ErrorHandlerResult {
	// A job that used its last attempt is discarded: that is the one to tell people about. An earlier failure is
	// recorded, and told only when its fingerprint is new.
	final := job.Attempt >= job.MaxAttempts
	rule := errorJournal.NotifyNew
	if final {
		rule = errorJournal.NotifyAlways
	}
	errorJournal.Report(errorJournal.Event{
		Kind: errorJournal.KindJob, Source: job.Kind, Message: err.Error(), Notify: rule, Details: details(job, final),
	})
	return nil
}

func (ErrorHandler) HandlePanic(_ context.Context, job *rivertype.JobRow, panicVal any, trace string) *river.ErrorHandlerResult {
	errorJournal.Report(errorJournal.Event{
		Kind: errorJournal.KindPanic, Source: job.Kind, Message: sprint(panicVal), Stack: trace,
		Details: details(job, job.Attempt >= job.MaxAttempts),
	})
	return nil
}

func details(job *rivertype.JobRow, final bool) map[string]string {
	return map[string]string{
		"job_id": strconv.FormatInt(job.ID, 10), "queue": job.Queue,
		"attempt": strconv.Itoa(job.Attempt), "max_attempts": strconv.Itoa(job.MaxAttempts),
		"discarded": strconv.FormatBool(final),
	}
}

func sprint(v any) string { return fmt.Sprint(v) }

// IUseCase is the purge job's port.
type IUseCase interface{ PurgeErrorJournal(context.Context) error }

type purgeWorker struct {
	river.WorkerDefaults[jobsModel.ErrorJournalPurgeArgs]
	uc IUseCase
}

// NewPurgeWorker builds the worker of the daily purge: groups, samples and 404 counters past the retention.
func NewPurgeWorker(uc IUseCase) *purgeWorker { return &purgeWorker{uc: uc} }

func (w *purgeWorker) Work(ctx context.Context, _ *river.Job[jobsModel.ErrorJournalPurgeArgs]) error {
	return w.uc.PurgeErrorJournal(ctx)
}
