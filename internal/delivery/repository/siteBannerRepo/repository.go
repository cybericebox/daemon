// Package siteBannerRepo is the repository for site banners.
package siteBannerRepo

import (
	"context"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

// Queries is the narrow slice of the sqlc Querier this repository needs.
type Queries interface {
	CreateSiteBanner(ctx context.Context, arg postgres.CreateSiteBannerParams) (postgres.SiteBanner, error)
	UpdateSiteBanner(ctx context.Context, arg postgres.UpdateSiteBannerParams) (postgres.SiteBanner, error)
	GetSiteBanner(ctx context.Context, id uuid.UUID) (postgres.SiteBanner, error)
	DeleteSiteBanner(ctx context.Context, id uuid.UUID) (int64, error)
	ListSiteBanners(ctx context.Context, scopeFilter string) ([]postgres.SiteBanner, error)
	ListVisibleSiteBanners(ctx context.Context, arg postgres.ListVisibleSiteBannersParams) ([]postgres.SiteBanner, error)
}

type Repository struct{ q Queries }

func New(q Queries) *Repository { return &Repository{q: q} }

func (r *Repository) Create(ctx context.Context, b siteBannerModel.Banner) (siteBannerModel.Banner, error) {
	row, err := r.q.CreateSiteBanner(ctx, postgres.CreateSiteBannerParams{
		ID: b.ID, ScopeEventID: nullUUID(b.ScopeEventID), Text: b.Text, LinkUrl: b.LinkURL, LinkLabel: b.LinkLabel,
		Level: string(b.Level), ActiveFrom: ts(b.ActiveFrom), ActiveTo: ts(b.ActiveTo), Dismissible: b.Dismissible,
		Audience: string(b.Audience), IsActive: b.IsActive, CreatedBy: nullUUID(b.CreatedBy),
	})
	if err != nil {
		return siteBannerModel.Banner{}, err
	}
	return toBanner(row), nil
}

func (r *Repository) Update(ctx context.Context, id uuid.UUID, in siteBannerModel.Input) (siteBannerModel.Banner, error) {
	row, err := r.q.UpdateSiteBanner(ctx, postgres.UpdateSiteBannerParams{
		ID: id, Text: in.Text, LinkUrl: in.LinkURL, LinkLabel: in.LinkLabel, Level: string(in.Level),
		ActiveFrom: ts(in.ActiveFrom), ActiveTo: ts(in.ActiveTo), Dismissible: in.Dismissible,
		Audience: string(in.Audience), IsActive: in.IsActive,
	})
	if err != nil {
		return siteBannerModel.Banner{}, err
	}
	return toBanner(row), nil
}

func (r *Repository) Get(ctx context.Context, id uuid.UUID) (siteBannerModel.Banner, error) {
	row, err := r.q.GetSiteBanner(ctx, id)
	if err != nil {
		return siteBannerModel.Banner{}, err
	}
	return toBanner(row), nil
}

// Delete reports whether a banner was removed.
func (r *Repository) Delete(ctx context.Context, id uuid.UUID) (bool, error) {
	n, err := r.q.DeleteSiteBanner(ctx, id)
	return n > 0, err
}

// List returns the banners of a scope: "platform" or an Event id.
func (r *Repository) List(ctx context.Context, scope string) ([]siteBannerModel.Banner, error) {
	rows, err := r.q.ListSiteBanners(ctx, scope)
	if err != nil {
		return nil, err
	}
	return toBanners(rows), nil
}

// Visible returns the banners a viewer sees now. userID is nil for an
// anonymous viewer, eventID "" outside an Event site.
func (r *Repository) Visible(ctx context.Context, eventID string, userID *uuid.UUID) ([]siteBannerModel.Banner, error) {
	rows, err := r.q.ListVisibleSiteBanners(ctx, postgres.ListVisibleSiteBannersParams{
		EventFilter: eventID, UserID: nullUUID(userID),
	})
	if err != nil {
		return nil, err
	}
	return toBanners(rows), nil
}

func toBanners(rows []postgres.SiteBanner) []siteBannerModel.Banner {
	out := make([]siteBannerModel.Banner, 0, len(rows))
	for _, row := range rows {
		out = append(out, toBanner(row))
	}
	return out
}

func toBanner(row postgres.SiteBanner) siteBannerModel.Banner {
	b := siteBannerModel.Banner{
		ID: row.ID, Text: row.Text, LinkURL: row.LinkUrl, LinkLabel: row.LinkLabel,
		Level: siteBannerModel.Level(row.Level), Dismissible: row.Dismissible,
		Audience: siteBannerModel.Audience(row.Audience), IsActive: row.IsActive,
		CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt,
	}
	if row.ScopeEventID.Valid {
		b.ScopeEventID = new(row.ScopeEventID.UUID)
	}
	if row.CreatedBy.Valid {
		b.CreatedBy = new(row.CreatedBy.UUID)
	}
	if row.ActiveFrom.Valid {
		b.ActiveFrom = new(row.ActiveFrom.Time)
	}
	if row.ActiveTo.Valid {
		b.ActiveTo = new(row.ActiveTo.Time)
	}
	return b
}

func nullUUID(id *uuid.UUID) uuid.NullUUID {
	if id == nil {
		return uuid.NullUUID{}
	}
	return uuid.NullUUID{UUID: *id, Valid: true}
}

func ts(t *time.Time) pgtype.Timestamptz {
	if t == nil {
		return pgtype.Timestamptz{}
	}
	return pgtype.Timestamptz{Time: *t, Valid: true}
}
