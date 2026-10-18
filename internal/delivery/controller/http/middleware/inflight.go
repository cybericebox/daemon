package middleware

import (
	"bytes"
	"context"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/controller/http/response"
)

const (
	// inflightReportEvery is the least time between two refusal summaries in the log.
	inflightReportEvery = 10 * time.Second
	// inflightBufferMax is the most of its body a general request reads before it takes a slot.
	inflightBufferMax = 1 << 20
	percent           = 100
)

// TransferRoutes are the route templates of uploads (a per-route LimitBody), downloads, exports and media
// streams: slow by nature, they take transfer slots of the in-flight limiter, never general ones (a test
// checks each one is mounted).
func TransferRoutes() []string {
	routes := []string{
		// uploads
		"/api/auth/account/avatar",
		"/api/events/self/answer-files",
		"/api/events/:id/manage/answer-files",
		"/api/events/:id/manage/logo",
		"/api/events/:id/manage/preview-picture",
		"/api/events/:id/manage/content-images",
		"/api/events/:id/manage/content/live/logos",
		"/api/events/:id/manage/brand-drafts/:kind",
		"/api/events/:id/manage/notification-templates/email/:templateID/images",
		"/api/notifications/templates/email/images",
		"/api/exercises/files",
		"/api/exercises/import",
		"/api/exercises/uploads/:uploadID/chunks/:index",
		// file downloads
		"/api/exercises/files/:fileID",
		"/api/exercises/files/:fileID/chunks/:index",
		"/api/events/self/answer-files/:fileID",
		"/api/events/:id/manage/answer-files/:fileID",
		"/api/events/:id/teams/challenges/:challengeID/files/:fileID",
		"/api/events/:id/manage/participant-preview/selections/:selectionID/tasks/:taskID/files/:fileID",
		"/api/events/:id/manage/labs/moderators/challenges/:challengeID/files/:fileID",
		// exports
		"/api/analytics/events/export.csv",
		"/api/analytics/infrastructure/export.csv",
		"/api/analytics/mail/export.csv",
		"/api/analytics/overview/export.csv",
		"/api/analytics/tasks/export.csv",
		"/api/analytics/users/export.csv",
		"/api/events/:id/manage/analytics/communications/export.csv",
		"/api/events/:id/manage/analytics/integrity/export.csv",
		"/api/events/:id/manage/analytics/overview/export.csv",
		"/api/events/:id/manage/analytics/participants/export.csv",
		"/api/events/:id/manage/analytics/progress/inactive/export.csv",
		"/api/events/:id/manage/analytics/progress/matrix/export.csv",
		"/api/events/:id/manage/analytics/stands/export.csv",
		"/api/events/:id/manage/analytics/tasks/export.csv",
		"/api/events/:id/manage/analytics/usage/export.csv",
		"/api/events/:id/manage/analytics/report/export.zip",
		"/api/events/:id/manage/results/export.csv",
		"/api/events/:id/manage/solution-attempts/export.csv",
		// media streamed from the object store (and the public media of PublicMediaRoutes)
		"/api/notifications/templates/email/images/:fileID",
		"/api/events/:id/manage/notification-templates/email/images/:fileID",
	}
	for route := range PublicMediaRoutes {
		routes = append(routes, route)
	}
	return routes
}

type internalListenerKey struct{}

// WithInternalListener marks the context of a connection to the internal listener (in-cluster SSR): one
// trusted caller in front of many visitors, held only to the general cap.
func WithInternalListener(ctx context.Context) context.Context {
	return context.WithValue(ctx, internalListenerKey{}, true)
}

// OnInternalListener reports whether ctx belongs to a connection to the internal listener.
func OnInternalListener(ctx context.Context) bool {
	internal, _ := ctx.Value(internalListenerKey{}).(bool)
	return internal
}

type inflightClass int

const (
	inflightGeneral inflightClass = iota
	inflightStreams
	inflightTransfers
	inflightClasses
)

// sessionSlots are the general slots one session may hold at once; users counts the requests that use the
// entry, so it is dropped with the last one.
type sessionSlots struct {
	slots chan struct{}
	users int
}

