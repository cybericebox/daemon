package errorJournalUseCase

import (
	"context"
	"net/mail"
	"strings"

	"github.com/gofrs/uuid"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	"github.com/cybericebox/daemon/pkg/telegram"
)

// Limits of the notification lists.
const (
	MaxEmails        = 20
	MaxTelegramChats = 20
	maxLabelRunes    = 60
)

// Channels of a test result.
const (
	ChannelTelegram = "telegram"
	ChannelEmail    = "email"
)

// SettingsView is the settings and what the server can do with them.
type SettingsView struct {
	errorJournal.Settings
	// TelegramEnabled is false while TELEGRAM_BOT_TOKEN is not set: chat ids are kept but nothing is sent.
	TelegramEnabled bool
}

// TestResult is the outcome of the test message for one target.
type TestResult struct {
	Channel string
	Target  string
	Label   string
	OK      bool
	Error   string
}

// SettingsInput replaces the notification lists. Failing flags are not an input: the server owns them.
type SettingsInput struct {
	Emails             []string
	EmailToSuperAdmins bool
	TelegramChats      []ChatInput
}

type ChatInput struct {
	ChatID string
	Label  string
}

func (j *Journal) GetErrorJournalSettings(ctx context.Context) (SettingsView, error) {
	s, err := j.repo.GetSettings(ctx)
	if err != nil {
		return SettingsView{}, err
	}
	return SettingsView{Settings: s, TelegramEnabled: j.tg != nil && j.tg.Enabled()}, nil
}

// SaveSettings validates and stores the lists. A chat id that stays keeps its failing mark.
func (j *Journal) SaveErrorJournalSettings(ctx context.Context, in SettingsInput) (SettingsView, error) {
	emails, err := normalizeEmails(in.Emails)
	if err != nil {
		return SettingsView{}, err
	}
	chats, err := j.mergeChats(ctx, in.TelegramChats)
	if err != nil {
		return SettingsView{}, err
	}
	now := j.now()
	if err = j.repo.SaveEmails(ctx, emails, in.EmailToSuperAdmins, now); err != nil {
		return SettingsView{}, err
	}
	if err = j.repo.ReplaceChats(ctx, chats); err != nil {
		return SettingsView{}, err
	}
	return j.GetErrorJournalSettings(ctx)
}

func normalizeEmails(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := []string{}
	for _, raw := range in {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}
		addr, err := mail.ParseAddress(raw)
		if err != nil || addr.Name != "" || addr.Address != raw {
			return nil, errorJournal.ErrEmailInvalid.Err()
		}
		low := strings.ToLower(addr.Address)
		if !seen[low] {
			seen[low] = true
			out = append(out, low)
		}
	}
	if len(out) > MaxEmails {
		return nil, errorJournal.ErrTooManyEmails.Err()
	}
	return out, nil
}

func (j *Journal) mergeChats(ctx context.Context, in []ChatInput) ([]errorJournal.TelegramChat, error) {
	current, err := j.repo.GetSettings(ctx)
	if err != nil {
		return nil, err
	}
	existing := map[string]errorJournal.TelegramChat{}
	for _, c := range current.TelegramChats {
		existing[c.ChatID] = c
	}
	now := j.now()
	seen := map[string]bool{}
	out := []errorJournal.TelegramChat{}
	for _, c := range in {
		id := strings.TrimSpace(c.ChatID)
		if !telegram.ValidChatID(id) {
			return nil, errorJournal.ErrChatIDInvalid.Err()
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		chat := errorJournal.TelegramChat{ChatID: id, CreatedAt: now}
		if old, ok := existing[id]; ok {
			chat = old
		}
		label := []rune(strings.TrimSpace(c.Label))
		if len(label) > maxLabelRunes {
			label = label[:maxLabelRunes]
		}
		chat.Label = string(label)
		out = append(out, chat)
	}
	if len(out) > MaxTelegramChats {
		return nil, errorJournal.ErrTooManyChats.Err()
	}
	return out, nil
}

// SendTest sends a test message to every chat id and address and reports each one; a chat that answers is no
// longer marked failing, one that is refused is.
func (j *Journal) SendErrorJournalTest(ctx context.Context) ([]TestResult, error) {
	text := "[" + j.envLabel() + "] test\nThis is a test message of the platform error journal."
	g := errorJournal.Group{ID: uuid.Nil}
	results := j.dispatch(ctx, g, text, true)
	if results == nil {
		results = []TestResult{}
	}
	return results, nil
}

func (j *Journal) envLabel() string {
	if j.cfg.Environment == "" {
		return "platform"
	}
	return j.cfg.Environment
}
