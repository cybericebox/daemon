package errorJournalUseCase

import (
	"context"
	"errors"
	"fmt"
	"sort"
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

func (j *Journal) notificationPlan(e errorJournal.Event, rec RecordResult, sample errorJournal.Sample) *NotificationPlan {
	now := j.now()
	if rec.Group.Status == errorJournal.StatusIgnored {
		return nil
	}
	send, bypass := j.wantsMessage(e, rec, now)
	if !send {
		return nil
	}
	if e.Kind == errorJournal.KindHTTP403 && !j.kindMessageDue(e.Kind, now) {
		return nil
	}
	cutoff := now.Add(-j.cfg.NotifyCooldown)
	if bypass {
		cutoff = now.Add(time.Second)
	}
	return &NotificationPlan{Now: now, Cutoff: cutoff, Body: j.message(rec.Group, sample, rec.Group.SuppressedSince), EmailBody: j.emailMessage(rec.Group, sample, rec.Group.SuppressedSince)}
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
		fmt.Fprintf(&b, "%s/errors/%s/\n", strings.TrimRight(j.cfg.AdminURL, "/"), g.ID)
	}
	return strings.TrimRight(b.String(), "\n")
}

// emailMessage keeps the complete recorded sample in the email body. Telegram uses the concise message above.
// The sample has already passed the journal's scrubbing and size limits.
func (j *Journal) emailMessage(g errorJournal.Group, s errorJournal.Sample, sinceLast int64) string {
	summarySample := s
	summarySample.Message = ""
	var b strings.Builder
	b.WriteString(j.message(g, summarySample, sinceLast))
	if s.Message != "" {
		fmt.Fprintf(&b, "\n\n%s", s.Message)
	}
	if len(s.Details) > 0 {
		b.WriteString("\n\nDetails:\n")
		keys := make([]string, 0, len(s.Details))
		for key := range s.Details {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			fmt.Fprintf(&b, "%s: %s\n", key, s.Details[key])
		}
	}
	if s.Stack != "" {
		fmt.Fprintf(&b, "\n\nStack:\n%s", s.Stack)
	}
	return strings.TrimRight(b.String(), "\n")
}

// subject is only the general error kind and environment, never the diagnostic details.
func subject(text string) string {
	line, _, _ := strings.Cut(text, "\n")
	return line
}

// dispatchTest returns actual per-target outcomes for the explicit settings test.
// Recipient/settings lookup failures are returned instead of appearing as an empty successful test.
func (j *Journal) dispatchTest(ctx context.Context, text string) ([]TestResult, error) {
	settings, err := j.repo.GetSettings(ctx)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to read the error journal settings")
		return nil, err
	}
	recipients, err := j.recipientsEligible(ctx, settings)
	if err != nil {
		return nil, err
	}
	results := []TestResult{}
	if j.tg != nil && j.tg.Enabled() {
		for _, chat := range settings.TelegramChats {
			results = append(results, j.sendTelegram(ctx, chat, text))
		}
	}
	if j.mail != nil {
		for _, to := range recipients {
			results = append(results, j.sendEmail(ctx, to, text))
		}
	}
	return results, nil
}

func (j *Journal) sendTelegram(ctx context.Context, chat errorJournal.TelegramChat, text string) TestResult {
	res := TestResult{Channel: ChannelTelegram, Target: chat.ChatID, Label: chat.Label}
	sendCtx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	err := j.tg.Send(sendCtx, chat.ChatID, text)
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
	sendCtx, cancel := context.WithTimeout(ctx, deliveryTimeout)
	defer cancel()
	err := j.mail.Deliver(sendCtx, nil, email.Message{To: to, Subject: subject(text), Text: text})
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
	out, err := j.recipientsEligible(ctx, s)
	if err != nil {
		log.Warn().Err(err).Msg("Failed to check error notification recipients")
	}
	return out
}
