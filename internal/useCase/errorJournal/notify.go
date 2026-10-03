package errorJournalUseCase

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/rs/zerolog/log"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/pkg/email"
	"github.com/cybericebox/daemon/pkg/telegram"
)

// spikeCounter counts occurrences of a fingerprint in a sliding window, in memory (per replica).
type spikeCounter struct {
	mu   sync.Mutex
	hits map[string][]time.Time
}

// add records an occurrence and reports the number inside the window.
func (s *spikeCounter) add(fp string, now time.Time, window time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	cutoff := now.Add(-window)
	recent := s.hits[fp]
	i := 0
	for i < len(recent) && !recent[i].After(cutoff) {
		i++
	}
	recent = append(recent[i:], now)
	s.hits[fp] = recent
	if len(s.hits) > 5000 { // bounded: drop idle fingerprints
		for k, v := range s.hits {
			if len(v) == 0 || !v[len(v)-1].After(cutoff) {
				delete(s.hits, k)
			}
		}
	}
	return len(recent)
}

// wantsMessage applies the rule of the event to the state of its group.
func (j *Journal) wantsMessage(e errorJournal.Event, rec RecordResult, now time.Time) (send, bypassCooldown bool) {
	rule := e.Notify
	if rule == errorJournal.NotifyDefault {
		rule = errorJournal.DefaultRule(e.Kind)
	}
	isNew := rec.Inserted || rec.Reopened
	count := j.spikes.add(rec.Group.Fingerprint, now, j.cfg.SpikeWindow)
	spike := j.cfg.SpikeThreshold > 0 && count >= j.cfg.SpikeThreshold
	switch rule {
	case errorJournal.NotifyAlways:
		return true, isNew
	case errorJournal.NotifyNew:
		return isNew, isNew
	case errorJournal.NotifyNewOrSpike:
		return isNew || spike, isNew
	case errorJournal.NotifySpike:
		return spike, false
	}
	return false, false
}

func (j *Journal) notify(ctx context.Context, e errorJournal.Event, rec RecordResult, sample errorJournal.Sample) {
	now := j.now()
	send, bypass := j.wantsMessage(e, rec, now)
	if !send {
		return
	}
	// A new fingerprint is always told, so a flood of distinct 403 groups would be a flood of messages: this kind
	// is told once per cooldown, whichever group it is.
	if e.Kind == errorJournal.KindHTTP403 && !j.kindMessageDue(e.Kind, now) {
		return
	}
	cutoff := now.Add(-j.cfg.NotifyCooldown)
	if bypass {
		cutoff = now.Add(time.Second) // a new fingerprint is always told, whatever the last message was
	}
	suppressed, claimed, err := j.repo.MarkNotified(ctx, rec.Group.ID, now, cutoff)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to claim the error notification")
		return
	}
	if !claimed {
		return
	}
	text := j.message(rec.Group, sample, suppressed)
	j.dispatch(ctx, rec.Group, text, false)
}

// kindMessageDue claims the one message per cooldown of a noisy kind.
func (j *Journal) kindMessageDue(kind errorJournal.Kind, now time.Time) bool {
	j.mu.Lock()
	defer j.mu.Unlock()
	if last, ok := j.kindMessaged[kind]; ok && now.Sub(last) < j.cfg.NotifyCooldown {
		return false
	}
	j.kindMessaged[kind] = now
	return true
}

