package liveScreenRepo

import (
	"context"
	"encoding/json"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

// displayFromDB maps a screen row; its layouts pass the read-side checks.
func displayFromDB(row postgres.EventLiveScreen) (eventContentModel.LiveDisplay, error) {
	display := eventContentModel.LiveDisplay{ID: row.ID, EventID: row.EventID, Position: row.Position, Name: row.Name, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	if err := json.Unmarshal(row.Layout, &display.Layout); err != nil {
		return eventContentModel.LiveDisplay{}, err
	}
	if len(row.LayoutDraft) > 0 && string(row.LayoutDraft) != "null" {
		var draft eventContentModel.LiveLayout
		if err := json.Unmarshal(row.LayoutDraft, &draft); err != nil {
			return eventContentModel.LiveDisplay{}, err
		}
		display.Draft = &draft
	}
	return display, display.ValidateStored()
}

// ListDisplays is the event's screens in the editor order.
func (r *Repository) ListDisplays(ctx context.Context, eventID uuid.UUID) ([]eventContentModel.LiveDisplay, error) {
	rows, err := r.q.ListEventLiveScreens(ctx, eventID)
	if err != nil {
		return nil, err
	}
	out := make([]eventContentModel.LiveDisplay, 0, len(rows))
	for _, row := range rows {
		display, mapErr := displayFromDB(row)
		if mapErr != nil {
			return nil, mapErr
		}
		out = append(out, display)
	}
	return out, nil
}

// GetDisplay is one screen of the event; pgx.ErrNoRows for another event's.
func (r *Repository) GetDisplay(ctx context.Context, eventID, displayID uuid.UUID) (eventContentModel.LiveDisplay, error) {
	row, err := r.q.GetEventLiveScreen(ctx, postgres.GetEventLiveScreenParams{ID: displayID, EventID: eventID})
	if err != nil {
		return eventContentModel.LiveDisplay{}, err
	}
	return displayFromDB(row)
}

// FirstDisplay is the screen of the single-screen routes.
func (r *Repository) FirstDisplay(ctx context.Context, eventID uuid.UUID) (eventContentModel.LiveDisplay, error) {
	row, err := r.q.GetFirstEventLiveScreen(ctx, eventID)
	if err != nil {
		return eventContentModel.LiveDisplay{}, err
	}
	return displayFromDB(row)
}

// LockEvent holds the event row until the transaction ends: screens are
// added and deleted under it.
func (r *Repository) LockEvent(ctx context.Context, eventID uuid.UUID) error {
	_, err := r.q.LockEventForLiveScreenChange(ctx, eventID)
	return err
}

func (r *Repository) CountDisplays(ctx context.Context, eventID uuid.UUID) (int64, error) {
	return r.q.CountEventLiveScreens(ctx, eventID)
}

// CreateDisplay stores a new screen after the others.
func (r *Repository) CreateDisplay(ctx context.Context, display eventContentModel.LiveDisplay) (eventContentModel.LiveDisplay, error) {
	layout, err := json.Marshal(display.Layout)
	if err != nil {
		return eventContentModel.LiveDisplay{}, err
	}
	var draft []byte
	if display.Draft != nil {
		if draft, err = json.Marshal(display.Draft); err != nil {
			return eventContentModel.LiveDisplay{}, err
		}
	}
	row, err := r.q.CreateEventLiveScreen(ctx, postgres.CreateEventLiveScreenParams{
		ID: display.ID, EventID: display.EventID, Name: display.Name, Layout: layout, LayoutDraft: draft, CreatedAt: display.CreatedAt,
	})
	if err != nil {
		return eventContentModel.LiveDisplay{}, err
	}
	return displayFromDB(row)
}

// RenameDisplay; pgx.ErrNoRows when the screen is not the event's.
func (r *Repository) RenameDisplay(ctx context.Context, eventID, displayID uuid.UUID, name string, now time.Time) (eventContentModel.LiveDisplay, error) {
	row, err := r.q.RenameEventLiveScreen(ctx, postgres.RenameEventLiveScreenParams{ID: displayID, EventID: eventID, Name: name, UpdatedAt: now})
	if err != nil {
		return eventContentModel.LiveDisplay{}, err
	}
	return displayFromDB(row)
}

func (r *Repository) SetDisplayPosition(ctx context.Context, eventID, displayID uuid.UUID, position int32) (int64, error) {
	return r.q.SetEventLiveScreenPosition(ctx, postgres.SetEventLiveScreenPositionParams{ID: displayID, EventID: eventID, Position: position})
}

func (r *Repository) SaveDisplayDraft(ctx context.Context, eventID, displayID uuid.UUID, layout eventContentModel.LiveLayout, now time.Time) (int64, error) {
	encoded, err := json.Marshal(layout)
	if err != nil {
		return 0, err
	}
	return r.q.SaveEventLiveScreenDraft(ctx, postgres.SaveEventLiveScreenDraftParams{ID: displayID, EventID: eventID, LayoutDraft: encoded, UpdatedAt: now})
}

// PublishDisplay promotes the screen's draft; pgx.ErrNoRows without a draft.
func (r *Repository) PublishDisplay(ctx context.Context, eventID, displayID uuid.UUID, now time.Time) (eventContentModel.LiveLayout, error) {
	encoded, err := r.q.PublishEventLiveScreen(ctx, postgres.PublishEventLiveScreenParams{ID: displayID, EventID: eventID, UpdatedAt: now})
	if err != nil {
		return eventContentModel.LiveLayout{}, err
	}
	var layout eventContentModel.LiveLayout
	if err = json.Unmarshal(encoded, &layout); err != nil {
		return eventContentModel.LiveLayout{}, err
	}
	return layout, layout.ValidateStored()
}

// DeleteDisplay removes the screen together with its links.
func (r *Repository) DeleteDisplay(ctx context.Context, eventID, displayID uuid.UUID) (int64, error) {
	return r.q.DeleteEventLiveScreen(ctx, postgres.DeleteEventLiveScreenParams{ID: displayID, EventID: eventID})
}
