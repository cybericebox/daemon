// Package errorJournalRepo is the storage of the platform error journal: groups by fingerprint, a few samples per
// group, daily 404 counters and the notification settings. The job queue statistics are the one raw query: River
// owns its tables, so they are not part of the sqlc schema.
package errorJournalRepo

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	errorJournalUseCase "github.com/cybericebox/daemon/internal/useCase/errorJournal"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	UpsertErrorGroup(ctx context.Context, arg postgres.UpsertErrorGroupParams) (postgres.UpsertErrorGroupRow, error)
	InsertErrorSample(ctx context.Context, arg postgres.InsertErrorSampleParams) error
	TrimErrorSamples(ctx context.Context, arg postgres.TrimErrorSamplesParams) error
	ListErrorGroups(ctx context.Context, arg postgres.ListErrorGroupsParams) ([]postgres.ErrorGroup, error)
	CountErrorGroups(ctx context.Context, arg postgres.CountErrorGroupsParams) (int64, error)
	GetErrorGroup(ctx context.Context, id uuid.UUID) (postgres.ErrorGroup, error)
	ListErrorSamples(ctx context.Context, arg postgres.ListErrorSamplesParams) ([]postgres.ErrorSample, error)
	SetErrorGroupStatus(ctx context.Context, arg postgres.SetErrorGroupStatusParams) (postgres.ErrorGroup, error)
	ResolveErrorGroupByFingerprint(ctx context.Context, arg postgres.ResolveErrorGroupByFingerprintParams) error
	MarkErrorGroupNotified(ctx context.Context, arg postgres.MarkErrorGroupNotifiedParams) (int64, error)
	AddErrorNotFound(ctx context.Context, arg postgres.AddErrorNotFoundParams) error
	ListErrorNotFound(ctx context.Context, arg postgres.ListErrorNotFoundParams) ([]postgres.ErrorNotFoundDaily, error)
	PurgeErrorGroups(ctx context.Context, lastSeenAt time.Time) (int64, error)
	PurgeErrorSamples(ctx context.Context, occurredAt time.Time) (int64, error)
	PurgeErrorNotFound(ctx context.Context, before pgtype.Date) (int64, error)
	GetErrorJournalSettings(ctx context.Context) (postgres.GetErrorJournalSettingsRow, error)
	UpdateErrorJournalEmails(ctx context.Context, arg postgres.UpdateErrorJournalEmailsParams) error
	ListErrorTelegramChats(ctx context.Context) ([]postgres.ErrorJournalTelegramChat, error)
	DeleteErrorTelegramChatsNotIn(ctx context.Context, keep []string) error
	UpsertErrorTelegramChat(ctx context.Context, arg postgres.UpsertErrorTelegramChatParams) error
	SetErrorTelegramChatFailing(ctx context.Context, arg postgres.SetErrorTelegramChatFailingParams) error
	ListSuperAdminEmails(ctx context.Context) ([]string, error)
}

// Pool is the part of the connection pool the queue statistics use.
type Pool interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Repository struct {
	q    Queries
	pool Pool
}

func New(q Queries, pool Pool) *Repository { return &Repository{q: q, pool: pool} }

var _ errorJournalUseCase.Repository = (*Repository)(nil)

