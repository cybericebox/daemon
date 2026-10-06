package event

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"golang.org/x/sync/singleflight"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventResultRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/scoreboardRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

// Every viewer of an event reads the same results: one results page open in
// 200 browsers must not cost 200 times the database work. The event-wide part
// (config, event, revision, recent changes) and the scoreboard projections
// are kept for a moment and loaded once for concurrent readers
// (singleflight). Only the viewer's own identity is read per viewer.
const (
	// resultsSharedTTL bounds how stale the event-wide part may be; it
	// matches the shortest live poll, so each tick costs one read per event.
	resultsSharedTTL = 2 * time.Second
	// resultsBoardTTL bounds changes that do not advance the revision (team
	// names, hidden teams); a new revision is a new key anyway.
	resultsBoardTTL = 2 * time.Second
	// resultsIdleTTL drops the event-wide part of an event nobody watches.
	resultsIdleTTL = 5 * time.Minute
	// resultsLoadTimeout limits a shared load, which outlives the request
	// that started it.
	resultsLoadTimeout = 10 * time.Second
)

// sharedResults is the event-wide part of a results read: the same for every
// viewer. It is immutable once stored.
type sharedResults struct {
	cfg      eventConfigModel.EventConfig
	event    eventModel.Event
	revision eventResultRepo.Revision
	// changes are the recent changes (base, top] in revision order, without
	// gaps; base is the revision the window starts after.
	base     int64
	changes  []eventResultRepo.Change
	loadedAt time.Time
}

// top is the newest revision held in the change window.
func (s *sharedResults) top() int64 {
	if len(s.changes) == 0 {
		return s.base
	}
	return s.changes[len(s.changes)-1].Revision
}

// changesAfter returns the held changes after the revision; ok is false when
// the window does not start early enough to answer.
func (s *sharedResults) changesAfter(after int64) ([]eventResultRepo.Change, bool) {
	if after < s.base {
		return nil, false
	}
	i, _ := slices.BinarySearchFunc(s.changes, after+1, func(c eventResultRepo.Change, rev int64) int {
		return cmp.Compare(c.Revision, rev)
	})
	return s.changes[i:], true
}

func (s *sharedResults) policy(viewer resultsViewer) resultsPolicy {
	return resultsPolicy{cfg: s.cfg, event: s.event, manager: viewer.manager, participant: viewer.participant}
}

type cachedValue struct {
	value    any
	loadedAt time.Time
}

type resultsCache struct {
	mu     sync.Mutex
	shared map[uuid.UUID]*sharedResults
	boards map[string]cachedValue
	group  singleflight.Group
	now    func() time.Time
}

func newResultsCache() *resultsCache {
	return &resultsCache{shared: map[uuid.UUID]*sharedResults{}, boards: map[string]cachedValue{}, now: time.Now}
}

// do runs load once for all concurrent callers of the key. The load is
// detached from the caller that started it, so one closed browser tab does
// not fail the others; resultsLoadTimeout bounds how long they wait.
func (c *resultsCache) do(ctx context.Context, key string, load func(context.Context) (any, error)) (any, error) {
	value, err, _ := c.group.Do(key, func() (any, error) {
		loadCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), resultsLoadTimeout)
		defer cancel()
		return load(loadCtx)
	})
	return value, err
}

func (c *resultsCache) freshShared(eventID uuid.UUID) (*sharedResults, *sharedResults) {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev := c.shared[eventID]
	if prev != nil && c.now().Sub(prev.loadedAt) < resultsSharedTTL {
		return prev, prev
	}
	return nil, prev
}

func (c *resultsCache) storeShared(eventID uuid.UUID, s *sharedResults) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for id, held := range c.shared {
		if now.Sub(held.loadedAt) > resultsIdleTTL {
			delete(c.shared, id)
		}
	}
	c.shared[eventID] = s
}

// sharedResults returns the event-wide part of the results, at most
// resultsSharedTTL old, read once for all concurrent viewers of the event.
func (u *EventUseCase) sharedResults(ctx context.Context, eventID uuid.UUID) (*sharedResults, error) {
	c := u.resultsCache
	if fresh, _ := c.freshShared(eventID); fresh != nil {
		return fresh, nil
	}
	value, err := c.do(ctx, "shared:"+eventID.String(), func(ctx context.Context) (any, error) {
		// A flight that just finished may already have refreshed it.
		fresh, prev := c.freshShared(eventID)
		if fresh != nil {
			return fresh, nil
		}
		next, loadErr := u.loadSharedResults(ctx, eventID, prev)
		if loadErr != nil {
			return nil, loadErr
		}
		c.storeShared(eventID, next)
		return next, nil
	})
	if err != nil {
		return nil, err
	}
	return value.(*sharedResults), nil
}

