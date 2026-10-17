package middleware

import (
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

// inflightReportEvery is the least time between two refusal summaries in the log.
const inflightReportEvery = 10 * time.Second

// TransferRoutes are the route templates of uploads (a per-route LimitBody), downloads, exports and media
// streams: slow by nature, they take transfer slots of the in-flight limiter, never general ones. A test
// checks that each one is mounted.
var TransferRoutes = map[string]bool{
	// uploads
	"/api/auth/account/avatar":                                               true,
	"/api/events/self/answer-files":                                          true,
	"/api/events/:id/manage/answer-files":                                    true,
	"/api/events/:id/manage/logo":                                            true,
	"/api/events/:id/manage/preview-picture":                                 true,
	"/api/events/:id/manage/content-images":                                  true,
	"/api/events/:id/manage/content/live/logos":                              true,
	"/api/events/:id/manage/brand-drafts/:kind":                              true,
	"/api/events/:id/manage/notification-templates/email/:templateID/images": true,
	"/api/notifications/templates/email/images":                              true,
	"/api/exercises/files":                                                   true,
	"/api/exercises/import":                                                  true,
	"/api/exercises/uploads/:uploadID/chunks/:index":                         true,
	// file downloads
	"/api/exercises/files/:fileID":                                                                   true,
	"/api/exercises/files/:fileID/chunks/:index":                                                     true,
	"/api/events/self/answer-files/:fileID":                                                          true,
	"/api/events/:id/manage/answer-files/:fileID":                                                    true,
	"/api/events/:id/teams/challenges/:challengeID/files/:fileID":                                    true,
	"/api/events/:id/manage/participant-preview/selections/:selectionID/tasks/:taskID/files/:fileID": true,
	"/api/events/:id/manage/labs/moderators/challenges/:challengeID/files/:fileID":                   true,
	// exports
	"/api/analytics/events/export.csv":                              true,
	"/api/analytics/infrastructure/export.csv":                      true,
	"/api/analytics/mail/export.csv":                                true,
	"/api/analytics/overview/export.csv":                            true,
	"/api/analytics/tasks/export.csv":                               true,
	"/api/analytics/users/export.csv":                               true,
	"/api/events/:id/manage/analytics/communications/export.csv":    true,
	"/api/events/:id/manage/analytics/integrity/export.csv":         true,
	"/api/events/:id/manage/analytics/overview/export.csv":          true,
	"/api/events/:id/manage/analytics/participants/export.csv":      true,
	"/api/events/:id/manage/analytics/progress/inactive/export.csv": true,
	"/api/events/:id/manage/analytics/progress/matrix/export.csv":   true,
	"/api/events/:id/manage/analytics/stands/export.csv":            true,
	"/api/events/:id/manage/analytics/tasks/export.csv":             true,
	"/api/events/:id/manage/analytics/usage/export.csv":             true,
	"/api/events/:id/manage/analytics/report/export.zip":            true,
	"/api/events/:id/manage/results/export.csv":                     true,
	"/api/events/:id/manage/solution-attempts/export.csv":           true,
	// media streamed from the object store
	"/api/auth/avatar/:id":                                               true,
	"/api/events/:id/logo/:fileID":                                       true,
	"/api/events/:id/favicon/:fileID":                                    true,
	"/api/events/:id/preview-picture/:fileID":                            true,
	"/api/events/:id/content-images/:fileID":                             true,
	"/api/notifications/templates/email/images/:fileID":                  true,
	"/api/events/:id/manage/notification-templates/email/images/:fileID": true,
}

type inflightClass int

const (
	inflightGeneral inflightClass = iota
	inflightStreams
	inflightTransfers
	inflightClasses
)

// InflightLimiter bounds the requests a replica serves at once, in three classes with their own slots:
// live streams, transfers and everything else (unmatched paths included). /api/health is never limited.
// A request over its cap waits up to the configured wait for a slot (streams never wait) and is then
// refused with the overload 503. In memory, per replica.
type InflightLimiter struct {
	slots [inflightClasses]chan struct{} // nil: that cap is off
	wait  time.Duration
	now   func() time.Time

	inflight   atomic.Int64
	peak       atomic.Int64
	rejected   [inflightClasses]atomic.Int64
	lastReport atomic.Int64 // unix nanoseconds of the last summary
}

func NewInflightLimiter(cfg config.InflightConfig) *InflightLimiter {
	l := &InflightLimiter{wait: cfg.Wait, now: time.Now}
	for class, size := range [inflightClasses]int{inflightGeneral: cfg.Max, inflightStreams: cfg.StreamsMax, inflightTransfers: cfg.TransfersMax} {
		if size > 0 {
			l.slots[class] = make(chan struct{}, size)
		}
	}
	l.lastReport.Store(l.now().UnixNano())
	return l
}

// Handle is the global middleware. It runs after CORS, so the 503 stays readable by the page, and before
// anything that touches the database.
func (l *InflightLimiter) Handle(ctx *gin.Context) {
	l.report()
	path := ctx.FullPath()
	if path == "/api/health" {
		ctx.Next()
		return
	}
	class, wait := inflightGeneral, l.wait
	switch {
	case exemptFromRateLimit[path]:
		class, wait = inflightStreams, 0
	case TransferRoutes[path]:
		class = inflightTransfers
	}
	slots := l.slots[class]
	if slots == nil {
		ctx.Next()
		return
	}
	if !acquire(ctx, slots, wait) {
		l.rejected[class].Add(1)
		response.AbortWithServiceOverloaded(ctx)
		return
	}
	// Deferred, so a panicking handler gives its slot back too.
	defer func() {
		l.inflight.Add(-1)
		<-slots
	}()
	l.raisePeak(l.inflight.Add(1))
	ctx.Next()
}

func (l *InflightLimiter) raisePeak(n int64) {
	for {
		peak := l.peak.Load()
		if n <= peak || l.peak.CompareAndSwap(peak, n) {
			return
		}
	}
}

// acquire takes a slot, waiting up to wait for one; a client that goes away while waiting gets none.
func acquire(ctx *gin.Context, slots chan struct{}, wait time.Duration) bool {
	select {
	case slots <- struct{}{}:
		return true
	default:
	}
	if wait <= 0 {
		return false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case slots <- struct{}{}:
		return true
	case <-timer.C:
		return false
	case <-ctx.Request.Context().Done():
		return false
	}
}

// report writes one summary of the refusals since the last one, at most every inflightReportEvery and only
// when there were refusals. It runs on a request: no goroutine of its own.
func (l *InflightLimiter) report() {
	now := l.now().UnixNano()
	last := l.lastReport.Load()
	if now-last < int64(inflightReportEvery) || !l.anyRejected() ||
		!l.lastReport.CompareAndSwap(last, now) {
		return
	}
	var counts [inflightClasses]int64
	for class := range counts {
		counts[class] = l.rejected[class].Swap(0)
	}
	log.Warn().
		Int64("rejected", counts[inflightGeneral]+counts[inflightStreams]+counts[inflightTransfers]).
		Int64("rejected_general", counts[inflightGeneral]).
		Int64("rejected_streams", counts[inflightStreams]).
		Int64("rejected_transfers", counts[inflightTransfers]).
		Int64("peak_inflight", l.peak.Swap(l.inflight.Load())).
		Msg("In-flight limit refused requests")
}

func (l *InflightLimiter) anyRejected() bool {
	for class := range l.rejected {
		if l.rejected[class].Load() > 0 {
			return true
		}
	}
	return false
}
