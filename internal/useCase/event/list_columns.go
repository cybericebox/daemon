package event

import (
	"context"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/cybericebox/daemon/internal/delivery/repository/eventConfigRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
)

const maxListColumns = 200

// ListColumnView is one extra-field column of a moderation list.
type ListColumnView struct {
	Key     string
	Visible bool
}

func validList(list string) bool { return list == "participants" || list == "teams" }

// GetListColumns returns the event-wide column layout of a moderation list;
// no saved layout is an empty list (clients show every field in form order).
func (u *EventUseCase) GetListColumns(ctx context.Context, eventID uuid.UUID, list string) ([]ListColumnView, error) {
	if !validList(list) {
		return nil, eventConfigModel.ErrListColumnsInvalid.Err()
	}
	columns, err := u.configs.ListColumns(ctx, eventID, list)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) || err == pgx.ErrNoRows {
			return []ListColumnView{}, nil
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get list columns").Err()
	}
	out := make([]ListColumnView, 0, len(columns))
	for _, column := range columns {
		out = append(out, ListColumnView{Key: column.Key, Visible: column.Visible})
	}
	return out, nil
}

// PutListColumns replaces the shared layout (order = slice order).
func (u *EventUseCase) PutListColumns(ctx context.Context, eventID uuid.UUID, list string, columns []ListColumnView, by uuid.UUID) ([]ListColumnView, error) {
	if !validList(list) || len(columns) > maxListColumns {
		return nil, eventConfigModel.ErrListColumnsInvalid.Err()
	}
	seen := make(map[string]struct{}, len(columns))
	stored := make([]eventConfigRepo.ListColumn, 0, len(columns))
	for _, column := range columns {
		key := strings.TrimSpace(column.Key)
		if key == "" || len(key) > 128 {
			return nil, eventConfigModel.ErrListColumnsInvalid.Err()
		}
		if _, duplicate := seen[key]; duplicate {
			return nil, eventConfigModel.ErrListColumnsInvalid.Err()
		}
		seen[key] = struct{}{}
		stored = append(stored, eventConfigRepo.ListColumn{Key: key, Visible: column.Visible})
	}
	if err := u.configs.PutListColumns(ctx, eventID, list, stored, time.Now().UTC(), by); err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to save list columns").Err()
	}
	out := make([]ListColumnView, 0, len(stored))
	for _, column := range stored {
		out = append(out, ListColumnView{Key: column.Key, Visible: column.Visible})
	}
	return out, nil
}
