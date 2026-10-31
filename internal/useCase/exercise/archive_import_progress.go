package exercise

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// importProgressInterval is the least time between two throttled progress writes of a running import (a count
// moving); a change of stage is written at once, it is rare.
const importProgressInterval = 2 * time.Second

// importProgress records how far a running background import has come on its job. Every method is safe on a nil
// receiver (no job to report to). A write that fails is logged and skipped: progress is a courtesy, the outcome
// is written separately and retried.
type importProgress struct {
	u *ExerciseUseCase
	// mu guards job and last: the heartbeat writes from its own goroutine.
	mu   sync.Mutex
	job  exerciseModel.ImportJob
	last time.Time
}

func (u *ExerciseUseCase) newImportProgress(job exerciseModel.ImportJob) *importProgress {
	return &importProgress{u: u, job: job}
}

// stage moves to a stage and writes at once.
func (p *importProgress) stage(ctx context.Context, stage exerciseModel.ImportStage) {
	if p == nil {
		return
	}
	next := p.current()
	next.Stage = stage
	p.report(ctx, next, true)
}

// tasksTotal records how many tasks the archive holds (known once it is parsed).
func (p *importProgress) tasksTotal(ctx context.Context, total int) {
	if p == nil {
		return
	}
	next := p.current()
	next.TasksTotal = total
	p.report(ctx, next, true)
}

// images starts the image stage with the number and the size of the image files to push.
func (p *importProgress) images(ctx context.Context, total int, bytes int64) {
	if p == nil {
		return
	}
	next := p.current()
	next.Stage, next.ImagesTotal, next.BytesTotal = exerciseModel.ImportStageImages, total, bytes
	p.report(ctx, next, true)
}

// imagePushed counts a pushed image of the given size.
func (p *importProgress) imagePushed(ctx context.Context, size int64) {
	if p == nil {
		return
	}
	next := p.current()
	next.ImagesDone++
	next.BytesPushed += size
	p.report(ctx, next, false)
}

// taskCreated counts a created task.
func (p *importProgress) taskCreated(ctx context.Context) {
	if p == nil {
		return
	}
	next := p.current()
	next.TasksDone++
	p.report(ctx, next, false)
}

// report keeps the progress in memory always and writes it when forced or when the interval has passed since the
// last write. The first write of a count is never held back by an interval that never started.
func (p *importProgress) report(ctx context.Context, next exerciseModel.ImportProgress, force bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.u.timeNow()
	if !p.job.Report(next, now) {
		return
	}
	if !force && !p.last.IsZero() && now.Sub(p.last) < importProgressInterval {
		return
	}
	p.write(ctx, now)
}

// write saves the progress held now; the caller holds mu.
func (p *importProgress) write(ctx context.Context, now time.Time) {
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), importStateTimeout)
	defer cancel()
	p.last = now
	if _, err := p.u.exercises.SaveImportJobProgress(c, p.job); err != nil {
		log.Warn().Err(err).Str("job", p.job.ID.String()).Msg("archive import: recording the progress failed")
	}
}

// current is the progress held now.
func (p *importProgress) current() exerciseModel.ImportProgress {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.job.Progress
}

// snapshot is the job with the progress held now.
func (p *importProgress) snapshot() exerciseModel.ImportJob {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.job
}

// heartbeat writes the job's liveness mark every `every` until stop is called (it returns only after the last
// write): an import that goes quiet for exerciseModel.ImportJobStaleAfter reads as interrupted. A write that fails
// is logged and retried at the next beat.
func (p *importProgress) heartbeat(ctx context.Context, every time.Duration) (stop func()) {
	if p == nil {
		return func() {}
	}
	done, stopped := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(stopped)
		ticker := time.NewTicker(every)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				p.mu.Lock()
				if p.job.Report(p.job.Progress, p.u.timeNow()) {
					p.write(ctx, p.u.timeNow())
				}
				p.mu.Unlock()
			}
		}
	}()
	return func() {
		close(done)
		<-stopped
	}
}
