// Package errorJournalUseCase records what broke on the platform, groups it by fingerprint, tells the operators
// and serves the admin page. Capture points call Report and never wait: events go through a bounded queue to one
// writer, so a database outage or an error storm cannot slow a request or a job down.
package errorJournalUseCase

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/pkg/email"
)

// Config holds the tunables of the journal (ERROR_JOURNAL_* env keys).
type Config struct {
	// SamplesPerGroup is how many recent samples a group keeps.
	SamplesPerGroup int
	// Retention is how long groups, samples and 404 counters are kept.
	Retention time.Duration
	// NotifyCooldown is the least time between two messages about one fingerprint; a storm becomes one message
	// with a count.
	NotifyCooldown time.Duration
	// SpikeThreshold occurrences within SpikeWindow of a known fingerprint are a spike.
	SpikeThreshold int
	SpikeWindow    time.Duration
	// BufferSize is the capacity of the capture queue; events beyond it are dropped and counted.
	BufferSize int
	// NotFoundFlushEvery is how often the in-memory 404 counters are written.
	NotFoundFlushEvery time.Duration
	// QueueStallAfter: a job that waits longer than this while its queue should be working means a stalled worker.
	QueueStallAfter time.Duration
	// QueueBacklogLimit: more waiting jobs than this is a growing queue.
	QueueBacklogLimit int
	// CertExpiryWarn: an agent certificate that ends within this is reported.
	CertExpiryWarn time.Duration
	// AgentOfflineAfter: an agent that has been unreachable this long is reported offline.
	AgentOfflineAfter time.Duration
	// WatchEvery is how often the periodic checks (queue, certificates) run.
	WatchEvery time.Duration
	// AdminURL is the base of the links in messages (https://admin.<domain>); empty leaves links out.
	AdminURL string
	// Environment labels messages (development, stage, production).
	Environment string
}

// DefaultConfig is what the env defaults give; tests start from it.
func DefaultConfig() Config {
	return Config{
		SamplesPerGroup: 5, Retention: 30 * 24 * time.Hour,
		NotifyCooldown: 15 * time.Minute, SpikeThreshold: 20, SpikeWindow: 5 * time.Minute,
		BufferSize: 1024, NotFoundFlushEvery: 10 * time.Second,
		QueueStallAfter: 5 * time.Minute, QueueBacklogLimit: 1000,
		CertExpiryWarn: 14 * 24 * time.Hour, AgentOfflineAfter: 2 * time.Minute, WatchEvery: time.Minute,
	}
}

// Repository is the storage port.
type Repository interface {
	// Record upserts the group of the event and adds its sample, keeping the newest keep samples of the group.
	Record(ctx context.Context, in RecordInput) (RecordResult, error)
	ListGroups(ctx context.Context, f GroupFilter) ([]errorJournal.Group, int64, error)
	GetGroup(ctx context.Context, id uuid.UUID) (errorJournal.Group, error)
	ListSamples(ctx context.Context, groupID uuid.UUID, limit int) ([]errorJournal.Sample, error)
	SetGroupStatus(ctx context.Context, id uuid.UUID, status errorJournal.Status, now time.Time) (errorJournal.Group, error)
	// ResolveByFingerprint marks an open group resolved (a recovered agent); no such group is not an error.
	ResolveByFingerprint(ctx context.Context, fingerprint string, now time.Time) error
	// MarkNotified claims the right to send the message of the group: it succeeds when nobody sent one since
	// cutoff (or ever), resets the suppressed count and returns it. Several replicas race on it safely.
	MarkNotified(ctx context.Context, id uuid.UUID, now, cutoff time.Time) (suppressed int64, claimed bool, err error)
	AddNotFound(ctx context.Context, day time.Time, route string, hits int64) error
	ListNotFound(ctx context.Context, from, to time.Time) ([]errorJournal.NotFoundDay, error)
	Purge(ctx context.Context, cutoff time.Time) (PurgeResult, error)

	GetSettings(ctx context.Context) (errorJournal.Settings, error)
	SaveEmails(ctx context.Context, emails []string, toSuperAdmins bool, now time.Time) error
	ReplaceChats(ctx context.Context, chats []errorJournal.TelegramChat) error
	SetChatFailing(ctx context.Context, chatID string, failing bool, reason string, now time.Time) error
	SuperAdminEmails(ctx context.Context) ([]string, error)
	// QueueStats reads the job queue: jobs ready to run, and how long the oldest has waited.
	QueueStats(ctx context.Context, now time.Time) (QueueStats, error)
}

