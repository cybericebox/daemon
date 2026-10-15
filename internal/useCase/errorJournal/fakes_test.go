package errorJournalUseCase

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/gofrs/uuid"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/pkg/email"
	"github.com/cybericebox/daemon/pkg/telegram"
)

var errNotFound = errors.New("not found")

type fakeRepo struct {
	mu            sync.Mutex
	groups        map[string]*errorJournal.Group // by fingerprint
	samples       map[uuid.UUID][]errorJournal.Sample
	notFound      map[string]int64
	settings      errorJournal.Settings
	admins        []string
	queue         QueueStats
	purgedAt      time.Time
	deliveries    map[uuid.UUID]Delivery
	enqueued      map[uuid.UUID]time.Time
	afterRecord   func()
	smtpProviders []SMTPProviderState
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{deliveries: map[uuid.UUID]Delivery{}, enqueued: map[uuid.UUID]time.Time{}, groups: map[string]*errorJournal.Group{}, samples: map[uuid.UUID][]errorJournal.Sample{}, notFound: map[string]int64{}}
}

func (r *fakeRepo) Record(_ context.Context, in RecordInput) (RecordResult, error) {
	r.mu.Lock()
	defer func() {
		r.mu.Unlock()
		if r.afterRecord != nil {
			r.afterRecord()
		}
	}()
	g, ok := r.groups[in.Fingerprint]
	res := RecordResult{}
	if !ok {
		g = &errorJournal.Group{ID: uuid.Must(uuid.NewV7()), Fingerprint: in.Fingerprint, Kind: in.Kind, Source: in.Source, Title: in.Title, Status: errorJournal.StatusOpen, FirstSeenAt: in.At}
		r.groups[in.Fingerprint] = g
		res.Inserted = true
	} else if g.Status == errorJournal.StatusResolved {
		g.Status, g.ResolvedAt = errorJournal.StatusOpen, nil
		res.Reopened = true
	}
	g.Occurrences += int64(in.Count)
	g.SuppressedSince += int64(in.Count)
	g.LastSeenAt = in.At
	in.Sample.GroupID = g.ID
	r.samples[g.ID] = append([]errorJournal.Sample{in.Sample}, r.samples[g.ID]...)
	if len(r.samples[g.ID]) > in.Keep {
		r.samples[g.ID] = r.samples[g.ID][:in.Keep]
	}
	res.Group = *g
	if in.Notification != nil {
		if plan := in.Notification(res); plan != nil {
			pending := false
			for _, d := range r.deliveries {
				if d.GroupID == g.ID && d.DeliveredAt == nil {
					pending = true
				}
			}
			last, had := r.enqueued[g.ID]
			if !pending && (!had || !last.After(plan.Cutoff)) {
				id := uuid.Must(uuid.NewV7())
				r.deliveries[id] = Delivery{ID: id, GroupID: g.ID, Channel: "prepare", Body: plan.Body, EmailBody: plan.EmailBody, CreatedAt: plan.Now, NextAttemptAt: plan.Now}
				r.enqueued[g.ID] = plan.Now
			}
		}
	}
	return res, nil
}

func (r *fakeRepo) ListGroups(_ context.Context, f GroupFilter) ([]errorJournal.Group, int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []errorJournal.Group
	for _, g := range r.groups {
		out = append(out, *g)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeenAt.After(out[j].LastSeenAt) })
	return out, int64(len(out)), nil
}

func (r *fakeRepo) byID(id uuid.UUID) *errorJournal.Group {
	for _, g := range r.groups {
		if g.ID == id {
			return g
		}
	}
	return nil
}

func (r *fakeRepo) GetGroup(_ context.Context, id uuid.UUID) (errorJournal.Group, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g := r.byID(id); g != nil {
		return *g, nil
	}
	return errorJournal.Group{}, errNotFound
}

func (r *fakeRepo) ListSamples(_ context.Context, id uuid.UUID, limit int) ([]errorJournal.Sample, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]errorJournal.Sample(nil), r.samples[id]...), nil
}

func (r *fakeRepo) SetGroupStatus(_ context.Context, id uuid.UUID, s errorJournal.Status, now time.Time) (errorJournal.Group, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.byID(id)
	if g == nil {
		return errorJournal.Group{}, errNotFound
	}
	g.Status = s
	return *g, nil
}

func (r *fakeRepo) ResolveByFingerprint(_ context.Context, fp string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if g := r.groups[fp]; g != nil && g.Status == errorJournal.StatusOpen && !g.LastSeenAt.After(now) {
		g.Status = errorJournal.StatusResolved
		g.ResolvedAt = &now
	}
	return nil
}

func (r *fakeRepo) MarkNotified(_ context.Context, id uuid.UUID, now, cutoff time.Time) (int64, bool, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	g := r.byID(id)
	if g == nil {
		return 0, false, errNotFound
	}
	if g.LastNotifiedAt != nil && g.LastNotifiedAt.After(cutoff) {
		return 0, false, nil
	}
	n := g.SuppressedSince
	g.LastNotifiedAt, g.SuppressedSince = &now, 0
	return n, true, nil
}

func (r *fakeRepo) AddNotFound(_ context.Context, day time.Time, route string, hits int64) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.notFound[day.Format("2006-01-02")+"|"+route] += hits
	return nil
}

func (r *fakeRepo) ListNotFound(context.Context, time.Time, time.Time) ([]errorJournal.NotFoundDay, error) {
	return nil, nil
}

func (r *fakeRepo) Purge(_ context.Context, cutoff time.Time) (PurgeResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.purgedAt = cutoff
	return PurgeResult{}, nil
}

func (r *fakeRepo) GetSettings(context.Context) (errorJournal.Settings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s := r.settings
	s.TelegramChats = append([]errorJournal.TelegramChat(nil), r.settings.TelegramChats...)
	return s, nil
}