// message is the notification text: short, plain, no secrets (the sample is already scrubbed).
func (j *Journal) message(g errorJournal.Group, s errorJournal.Sample, sinceLast int64) string {
	var b strings.Builder
	env := j.cfg.Environment
	if env == "" {
		env = "platform"
	}
	fmt.Fprintf(&b, "[%s] %s\n%s\n", env, g.Kind, g.Title)
	if g.Source != "" && !strings.Contains(g.Title, g.Source) {
		fmt.Fprintf(&b, "Source: %s\n", g.Source)
	}
	if s.Message != "" {
		fmt.Fprintf(&b, "%s\n", errorJournal.Truncate(s.Message, 500))
	}
	fmt.Fprintf(&b, "Total: %d", g.Occurrences)
	if sinceLast > 1 {
		fmt.Fprintf(&b, " (%d since the last message)", sinceLast)
	}
	fmt.Fprintf(&b, " · first %s · last %s UTC\n", g.FirstSeenAt.UTC().Format("2006-01-02 15:04"), g.LastSeenAt.UTC().Format("15:04"))
	if s.RequestID != "" {
		fmt.Fprintf(&b, "Request: %s\n", s.RequestID)
	}
	if s.UserID != nil {
		fmt.Fprintf(&b, "User: %s\n", s.UserID)
	}
	if j.cfg.AdminURL != "" {
		fmt.Fprintf(&b, "%s/errors/%s\n", strings.TrimRight(j.cfg.AdminURL, "/"), g.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}

// subject is the e-mail subject: the first line of the text.
func subject(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	rest := strings.SplitN(text, "\n", 3)
	if len(rest) > 1 {
		line += " · " + errorJournal.Truncate(rest[1], 120)
	}
	return line
}

// dispatch sends the text to every channel. A failure of one chat or address never stops the others. A chat that
// answers 403 is marked failing in the settings, never dropped. test makes a failed chat count as a result.
func (j *Journal) dispatch(ctx context.Context, g errorJournal.Group, text string, test bool) []TestResult {
	settings, err := j.repo.GetSettings(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read the error journal settings")
		return nil
	}
	var results []TestResult
	if j.tg != nil && j.tg.Enabled() {
		for _, chat := range settings.TelegramChats {
			results = append(results, j.sendTelegram(ctx, chat, text))
		}
	}
	if j.mail != nil {
		for _, to := range j.recipients(ctx, settings) {
			results = append(results, j.sendEmail(ctx, to, text))
		}
	}
	return results
}

func (j *Journal) sendTelegram(ctx context.Context, chat errorJournal.TelegramChat, text string) TestResult {
	res := TestResult{Channel: ChannelTelegram, Target: chat.ChatID, Label: chat.Label}
	err := j.tg.Send(ctx, chat.ChatID, text)
	now := j.now()
	switch {
	case err == nil:
		res.OK = true
		if chat.Failing {
			if e := j.repo.SetChatFailing(ctx, chat.ChatID, false, "", now); e != nil {
				log.Warn().Err(e).Msg("Failed to clear a Telegram chat failure")
			}
		}
	default:
		res.Error = errorJournal.Clean(err.Error(), 300)
		if errors.Is(err, telegram.ErrForbidden) || errors.Is(err, telegram.ErrChatNotFound) {
			if e := j.repo.SetChatFailing(ctx, chat.ChatID, true, res.Error, now); e != nil {
				log.Warn().Err(e).Msg("Failed to mark a Telegram chat failing")
			}
		}
		log.Warn().Str("chat", chat.Label).Str("error", res.Error).Msg("Telegram notification failed")
	}
	return res
}

func (j *Journal) sendEmail(ctx context.Context, to, text string) TestResult {
	res := TestResult{Channel: ChannelEmail, Target: to}
	err := j.mail.Deliver(ctx, nil, email.Message{To: to, Subject: subject(text), Text: text})
	if err != nil {
		res.Error = errorJournal.Clean(err.Error(), 300)
		log.Warn().Str("error", res.Error).Msg("Error journal e-mail failed")
		return res
	}
	res.OK = true
	return res
}

// recipients is the e-mail list plus, by default, the super admins; each address once.
func (j *Journal) recipients(ctx context.Context, s errorJournal.Settings) []string {
	seen := map[string]bool{}
	var out []string
	add := func(a string) {
		a = strings.ToLower(strings.TrimSpace(a))
		if a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	for _, a := range s.Emails {
		add(a)
	}
	if s.EmailToSuperAdmins {
		admins, err := j.repo.SuperAdminEmails(ctx)
		if err != nil {
			log.Warn().Err(err).Msg("Failed to list the super admin addresses")
		}
		for _, a := range admins {
			add(a)
		}
	}
	return out
}