// InflightLimiter bounds the requests a replica serves at once, in three classes with their own slots:
// live streams, transfers and everything else (unmatched paths included). /api/health is never limited.
// Inside the general class, anonymous requests share a part of the slots and one session may hold only a
// few at once, so neither unauthenticated clients nor one account can take them all; the internal listener
// is held to the general cap only. A request over a cap waits up to the configured wait (streams never wait)
// and is then refused with the overload 503. In memory, per replica.
type InflightLimiter struct {
	slots      [inflightClasses]chan struct{} // nil: that cap is off
	anon       chan struct{}                  // nil: no anonymous share
	perSession int                            // 0: no per-session cap
	sessionKey func(*gin.Context) (string, bool)
	wait       time.Duration
	now        func() time.Time

	// transfers holds TransferRoutes.
	transfers map[string]bool

	mu       sync.Mutex
	sessions map[string]*sessionSlots

	inflight        [inflightClasses]atomic.Int64
	peak            [inflightClasses]atomic.Int64
	rejected        [inflightClasses]atomic.Int64
	rejectedAnon    atomic.Int64
	rejectedSession atomic.Int64
	lastReport      atomic.Int64 // unix nanoseconds of the last summary
}

// NewInflightLimiter builds the limiter. sessionKey names the caller's session when its cookie opens (no
// database); a request it does not name is anonymous (nil: every request is).
func NewInflightLimiter(
	cfg config.InflightConfig,
	sessionKey func(*gin.Context) (string, bool),
) *InflightLimiter {
	l := &InflightLimiter{
		wait:       cfg.Wait,
		now:        time.Now,
		perSession: cfg.PerSession,
		sessionKey: sessionKey,
		sessions:   map[string]*sessionSlots{},
		transfers:  map[string]bool{},
	}
	for _, route := range TransferRoutes() {
		l.transfers[route] = true
	}
	sizes := [inflightClasses]int{
		inflightGeneral:   cfg.Max,
		inflightStreams:   cfg.StreamsMax,
		inflightTransfers: cfg.TransfersMax,
	}
	for class, size := range sizes {
		if size > 0 {
			l.slots[class] = make(chan struct{}, size)
		}
	}
	if cfg.Max > 0 && cfg.AnonShare > 0 && cfg.AnonShare < percent {
		l.anon = make(chan struct{}, max(1, cfg.Max*cfg.AnonShare/percent))
	}
	l.lastReport.Store(l.now().UnixNano())
	return l
}

// Handle is the global middleware. It runs after CORS (the 503 stays readable by the page) and after the
// origin guard and the anonymous rate limit (a refused request never takes a slot), before the handlers.
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
	case l.transfers[path]:
		class = inflightTransfers
	}
	slots := l.slots[class]
	if slots == nil {
		ctx.Next()
		return
	}
	if class == inflightGeneral {
		bufferBody(ctx)
	}
	w := &waiter{done: ctx.Request.Context().Done(), wait: wait}
	defer w.stop()
	if class == inflightGeneral && !OnInternalListener(ctx.Request.Context()) {
		sub, counter, leave := l.subCap(ctx)
		if leave != nil {
			defer leave()
		}
		if sub != nil {
			if !l.admit(ctx, w, sub, class, counter) {
				return
			}
			defer func() { <-sub }()
		}
	}
	if !l.admit(ctx, w, slots, class, nil) {
		return
	}
	// Deferred, so a panicking handler gives its slots back too.
	defer func() {
		l.inflight[class].Add(-1)
		<-slots
	}()
	l.raisePeak(class, l.inflight[class].Add(1))
	ctx.Next()
}

// subCap returns the slots inside the general cap this caller is held to (its session's own, or the anonymous
// share; nil: none), the counter of its refusals and, for a session, the function that drops its entry.
func (l *InflightLimiter) subCap(ctx *gin.Context) (chan struct{}, *atomic.Int64, func()) {
	if l.sessionKey != nil {
		if key, ok := l.sessionKey(ctx); ok {
			if l.perSession == 0 {
				return nil, nil, nil
			}
			entry := l.enterSession(key)
			return entry.slots, &l.rejectedSession, func() { l.leaveSession(key, entry) }
		}
	}
	return l.anon, &l.rejectedAnon, nil
}

func (l *InflightLimiter) enterSession(key string) *sessionSlots {
	l.mu.Lock()
	defer l.mu.Unlock()
	entry := l.sessions[key]
	if entry == nil {
		entry = &sessionSlots{slots: make(chan struct{}, l.perSession)}
		l.sessions[key] = entry
	}
	entry.users++
	return entry
}

func (l *InflightLimiter) leaveSession(key string, entry *sessionSlots) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if entry.users--; entry.users == 0 {
		delete(l.sessions, key)
	}
}