func (r *Repository) Record(ctx context.Context, in errorJournalUseCase.RecordInput) (errorJournalUseCase.RecordResult, error) {
	count := int64(max(in.Count, 1))
	groupID, err := uuid.NewV7()
	if err != nil {
		return errorJournalUseCase.RecordResult{}, err
	}
	row, err := r.q.UpsertErrorGroup(ctx, postgres.UpsertErrorGroupParams{
		ID: groupID, Fingerprint: in.Fingerprint, Kind: string(in.Kind), Source: in.Source, Title: in.Title, At: in.At, N: count,
	})
	if err != nil {
		return errorJournalUseCase.RecordResult{}, err
	}
	s := in.Sample
	details, err := json.Marshal(s.Details)
	if err != nil {
		details = []byte("{}")
	}
	params := postgres.InsertErrorSampleParams{
		ID: s.ID, GroupID: row.ID, OccurredAt: s.OccurredAt, Message: s.Message, Stack: s.Stack, Method: s.Method,
		Route: s.Route, RequestID: s.RequestID, Role: s.Role, Permission: s.Permission, Limiter: s.Limiter, Details: details,
	}
	if s.HTTPStatus != nil {
		params.HttpStatus = pgtype.Int4{Int32: int32(*s.HTTPStatus), Valid: true}
	}
	if s.UserID != nil {
		params.UserID = uuid.NullUUID{UUID: *s.UserID, Valid: true}
	}
	if err = r.q.InsertErrorSample(ctx, params); err != nil {
		return errorJournalUseCase.RecordResult{}, err
	}
	if err = r.q.TrimErrorSamples(ctx, postgres.TrimErrorSamplesParams{Gid: row.ID, Keep: int32(in.Keep)}); err != nil {
		return errorJournalUseCase.RecordResult{}, err
	}
	return errorJournalUseCase.RecordResult{
		Group: errorJournal.Group{
			ID: row.ID, Fingerprint: row.Fingerprint, Kind: errorJournal.Kind(row.Kind), Source: row.Source, Title: row.Title,
			Status: errorJournal.Status(row.Status), Occurrences: row.Occurrences, FirstSeenAt: row.FirstSeenAt,
			LastSeenAt: row.LastSeenAt, ResolvedAt: timePtr(row.ResolvedAt), LastNotifiedAt: timePtr(row.LastNotifiedAt),
			SuppressedSince: row.SuppressedSince,
		},
		Inserted: row.Occurrences == count,
		Reopened: row.Reopened,
	}, nil
}

