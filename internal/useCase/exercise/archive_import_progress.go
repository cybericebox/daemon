package exercise

import (
	"context"
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
	u    *ExerciseUseCase
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
	next := p.job.Progress
	next.Stage = stage
	p.report(ctx, next, true)
}

// tasksTotal records how many tasks the archive holds (known once it is parsed).
func (p *importProgress) tasksTotal(ctx context.Context, total int) {
	if p == nil {
		return
	}
	next := p.job.Progress
	next.TasksTotal = total
	p.report(ctx, next, true)
}

// images starts the image stage with the number and the size of the image files to push.
func (p *importProgress) images(ctx context.Context, total int, bytes int64) {
	if p == nil {
		return
	}
	next := p.job.Progress
	next.Stage, next.ImagesTotal, next.BytesTotal = exerciseModel.ImportStageImages, total, bytes
	p.report(ctx, next, true)
}

// imagePushed counts a pushed image of the given size.
func (p *importProgress) imagePushed(ctx context.Context, size int64) {
	if p == nil {
		return
	}
	next := p.job.Progress
	next.ImagesDone++
	next.BytesPushed += size
	p.report(ctx, next, false)
}

// taskCreated counts a created task.
func (p *importProgress) taskCreated(ctx context.Context) {
	if p == nil {
		return
	}
	next := p.job.Progress
	next.TasksDone++
	p.report(ctx, next, false)
}

// report keeps the progress in memory always and writes it when forced or when the interval has passed since the
// last write. The first write of a count is never held back by an interval that never started.
func (p *importProgress) report(ctx context.Context, next exerciseModel.ImportProgress, force bool) {
	now := p.u.timeNow()
	if !p.job.Report(next, now) {
		return
	}
	if !force && !p.last.IsZero() && now.Sub(p.last) < importProgressInterval {
		return
	}
	c, cancel := context.WithTimeout(context.WithoutCancel(ctx), importStateTimeout)
	defer cancel()
	p.last = now
	if _, err := p.u.exercises.SaveImportJobProgress(c, p.job); err != nil {
		log.Warn().Err(err).Str("job", p.job.ID.String()).Msg("archive import: recording the progress failed")
	}
}
