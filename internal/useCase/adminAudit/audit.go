package adminAudit

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
)

type IRepository interface {
	CreateAdminAuditLog(context.Context, postgres.CreateAdminAuditLogParams) error
	ListAdminAuditLog(context.Context, postgres.ListAdminAuditLogParams) ([]postgres.AdminAuditLog, error)
}

type Entry struct {
	ActorID        uuid.UUID
	Permission     string
	Method         string
	Route          string
	ResponseStatus int
	// Target names the object the action addressed (route ids); empty when the
	// route template already says everything.
	Target string
}

type UseCase struct{ repo IRepository }

func New(repo IRepository) *UseCase { return &UseCase{repo: repo} }

// RecordAdminAction deliberately stores route templates and response metadata only:
// request bodies can contain passwords, tokens, challenge answers, and VPN material.
func (u *UseCase) RecordAdminAction(ctx context.Context, entry Entry) error {
	return u.repo.CreateAdminAuditLog(ctx, postgres.CreateAdminAuditLogParams{
		ID: uuid.Must(uuid.NewV7()), ActorID: entry.ActorID, Permission: entry.Permission,
		Method: entry.Method, Route: entry.Route, ResponseStatus: int32(entry.ResponseStatus), CreatedAt: time.Now(), Target: entry.Target,
	})
}

// MaxPageSize is the largest page of the journal.
const MaxPageSize = 200

// DefaultPageSize applies when the caller names no limit.
const DefaultPageSize = 50

// ErrInvalidCursor: the cursor is not one this journal handed out.
var ErrInvalidCursor = errors.New("adminAudit: invalid cursor")

// Filter narrows the journal. Every field is optional (the zero value matches everything).
type Filter struct {
	ActorID uuid.NullUUID
	// Permission is an exact match on the gating permission.
	Permission string
	// Route is a "contains" match on the route template.
	Route string
	// Method is an exact (upper-case) HTTP method.
	Method string
	// StatusMin/StatusMax bound the final response status (an exact status has them equal, a class
	// 4xx is 400..499); zero means open on that side.
	StatusMin, StatusMax int32
	From, To             *time.Time
	// TargetKind matches the "kind:" token of the target ("event", "team", "agent"...), TargetID a
	// contains match on an id inside the target.
	TargetKind, TargetID string
	// Cursor continues after the last row of the previous page.
	Cursor string
	// Limit is the page size, 1..MaxPageSize (0 = DefaultPageSize).
	Limit int32
}

// Page is one page of the journal; NextCursor is empty on the last page.
type Page struct {
	Items      []postgres.AdminAuditLog
	NextCursor string
}

// ListAdminActions returns one page of the journal, newest first.
func (u *UseCase) ListAdminActions(ctx context.Context, f Filter) (Page, error) {
	limit := f.Limit
	if limit <= 0 {
		limit = DefaultPageSize
	}
	if limit > MaxPageSize {
		limit = MaxPageSize
	}
	params := postgres.ListAdminAuditLogParams{
		ActorID:    f.ActorID,
		Permission: text(f.Permission),
		Route:      text(f.Route),
		Method:     text(strings.ToUpper(f.Method)),
		TargetKind: text(f.TargetKind),
		TargetID:   text(f.TargetID),
		StatusMin:  pgtype.Int4{Int32: f.StatusMin, Valid: f.StatusMin > 0},
		StatusMax:  pgtype.Int4{Int32: f.StatusMax, Valid: f.StatusMax > 0},
		LimitVal:   limit + 1, // one more than the page: whether there is a next one
	}
	if f.From != nil {
		params.FromAt = pgtype.Timestamptz{Time: *f.From, Valid: true}
	}
	if f.To != nil {
		params.ToAt = pgtype.Timestamptz{Time: *f.To, Valid: true}
	}
	if f.Cursor != "" {
		at, id, err := decodeCursor(f.Cursor)
		if err != nil {
			return Page{}, err
		}
		params.CursorCreatedAt = pgtype.Timestamptz{Time: at, Valid: true}
		params.CursorID = uuid.NullUUID{UUID: id, Valid: true}
	}
	rows, err := u.repo.ListAdminAuditLog(ctx, params)
	if err != nil {
		return Page{}, err
	}
	page := Page{Items: rows}
	if len(rows) > int(limit) {
		page.Items = rows[:limit]
		last := page.Items[limit-1]
		page.NextCursor = encodeCursor(last.CreatedAt, last.ID)
	}
	return page, nil
}

func text(v string) pgtype.Text { return pgtype.Text{String: v, Valid: v != ""} }

// The cursor is the (created_at, id) of the last row, opaque to the client.
func encodeCursor(at time.Time, id uuid.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(time.RFC3339Nano) + "|" + id.String()))
}

func decodeCursor(cursor string) (time.Time, uuid.UUID, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	stamp, idText, ok := strings.Cut(string(raw), "|")
	if !ok {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	at, err := time.Parse(time.RFC3339Nano, stamp)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	id, err := uuid.FromString(idText)
	if err != nil {
		return time.Time{}, uuid.Nil, ErrInvalidCursor
	}
	return at, id, nil
}