func (r *Repository) ListGroups(ctx context.Context, f errorJournalUseCase.GroupFilter) ([]errorJournal.Group, int64, error) {
	kinds := make([]string, 0, len(f.Kinds))
	for _, k := range f.Kinds {
		kinds = append(kinds, string(k))
	}
	query := likeEscape(f.Query)
	from, to := tsPtr(f.From), tsPtr(f.To)
	rows, err := r.q.ListErrorGroups(ctx, postgres.ListErrorGroupsParams{
		Kinds: kinds, Status: string(f.Status), FromAt: from, ToAt: to, Query: query, Request: f.Request,
		LimitVal: int32(f.Limit), OffsetVal: int32(f.Offset),
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := r.q.CountErrorGroups(ctx, postgres.CountErrorGroupsParams{
		Kinds: kinds, Status: string(f.Status), FromAt: from, ToAt: to, Query: query, Request: f.Request,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]errorJournal.Group, 0, len(rows))
	for _, row := range rows {
		out = append(out, groupOf(row))
	}
	return out, total, nil
}

// likeEscape makes a user's text match literally inside ILIKE.
func likeEscape(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `%`, `\%`)
	return strings.ReplaceAll(s, `_`, `\_`)
}

func (r *Repository) GetGroup(ctx context.Context, id uuid.UUID) (errorJournal.Group, error) {
	row, err := r.q.GetErrorGroup(ctx, id)
	if err != nil {
		return errorJournal.Group{}, notFound(err)
	}
	return groupOf(row), nil
}

func (r *Repository) ListSamples(ctx context.Context, groupID uuid.UUID, limit int) ([]errorJournal.Sample, error) {
	rows, err := r.q.ListErrorSamples(ctx, postgres.ListErrorSamplesParams{GroupID: groupID, Limit: int32(limit)})
	if err != nil {
		return nil, err
	}
	out := make([]errorJournal.Sample, 0, len(rows))
	for _, row := range rows {
		s := errorJournal.Sample{
			ID: row.ID, GroupID: row.GroupID, OccurredAt: row.OccurredAt, Message: row.Message, Stack: row.Stack,
			Method: row.Method, Route: row.Route, RequestID: row.RequestID, Role: row.Role, Permission: row.Permission,
			Limiter: row.Limiter, Details: map[string]string{},
		}
		if row.HttpStatus.Valid {
			status := int(row.HttpStatus.Int32)
			s.HTTPStatus = &status
		}
		if row.UserID.Valid {
			id := row.UserID.UUID
			s.UserID = &id
		}
		_ = json.Unmarshal(row.Details, &s.Details) // a damaged row still shows its message
		out = append(out, s)
	}
	return out, nil
}

func (r *Repository) SetGroupStatus(ctx context.Context, id uuid.UUID, status errorJournal.Status, now time.Time) (errorJournal.Group, error) {
	row, err := r.q.SetErrorGroupStatus(ctx, postgres.SetErrorGroupStatusParams{ID: id, Status: string(status), Now: now})
	if err != nil {
		return errorJournal.Group{}, notFound(err)
	}
	return groupOf(row), nil
}

func (r *Repository) ResolveByFingerprint(ctx context.Context, fingerprint string, now time.Time) error {
	return r.q.ResolveErrorGroupByFingerprint(ctx, postgres.ResolveErrorGroupByFingerprintParams{
		Fingerprint: fingerprint, ResolvedAt: pgtype.Timestamptz{Time: now, Valid: true},
	})
}

func (r *Repository) MarkNotified(ctx context.Context, id uuid.UUID, now, cutoff time.Time) (int64, bool, error) {
	suppressed, err := r.q.MarkErrorGroupNotified(ctx, postgres.MarkErrorGroupNotifiedParams{
		ID: id, Now: pgtype.Timestamptz{Time: now, Valid: true}, Cutoff: pgtype.Timestamptz{Time: cutoff, Valid: true},
	})
	if repositoryTools.IsObjectNotFoundError(err) {
		return 0, false, nil // another replica sent it first
	}
	if err != nil {
		return 0, false, err
	}
	return suppressed, true, nil
}

func (r *Repository) AddNotFound(ctx context.Context, day time.Time, route string, hits int64) error {
	return r.q.AddErrorNotFound(ctx, postgres.AddErrorNotFoundParams{Day: dateOf(day), Route: route, Hits: hits})
}

func (r *Repository) ListNotFound(ctx context.Context, from, to time.Time) ([]errorJournal.NotFoundDay, error) {
	rows, err := r.q.ListErrorNotFound(ctx, postgres.ListErrorNotFoundParams{FromDay: dateOf(from), ToDay: dateOf(to)})
	if err != nil {
		return nil, err
	}
	out := make([]errorJournal.NotFoundDay, 0, len(rows))
	for _, row := range rows {
		out = append(out, errorJournal.NotFoundDay{Day: row.Day.Time, Route: row.Route, Hits: row.Hits})
	}
	return out, nil
}

func (r *Repository) Purge(ctx context.Context, cutoff time.Time) (errorJournalUseCase.PurgeResult, error) {
	var res errorJournalUseCase.PurgeResult
	var err error
	if res.Samples, err = r.q.PurgeErrorSamples(ctx, cutoff); err != nil {
		return res, err
	}
	if res.Groups, err = r.q.PurgeErrorGroups(ctx, cutoff); err != nil {
		return res, err
	}
	res.NotFound, err = r.q.PurgeErrorNotFound(ctx, dateOf(cutoff))
	return res, err
}

func (r *Repository) GetSettings(ctx context.Context) (errorJournal.Settings, error) {
	row, err := r.q.GetErrorJournalSettings(ctx)
	if err != nil {
		return errorJournal.Settings{}, err
	}
	chats, err := r.q.ListErrorTelegramChats(ctx)
	if err != nil {
		return errorJournal.Settings{}, err
	}
	s := errorJournal.Settings{
		Emails: append([]string{}, row.NotifyEmails...), EmailToSuperAdmins: row.EmailToSuperAdmins, UpdatedAt: row.UpdatedAt,
		TelegramChats: make([]errorJournal.TelegramChat, 0, len(chats)),
	}
	for _, c := range chats {
		s.TelegramChats = append(s.TelegramChats, errorJournal.TelegramChat{
			ChatID: c.ChatID, Label: c.Label, Failing: c.Failing, FailingSince: timePtr(c.FailingSince),
			LastError: c.LastError, CreatedAt: c.CreatedAt,
		})
	}
	return s, nil
}

func (r *Repository) SaveEmails(ctx context.Context, emails []string, toSuperAdmins bool, now time.Time) error {
	if emails == nil {
		emails = []string{}
	}
	return r.q.UpdateErrorJournalEmails(ctx, postgres.UpdateErrorJournalEmailsParams{
		NotifyEmails: emails, EmailToSuperAdmins: toSuperAdmins, UpdatedAt: now,
	})
}

// ReplaceChats makes the stored chats exactly the given ones. A chat that stays keeps its failing mark: only its
// label is written.
func (r *Repository) ReplaceChats(ctx context.Context, chats []errorJournal.TelegramChat) error {
	keep := make([]string, 0, len(chats))
	for _, c := range chats {
		keep = append(keep, c.ChatID)
	}
	if err := r.q.DeleteErrorTelegramChatsNotIn(ctx, keep); err != nil {
		return err
	}
	for _, c := range chats {
		if err := r.q.UpsertErrorTelegramChat(ctx, postgres.UpsertErrorTelegramChatParams{ChatID: c.ChatID, Label: c.Label, CreatedAt: c.CreatedAt}); err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) SetChatFailing(ctx context.Context, chatID string, failing bool, reason string, now time.Time) error {
	return r.q.SetErrorTelegramChatFailing(ctx, postgres.SetErrorTelegramChatFailingParams{ChatID: chatID, Failing: failing, Reason: reason, Now: now})
}

func (r *Repository) SuperAdminEmails(ctx context.Context) ([]string, error) {
	return r.q.ListSuperAdminEmails(ctx)
}

// queueStatsSQL: jobs that are ready to run (or whose retry time has come) and have not started, and how long the
// oldest has waited. River's own tables are not part of the sqlc schema.
const queueStatsSQL = `
SELECT count(*)::bigint,
       COALESCE(EXTRACT(EPOCH FROM ($1::timestamptz - min(scheduled_at))), 0)::float8
FROM river_job
WHERE state IN ('available', 'retryable', 'scheduled')
  AND scheduled_at <= $1::timestamptz`

func (r *Repository) QueueStats(ctx context.Context, now time.Time) (errorJournalUseCase.QueueStats, error) {
	var waiting int64
	var oldest float64
	if err := r.pool.QueryRow(ctx, queueStatsSQL, now).Scan(&waiting, &oldest); err != nil {
		return errorJournalUseCase.QueueStats{}, err
	}
	return errorJournalUseCase.QueueStats{Waiting: waiting, OldestWait: time.Duration(oldest * float64(time.Second)), Queue: "default"}, nil
}

func groupOf(row postgres.ErrorGroup) errorJournal.Group {
	return errorJournal.Group{
		ID: row.ID, Fingerprint: row.Fingerprint, Kind: errorJournal.Kind(row.Kind), Source: row.Source, Title: row.Title,
		Status: errorJournal.Status(row.Status), Occurrences: row.Occurrences, FirstSeenAt: row.FirstSeenAt,
		LastSeenAt: row.LastSeenAt, ResolvedAt: timePtr(row.ResolvedAt), LastNotifiedAt: timePtr(row.LastNotifiedAt),
		SuppressedSince: row.SuppressedSince,
	}
}

func notFound(err error) error {
	if repositoryTools.IsObjectNotFoundError(err) {
		return errorJournal.ErrGroupNotFound.Err()
	}
	return err
}

func timePtr(t pgtype.Timestamptz) *time.Time {
	if !t.Valid {
		return nil
	}
	v := t.Time
	return &v
}

func tsPtr(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}

func dateOf(t time.Time) pgtype.Date {
	y, m, d := t.UTC().Date()
	return pgtype.Date{Time: time.Date(y, m, d, 0, 0, 0, 0, time.UTC), Valid: true}
}