// admit takes a slot or ends the request: the overload 503 when none came free in time, a bare abort (nothing
// written, nothing counted) when the client went away while waiting.
func (l *InflightLimiter) admit(
	ctx *gin.Context, w *waiter, slots chan struct{}, class inflightClass, counter *atomic.Int64,
) bool {
	switch w.take(slots) {
	case admitted:
		return true
	case clientGone:
		ctx.Abort()
		return false
	case refused:
	}
	l.rejected[class].Add(1)
	if counter != nil {
		counter.Add(1)
	}
	response.AbortWithServiceOverloaded(ctx)
	return false
}

func (l *InflightLimiter) raisePeak(class inflightClass, n int64) {
	for {
		peak := l.peak[class].Load()
		if n <= peak || l.peak[class].CompareAndSwap(peak, n) {
			return
		}
	}
}

type admission int

const (
	admitted admission = iota
	refused
	clientGone
)

// waiter is one request's wait budget, shared by every slot it takes.
type waiter struct {
	done    <-chan struct{}
	wait    time.Duration
	timer   *time.Timer
	expired bool
}

func (w *waiter) take(slots chan struct{}) admission {
	select {
	case slots <- struct{}{}:
		return admitted
	default:
	}
	if w.wait <= 0 || w.expired {
		return refused
	}
	if w.timer == nil {
		w.timer = time.NewTimer(w.wait)
	}
	select {
	case slots <- struct{}{}:
		return admitted
	case <-w.timer.C:
		w.expired = true
		return refused
	case <-w.done:
		return clientGone
	}
}

func (w *waiter) stop() {
	if w.timer != nil {
		w.timer.Stop()
	}
}

// bufferBody reads a general request's body (up to inflightBufferMax, still under BodyLimit) before the
// request takes a slot, so a client slow to send its body holds none meanwhile. A body declared larger is not
// read here: it takes its slot first and streams. A read error reaches the handler after the buffered part,
// as it would have without the buffer.
func bufferBody(ctx *gin.Context) {
	req := ctx.Request
	if !hasBody(req) || req.ContentLength > inflightBufferMax {
		return
	}
	body := req.Body
	buf, err := io.ReadAll(io.LimitReader(body, inflightBufferMax))
	req.Body = bufferedBody{
		Reader: io.MultiReader(bytes.NewReader(buf), restOf(body, err)),
		Closer: body,
	}
	// LimitBody re-caps the original body: it has to find the buffered part in front of it too.
	if original, found := ctx.Get(originalBodyKey); found {
		if rc, ok := original.(io.ReadCloser); ok {
			ctx.Set(originalBodyKey, bufferedBody{
				Reader: io.MultiReader(bytes.NewReader(buf), restOf(rc, err)),
				Closer: rc,
			})
		}
	}
}

func restOf(body io.Reader, err error) io.Reader {
	if err != nil {
		return failedReader{err}
	}
	return body
}

type bufferedBody struct {
	io.Reader
	io.Closer
}

type failedReader struct{ err error }

func (r failedReader) Read([]byte) (int, error) { return 0, r.err }

// report writes one summary of the refusals since the last one, at most every inflightReportEvery and only
// when there were refusals. It runs on a request: no goroutine of its own.
func (l *InflightLimiter) report() {
	now := l.now().UnixNano()
	last := l.lastReport.Load()
	if now-last < int64(inflightReportEvery) || !l.anyRejected() ||
		!l.lastReport.CompareAndSwap(last, now) {
		return
	}
	var counts, peaks [inflightClasses]int64
	for class := range counts {
		counts[class] = l.rejected[class].Swap(0)
		peaks[class] = l.peak[class].Swap(l.inflight[class].Load())
	}
	log.Warn().
		Int64("rejected", counts[inflightGeneral]+counts[inflightStreams]+counts[inflightTransfers]).
		Int64("rejected_general", counts[inflightGeneral]).
		Int64("rejected_anonymous", l.rejectedAnon.Swap(0)).
		Int64("rejected_per_session", l.rejectedSession.Swap(0)).
		Int64("rejected_streams", counts[inflightStreams]).
		Int64("rejected_transfers", counts[inflightTransfers]).
		Int64("peak_general", peaks[inflightGeneral]).
		Int64("peak_streams", peaks[inflightStreams]).
		Int64("peak_transfers", peaks[inflightTransfers]).
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
