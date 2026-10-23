package exercise

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"

	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/model"
)

// catalogColumnsKey is the user preference holding the catalog table's columns.
const catalogColumnsKey = "catalog.columns"

// MaxCatalogColumns bounds the stored list; the frontend has a handful of columns.
const MaxCatalogColumns = 32

var catalogColumnName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// CatalogColumn is one column of the user's catalog table: shown or hidden, in the list's order, and its width.
type CatalogColumn struct {
	Key     string `json:"key"`
	Visible bool   `json:"visible"`
	// Width is the column's width in px the user resized it to; absent or 0 = the frontend's default width.
	Width int `json:"width,omitempty" minimum:"0" maximum:"1600"`
}

// MinCatalogColumnWidth and MaxCatalogColumnWidth bound a set column width (px).
const (
	MinCatalogColumnWidth = 60
	MaxCatalogColumnWidth = 1600
)

// ErrCatalogColumnsInvalid: the list is too long, a key is not a column name, a key repeats, or a width is out of
// range.
var ErrCatalogColumnsInvalid = errors.New("catalog columns must be at most 32 distinct column names, each width 0 (default) or 60..1600 px")

// ValidateCatalogColumns checks a list before it is stored: the frontend owns what the keys mean.
func ValidateCatalogColumns(columns []CatalogColumn) error {
	if len(columns) > MaxCatalogColumns {
		return ErrCatalogColumnsInvalid
	}
	seen := make(map[string]struct{}, len(columns))
	for _, column := range columns {
		if !catalogColumnName.MatchString(column.Key) {
			return fmt.Errorf("%w: %q", ErrCatalogColumnsInvalid, column.Key)
		}
		if column.Width != 0 && (column.Width < MinCatalogColumnWidth || column.Width > MaxCatalogColumnWidth) {
			return fmt.Errorf("%w: %q width %d", ErrCatalogColumnsInvalid, column.Key, column.Width)
		}
		if _, ok := seen[column.Key]; ok {
			return fmt.Errorf("%w: %q repeats", ErrCatalogColumnsInvalid, column.Key)
		}
		seen[column.Key] = struct{}{}
	}
	return nil
}

// CatalogColumns returns the caller's catalog columns; nil when they never arranged them (the default applies).
func (u *ExerciseUseCase) CatalogColumns(ctx context.Context, actor Actor) ([]CatalogColumn, error) {
	raw, err := u.repo.GetUserUIPreference(ctx, postgres.GetUserUIPreferenceParams{UserID: actor.UserID, Key: catalogColumnsKey})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to read the catalog columns").Err()
	}
	var columns []CatalogColumn
	// A stored value that no longer reads is treated as never set: the default columns are always valid.
	if json.Unmarshal(raw, &columns) != nil || ValidateCatalogColumns(columns) != nil {
		return nil, nil
	}
	return columns, nil
}

// SetCatalogColumns stores the caller's catalog columns (validated by the caller with ValidateCatalogColumns).
func (u *ExerciseUseCase) SetCatalogColumns(ctx context.Context, actor Actor, columns []CatalogColumn) error {
	if columns == nil {
		columns = []CatalogColumn{}
	}
	raw, err := json.Marshal(columns)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to encode the catalog columns").Err()
	}
	if err := u.repo.UpsertUserUIPreference(ctx, postgres.UpsertUserUIPreferenceParams{UserID: actor.UserID, Key: catalogColumnsKey, Value: raw}); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save the catalog columns").Err()
	}
	return nil
}