// Telegram is the bot port.
type Telegram interface {
	Enabled() bool
	Send(ctx context.Context, chatID, text string) error
}

// Mailer delivers one platform e-mail (the platform transport; no event).
type Mailer interface {
	Deliver(ctx context.Context, eventID *uuid.UUID, msg email.Message) error
}

// AgentCertificates lists the laboratory agents and the end of their client certificates.
type AgentCertificates interface {
	AgentCertificates(ctx context.Context) ([]AgentCertificate, error)
}

type (
	RecordInput struct {
		Fingerprint string
		Kind        errorJournal.Kind
		Source      string
		Title       string
		At          time.Time
		Sample      errorJournal.Sample
		Keep        int
		// Count is the occurrences this record adds (at least 1).
		Count int
	}
	RecordResult struct {
		Group    errorJournal.Group
		Inserted bool
		// Reopened: the group was resolved and the error came back.
		Reopened bool
	}
	GroupFilter struct {
		Kinds  []errorJournal.Kind
		Status errorJournal.Status
		From   *time.Time
		To     *time.Time
		Query  string
		// Request is a request id or its first characters (at least 8 hex): only the groups that have a sample
		// of that request.
		Request string
		Limit   int
		Offset  int
	}
	PurgeResult struct {
		Groups, Samples, NotFound int64
	}
	QueueStats struct {
		// Waiting is the number of jobs ready to run; OldestWait is how long the oldest of them has waited.
		Waiting    int64
		OldestWait time.Duration
		Queue      string
	}
	AgentCertificate struct {
		ID       uuid.UUID
		Name     string
		NotAfter *time.Time
	}
)

// Journal is the use case.
type Journal struct {
	repo   Repository
	tg     Telegram
	mail   Mailer
	agents AgentCertificates
	cfg    Config
	now    func() time.Time

	queue   chan errorJournal.Event
	dropped atomic.Int64
	hub     *Hub

	spikes spikeCounter

	mu       sync.Mutex
	notFound map[notFoundKey]int64
	// refusals folds the 403 flood of one fingerprint: the first event of a window is recorded at once, the rest
	// are counted here and written as one record per flush.
	refusals map[string]*refusalFold
	// kindMessaged is when the last message of a noisy kind (403) was sent, for every group of it.
	kindMessaged map[errorJournal.Kind]time.Time
	offline      map[string]*offlineState
	certLast     map[string]time.Time
}

// refusalFold counts the repeats of one 403 fingerprint inside its window.
type refusalFold struct {
	until   time.Time
	pending int
	last    errorJournal.Event
}

const (
	// refusalWindow is how long one fingerprint of 403s is folded after its first event.
	refusalWindow = time.Minute
	// maxRefusalFolds bounds the table: more distinct fingerprints than this are dropped and counted, never
	// written one by one.
	maxRefusalFolds = 5000
)

type notFoundKey struct {
	day   time.Time
	route string
}

// Dependencies of New; Telegram, Mailer and Agents may be nil (the matching channel is then off).
type Dependencies struct {
	Repo     Repository
	Telegram Telegram
	Mailer   Mailer
	Agents   AgentCertificates
	Config   Config
}

func New(deps Dependencies) *Journal {
	cfg := deps.Config
	if cfg.BufferSize < 1 {
		cfg.BufferSize = 1
	}
	if cfg.SamplesPerGroup < 1 {
		cfg.SamplesPerGroup = 1
	}
	return &Journal{
		repo: deps.Repo, tg: deps.Telegram, mail: deps.Mailer, agents: deps.Agents, cfg: cfg, now: time.Now,
		queue: make(chan errorJournal.Event, cfg.BufferSize), hub: NewHub(),
		spikes: spikeCounter{hits: map[string][]time.Time{}}, notFound: map[notFoundKey]int64{},
		offline: map[string]*offlineState{}, certLast: map[string]time.Time{},
		refusals: map[string]*refusalFold{}, kindMessaged: map[errorJournal.Kind]time.Time{},
	}
}

// ErrorJournalStream subscribes to the live stream of recorded errors (the admin page's SSE); call the returned
// function to end it.
func (j *Journal) ErrorJournalStream() (<-chan StreamEvent, func()) { return j.hub.Subscribe() }

// Report hands an event to the journal. It never blocks and never fails the caller: a full queue drops the event.
func (j *Journal) Report(e errorJournal.Event) {
	if j == nil {
		return
	}
	if e.At.IsZero() {
		e.At = j.now()
	}
	// A 403 flood (foreign origins, a user hammering a forbidden route) costs one write per fingerprint per
	// window, not one per request.
	if e.Kind == errorJournal.KindHTTP403 && j.foldRefusal(e) {
		return
	}
	select {
	case j.queue <- e:
	default:
		j.dropped.Add(1)
	}
}