// loadSharedResults reads the event-wide part and extends the previous change
// window by the changes committed since it; a gap restarts the window.
func (u *EventUseCase) loadSharedResults(ctx context.Context, eventID uuid.UUID, prev *sharedResults) (*sharedResults, error) {
	cfg, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event config").Err()
	}
	event, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, eventModel.ErrEventNotFound.Err()
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	// The revision is read before any projection that is served with it (see
	// GetResultsSnapshot).
	revision, err := u.results.CurrentRevision(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get event result revision").Err()
	}
	next := &sharedResults{cfg: cfg, event: event, revision: revision, base: revision.Revision, loadedAt: u.resultsCache.now()}
	if prev == nil || prev.top() > revision.Revision {
		return next, nil
	}
	if prev.top() == revision.Revision {
		next.base, next.changes = prev.base, prev.changes
		return next, nil
	}
	changes, err := u.results.ListChangesAfter(ctx, eventID, prev.top(), liveResultsReplayLimit)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list result changes").Err()
	}
	if len(changes) == 0 || changes[0].Revision != prev.top()+1 {
		return next, nil
	}
	window := append(slices.Clip(prev.changes), changes...)
	if over := len(window) - int(liveResultsReplayLimit); over > 0 {
		window = window[over:]
	}
	next.base, next.changes = window[0].Revision-1, window
	// Changes committed after the revision was read are in the window too.
	if top := next.top(); top > next.revision.Revision {
		next.revision.Revision = top
	}
	return next, nil
}

// cachedBoard reads one scoreboard projection through the cache. The key
// holds the revision the reader already acknowledged, so a cached answer is
// never older than that revision.
func cachedBoard[V any](ctx context.Context, c *resultsCache, key string, load func(context.Context) (V, error)) (V, error) {
	if held, found := c.freshBoard(key); found {
		return held.(V), nil
	}
	value, err := c.do(ctx, key, func(ctx context.Context) (any, error) {
		// A flight that just finished may already have loaded it.
		if held, found := c.freshBoard(key); found {
			return held, nil
		}
		loaded, loadErr := load(ctx)
		if loadErr != nil {
			return nil, loadErr
		}
		c.mu.Lock()
		defer c.mu.Unlock()
		now := c.now()
		for k, v := range c.boards {
			if now.Sub(v.loadedAt) >= resultsBoardTTL {
				delete(c.boards, k)
			}
		}
		c.boards[key] = cachedValue{value: loaded, loadedAt: now}
		return loaded, nil
	})
	if err != nil {
		var zero V
		return zero, err
	}
	return value.(V), nil
}

func (c *resultsCache) freshBoard(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	held, found := c.boards[key]
	if !found || c.now().Sub(held.loadedAt) >= resultsBoardTTL {
		return nil, false
	}
	return held.value, true
}

// scoreReader is the scoreboard projection a results read ranks from.
type scoreReader interface {
	List(context.Context, uuid.UUID, scoreboardRepo.Cut) ([]scoreboardRepo.Entry, error)
	EventTimeline(context.Context, uuid.UUID, scoreboardRepo.Cut) ([]scoreboardRepo.EventTimelineEntry, error)
}

// revisionScores reads the projections as of at least one revision through
// the shared cache.
type revisionScores struct {
	u        *EventUseCase
	revision int64
}

func (r revisionScores) key(kind string, eventID uuid.UUID, cut scoreboardRepo.Cut) string {
	at, team := "", ""
	if cut.At != nil {
		at = cut.At.UTC().Format(time.RFC3339Nano)
	}
	if cut.IncludeTeam != nil {
		team = cut.IncludeTeam.String()
	}
	return fmt.Sprintf("%s:%s:%d:%s:%s", kind, eventID, r.revision, at, team)
}

func (r revisionScores) List(ctx context.Context, eventID uuid.UUID, cut scoreboardRepo.Cut) ([]scoreboardRepo.Entry, error) {
	return cachedBoard(ctx, r.u.resultsCache, r.key("list", eventID, cut), func(ctx context.Context) ([]scoreboardRepo.Entry, error) {
		return r.u.scoreboard.List(ctx, eventID, cut)
	})
}

func (r revisionScores) EventTimeline(ctx context.Context, eventID uuid.UUID, cut scoreboardRepo.Cut) ([]scoreboardRepo.EventTimelineEntry, error) {
	return cachedBoard(ctx, r.u.resultsCache, r.key("timeline", eventID, cut), func(ctx context.Context) ([]scoreboardRepo.EventTimelineEntry, error) {
		return r.u.scoreboard.EventTimeline(ctx, eventID, cut)
	})
}