func (r *fakeRepo) SaveEmails(_ context.Context, emails []string, toAdmins bool, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings.Emails, r.settings.EmailToSuperAdmins = emails, toAdmins
	return nil
}

func (r *fakeRepo) ReplaceChats(_ context.Context, chats []errorJournal.TelegramChat) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings.TelegramChats = chats
	return nil
}

func (r *fakeRepo) SetChatFailing(_ context.Context, id string, failing bool, reason string, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for i := range r.settings.TelegramChats {
		if r.settings.TelegramChats[i].ChatID == id {
			r.settings.TelegramChats[i].Failing, r.settings.TelegramChats[i].LastError = failing, reason
		}
	}
	return nil
}

func (r *fakeRepo) SuperAdminEmails(context.Context) ([]string, error) { return r.admins, nil }

func (r *fakeRepo) QueueStats(context.Context, time.Time) (QueueStats, error) { return r.queue, nil }

type fakeTelegram struct {
	mu   sync.Mutex
	sent map[string][]string
	errs map[string]error
}

func newFakeTelegram() *fakeTelegram {
	return &fakeTelegram{sent: map[string][]string{}, errs: map[string]error{}}
}

func (t *fakeTelegram) Enabled() bool { return true }
func (t *fakeTelegram) Send(_ context.Context, chat, text string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err := t.errs[chat]; err != nil {
		return err
	}
	t.sent[chat] = append(t.sent[chat], text)
	return nil
}
func (t *fakeTelegram) count(chat string) int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.sent[chat])
}
func (t *fakeTelegram) last(chat string) string {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.sent[chat]) == 0 {
		return ""
	}
	return t.sent[chat][len(t.sent[chat])-1]
}

var _ = telegram.ErrForbidden

type fakeMailer struct {
	mu   sync.Mutex
	sent []email.Message
}

func (m *fakeMailer) Deliver(_ context.Context, _ *uuid.UUID, msg email.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sent = append(m.sent, msg)
	return nil
}

type fakeAgents struct{ certs []AgentCertificate }

func (a fakeAgents) AgentCertificates(context.Context) ([]AgentCertificate, error) {
	return a.certs, nil
}

// harness builds a journal over fakes with a controllable clock.
type harness struct {
	j    *Journal
	repo *fakeRepo
	tg   *fakeTelegram
	mail *fakeMailer
	now  time.Time
}

func newHarness(cfg Config) *harness {
	h := &harness{repo: newFakeRepo(), tg: newFakeTelegram(), mail: &fakeMailer{}, now: time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)}
	h.repo.settings = errorJournal.Settings{TelegramChats: []errorJournal.TelegramChat{{ChatID: "100", Label: "ops"}}, Emails: []string{"ops@example.org"}}
	h.j = New(Dependencies{Repo: h.repo, Telegram: h.tg, Mailer: h.mail, Config: cfg})
	h.j.now = func() time.Time { return h.now }
	h.repo.afterRecord = h.deliverAll
	return h
}

func (r *fakeRepo) GetDelivery(_ context.Context, id uuid.UUID) (Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	d, ok := r.deliveries[id]
	if !ok {
		return Delivery{}, ErrDeliveryGone
	}
	return d, nil
}
func (r *fakeRepo) StartDelivery(_ context.Context, id uuid.UUID, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	d := r.deliveries[id]
	d.Attempts++
	d.LastAttemptAt = &now
	r.deliveries[id] = d
	return nil
}
func (r *fakeRepo) FinishDelivery(_ context.Context, d Delivery, result TestResult, now, next time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.deliveries[d.ID]
	current.CancelledAt = d.CancelledAt
	current.LastError = result.Error
	current.NextAttemptAt = next
	if result.OK {
		current.DeliveredAt = &now
		g := r.byID(d.GroupID)
		g.LastNotifiedAt = &now
		g.SuppressedSince = 0
	}
	r.deliveries[d.ID] = current
	return nil
}
func (r *fakeRepo) ExpandDelivery(_ context.Context, d Delivery, targets []DeliveryTarget, now time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	current := r.deliveries[d.ID]
	if current.DeliveredAt != nil {
		return nil
	}
	for _, target := range targets {
		id := uuid.Must(uuid.NewV7())
		body := d.Body
		if target.Channel == ChannelEmail {
			body = d.EmailBody
		}
		r.deliveries[id] = Delivery{ID: id, GroupID: d.GroupID, Channel: target.Channel, Target: target.Target, Label: target.Label, Body: body, CreatedAt: now, NextAttemptAt: now}
	}
	current.DeliveredAt = &now
	r.deliveries[d.ID] = current
	return nil
}
func (r *fakeRepo) ListDeliveries(_ context.Context, id uuid.UUID) ([]Delivery, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := []Delivery{}
	for _, d := range r.deliveries {
		if d.GroupID == id && (d.Channel != "prepare" || d.DeliveredAt == nil) {
			out = append(out, d)
		}
	}
	return out, nil
}

// Existing rule tests explicitly simulate River completing the jobs after capture. Network calls are
// never made by production Record; recovery regressions disable this fixture convenience.
func (h *harness) deliverAll() {
	for range 20 {
		var ids []uuid.UUID
		h.repo.mu.Lock()
		for _, d := range h.repo.deliveries {
			if d.DeliveredAt == nil && d.CancelledAt == nil && !d.NextAttemptAt.After(h.now) {
				ids = append(ids, d.ID)
			}
		}
		h.repo.mu.Unlock()
		if len(ids) == 0 {
			return
		}
		for _, id := range ids {
			_ = h.j.ProcessJournalDelivery(context.Background(), id)
		}
	}
}