// foldRefusal counts a repeat of a 403 fingerprint seen inside its window and reports true; the first event of
// a window (or of a table that is full: dropped) is left to the caller.
func (j *Journal) foldRefusal(e errorJournal.Event) bool {
	fp := errorJournal.Fingerprint(e)
	j.mu.Lock()
	defer j.mu.Unlock()
	if f, ok := j.refusals[fp]; ok && e.At.Before(f.until) {
		f.pending++
		f.last = e
		return true
	}
	if _, known := j.refusals[fp]; !known && len(j.refusals) >= maxRefusalFolds {
		j.dropped.Add(1)
		return true
	}
	j.refusals[fp] = &refusalFold{until: e.At.Add(refusalWindow)}
	return false
}

// flushRefusals writes the folded repeats, one record per fingerprint with its count, and forgets the windows
// that ended.
func (j *Journal) flushRefusals(ctx context.Context) {
	now := j.now()
	var due []errorJournal.Event
	j.mu.Lock()
	for fp, f := range j.refusals {
		if f.pending > 0 {
			e := f.last
			e.Count = f.pending
			due = append(due, e)
			f.pending = 0
		}
		if !now.Before(f.until) {
			delete(j.refusals, fp)
		}
	}
	j.mu.Unlock()
	for _, e := range due {
		j.handle(ctx, e)
	}
}

// CountNotFound counts a 404: the route template of a handler 404, or an empty route for a path that matched
// nothing (paths are never kept).
func (j *Journal) CountNotFound(route string) {
	if j == nil {
		return
	}
	key := notFoundKey{day: dayOf(j.now()), route: route}
	j.mu.Lock()
	j.notFound[key]++
	j.mu.Unlock()
}

func dayOf(t time.Time) time.Time {
	y, m, d := t.UTC().Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC)
}

// Run is the writer: it records queued events and flushes the 404 counters until ctx ends, then drains what is
// left. Start it once per process.
func (j *Journal) Run(ctx context.Context) {
	flush := time.NewTicker(j.cfg.NotFoundFlushEvery)
	defer flush.Stop()
	for {
		select {
		case <-ctx.Done():
			j.drain()
			return
		case e := <-j.queue:
			j.handle(ctx, e)
		case <-flush.C:
			j.FlushNotFound(ctx)
			j.flushRefusals(ctx)
			if n := j.dropped.Swap(0); n > 0 {
				log.Warn().Int64("dropped", n).Msg("Error journal queue was full: events dropped")
			}
		}
	}
}

func (j *Journal) drain() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		select {
		case e := <-j.queue:
			j.handle(ctx, e)
		default:
			j.FlushNotFound(ctx)
			j.flushRefusals(ctx)
			return
		}
	}
}

// FlushNotFound writes the counted 404s. A failed write puts the counts back for the next flush.
func (j *Journal) FlushNotFound(ctx context.Context) {
	j.mu.Lock()
	pending := j.notFound
	j.notFound = map[notFoundKey]int64{}
	j.mu.Unlock()
	for key, hits := range pending {
		if err := j.repo.AddNotFound(ctx, key.day, key.route, hits); err != nil {
			log.Warn().Err(err).Msg("Failed to store the 404 counters")
			j.mu.Lock()
			j.notFound[key] += hits
			j.mu.Unlock()
		}
	}
}

// handle records one event and decides about a message. Its own failures are logged only: reporting them would
// feed the journal with its own faults.
func (j *Journal) handle(ctx context.Context, e errorJournal.Event) {
	if _, err := j.Record(ctx, e); err != nil {
		log.Warn().Err(err).Str("kind", string(e.Kind)).Msg("Failed to record an error in the journal")
	}
}

