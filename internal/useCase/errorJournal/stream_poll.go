package errorJournalUseCase

import (
	"context"
	"sync"
	"time"

	"github.com/gofrs/uuid" //nolint:depguard // the uuid package of the whole journal
	"github.com/rs/zerolog/log"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
)

const (
	defaultStreamPoll = 3 * time.Second
	changesPerRead    = 200
	readTimeout       = 5 * time.Second
)

// Change is one row of the change log: an occurrence (SampleID set) or a status change of a group.
type Change struct {
	Seq      int64
	GroupID  uuid.UUID
	SampleID uuid.UUID
	New      bool
	// Origin is the journal instance that wrote it.
	Origin string
}

// streamPoller keeps one poller per replica alive while the replica has at least one open stream.
type streamPoller struct {
	mu      sync.Mutex
	streams int
	stop    context.CancelFunc
}

func (p *streamPoller) open(j *Journal) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.streams++
	if p.streams == 1 {
		ctx, cancel := context.WithCancel(context.Background())
		p.stop = cancel
		go j.pollChanges(ctx)
	}
}

func (p *streamPoller) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.streams--
	if p.streams == 0 && p.stop != nil {
		p.stop()
		p.stop = nil
	}
}

// pollChanges reads the change log after a cursor every StreamPollEvery and shows the changes to the streams of
// this replica. The cursor starts at the newest row, so a stream sees what happens after it opened and nothing
// is missed after that: seq order is commit order.
func (j *Journal) pollChanges(ctx context.Context) {
	every := j.cfg.StreamPollEvery
	if every <= 0 {
		every = defaultStreamPoll
	}
	var cursor int64
	for ctx.Err() == nil {
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		latest, err := j.repo.LatestChange(readCtx)
		cancel()
		if err == nil {
			cursor = latest
			break
		}
		log.Debug().Err(err).Msg("Failed to read the error journal change log")
		if !sleepCtx(ctx, every) {
			return
		}
	}
	for sleepCtx(ctx, every) {
		for {
			readCtx, cancel := context.WithTimeout(ctx, readTimeout)
			changes, err := j.repo.ChangesAfter(readCtx, cursor, changesPerRead)
			cancel()
			if err != nil {
				log.Debug().Err(err).Msg("Failed to read the error journal change log")
				break
			}
			if len(changes) == 0 {
				break
			}
			cursor = changes[len(changes)-1].Seq
			j.showChanges(ctx, changes)
			if len(changes) < changesPerRead {
				break
			}
		}
	}
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// foldedChange is what several changes of one group come to in one read.
type foldedChange struct {
	sample uuid.UUID
	isNew  bool
}

// foldChanges folds the changes of one read per group, in order of first change, leaving out what this instance
// wrote itself (it showed those when it recorded them).
func (j *Journal) foldChanges(changes []Change) ([]uuid.UUID, map[uuid.UUID]*foldedChange) {
	order := make([]uuid.UUID, 0, len(changes))
	byGroup := map[uuid.UUID]*foldedChange{}
	for _, c := range changes {
		if c.Origin != "" && c.Origin == j.instance {
			continue
		}
		f := byGroup[c.GroupID]
		if f == nil {
			f = &foldedChange{}
			byGroup[c.GroupID] = f
			order = append(order, c.GroupID)
		}
		if c.SampleID != uuid.Nil {
			f.sample = c.SampleID
		}
		f.isNew = f.isNew || c.New
	}
	return order, byGroup
}

// showChanges publishes one event per changed group: the group as it is now and the newest sample written by
// another replica.
func (j *Journal) showChanges(ctx context.Context, changes []Change) {
	order, byGroup := j.foldChanges(changes)
	for _, id := range order {
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		g, err := j.repo.GetGroup(readCtx, id)
		if err != nil {
			cancel()
			continue // the group was purged meanwhile
		}
		f := byGroup[id]
		ev := StreamEvent{Group: g, New: f.isNew}
		if f.sample != uuid.Nil {
			ev.Sample = j.sampleOf(readCtx, id, f.sample)
		}
		cancel()
		j.hub.Publish(ev)
	}
}

func (j *Journal) sampleOf(ctx context.Context, group, sample uuid.UUID) *errorJournal.Sample {
	samples, err := j.repo.ListSamples(ctx, group, j.cfg.SamplesPerGroup)
	if err != nil {
		return nil
	}
	for i := range samples {
		if samples[i].ID == sample {
			return &samples[i]
		}
	}
	return nil
}