// Record stores one event synchronously, publishes it to the live stream and sends the notification the rules
// ask for.
func (j *Journal) Record(ctx context.Context, e errorJournal.Event) (RecordResult, error) {
	if !e.Kind.Valid() {
		e.Kind = errorJournal.KindHTTP5xx
	}
	if e.At.IsZero() {
		e.At = j.now()
	}
	fp := errorJournal.Fingerprint(e)
	sampleID, err := uuid.NewV7()
	if err != nil {
		return RecordResult{}, err
	}
	sample := errorJournal.Sample{
		ID: sampleID, OccurredAt: e.At,
		Message: errorJournal.Clean(e.Message, errorJournal.MaxMessageBytes),
		Stack:   errorJournal.Truncate(errorJournal.Scrub(e.Stack), errorJournal.MaxStackBytes),
		Method:  e.Method, Route: e.Route, RequestID: e.RequestID, UserID: e.UserID, Role: e.Role,
		Permission: e.Permission, Limiter: e.Limiter, Details: errorJournal.CleanDetails(e.Details),
	}
	if e.HTTPStatus != 0 {
		status := e.HTTPStatus
		sample.HTTPStatus = &status
	}
	rec, err := j.repo.Record(ctx, RecordInput{
		Fingerprint: fp, Kind: e.Kind, Source: e.Source, Title: errorJournal.Title(e), At: e.At,
		Sample: sample, Keep: j.cfg.SamplesPerGroup, Count: max(e.Count, 1),
	})
	if err != nil {
		return RecordResult{}, err
	}
	sample.GroupID = rec.Group.ID
	j.hub.Publish(StreamEvent{Group: rec.Group, Sample: &sample, New: rec.Inserted || rec.Reopened})
	if rec.Group.Status != errorJournal.StatusIgnored {
		j.notify(ctx, e, rec, sample)
	}
	return rec, nil
}

// reRequestPrefix is a request id (a UUID) or its beginning: at least 8 hex digits, dashes allowed.
var reRequestPrefix = regexp.MustCompile(`^[0-9A-Fa-f][0-9A-Fa-f-]{7,35}$`)

// ListGroups, GetGroup and the rest of the read side.
func (j *Journal) ListErrorGroups(ctx context.Context, f GroupFilter) ([]errorJournal.Group, int64, error) {
	if f.Limit < 1 || f.Limit > 200 {
		f.Limit = 50
	}
	if f.Offset < 0 || (f.Status != "" && !f.Status.Valid()) {
		return nil, 0, errorJournal.ErrFilterInvalid.Err()
	}
	for _, k := range f.Kinds {
		if !k.Valid() {
			return nil, 0, errorJournal.ErrFilterInvalid.Err()
		}
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return nil, 0, errorJournal.ErrPeriodInvalid.Err()
	}
	if f.Request = strings.TrimSpace(f.Request); f.Request != "" && !reRequestPrefix.MatchString(f.Request) {
		return nil, 0, errorJournal.ErrFilterInvalid.Err()
	}
	return j.repo.ListGroups(ctx, f)
}

// GroupDetail is a group with its recent samples.
type GroupDetail struct {
	Group   errorJournal.Group
	Samples []errorJournal.Sample
}

func (j *Journal) GetErrorGroup(ctx context.Context, id uuid.UUID) (GroupDetail, error) {
	g, err := j.repo.GetGroup(ctx, id)
	if err != nil {
		return GroupDetail{}, err
	}
	samples, err := j.repo.ListSamples(ctx, id, j.cfg.SamplesPerGroup)
	if err != nil {
		return GroupDetail{}, err
	}
	return GroupDetail{Group: g, Samples: samples}, nil
}

// SetGroupStatus resolves, ignores or reopens a group.
func (j *Journal) SetErrorGroupStatus(ctx context.Context, id uuid.UUID, status errorJournal.Status) (errorJournal.Group, error) {
	if !status.Valid() {
		return errorJournal.Group{}, errorJournal.ErrStatusInvalid.Err()
	}
	g, err := j.repo.SetGroupStatus(ctx, id, status, j.now())
	if err != nil {
		return errorJournal.Group{}, err
	}
	j.hub.Publish(StreamEvent{Group: g})
	return g, nil
}

// NotFoundStats returns the daily 404 counters of the period, newest day first.
func (j *Journal) ErrorNotFoundStats(ctx context.Context, from, to time.Time) ([]errorJournal.NotFoundDay, error) {
	if to.Before(from) {
		return nil, errorJournal.ErrPeriodInvalid.Err()
	}
	return j.repo.ListNotFound(ctx, dayOf(from), dayOf(to))
}

// Purge deletes what is older than the retention.
func (j *Journal) Purge(ctx context.Context) (PurgeResult, error) {
	return j.repo.Purge(ctx, j.now().Add(-j.cfg.Retention))
}

// PurgeErrorJournal is the River job's entry: the purge pass.
func (j *Journal) PurgeErrorJournal(ctx context.Context) error {
	res, err := j.Purge(ctx)
	if err != nil {
		return err
	}
	log.Info().Int64("groups", res.Groups).Int64("samples", res.Samples).Int64("notFound", res.NotFound).Msg("Error journal purged")
	return nil
}
