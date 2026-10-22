package event

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/liveScreenRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
)

// An event has 1..10 live screens («Екран 1», «Екран 2», …), each with its
// own name, published layout, draft and screen link. Every per-screen method
// takes the screen ID; uuid.Nil is the event's first screen, which the
// single-screen routes (before screens existed) keep addressing.

// LiveScreenSummaryView is a screen in the editor's switcher.
type LiveScreenSummaryView struct {
	ID       uuid.UUID
	Name     string
	Position int32
	// Version is the published layout version the open screens poll for.
	Version   int64
	HasDraft  bool
	UpdatedAt time.Time
	// Link is the screen's working link (no token); nil when it has none or
	// when the caller may not see links.
	Link *LiveScreenLinkView
}

// LiveScreenEditorView is one screen with both layouts.
type LiveScreenEditorView struct {
	LiveScreenSummaryView
	Published eventContentModel.LiveLayout
	Draft     *eventContentModel.LiveLayout
}

// CreateLiveScreenInput: Name empty = «Екран N». CopyFrom copies another
// screen (its published layout and draft); Layout starts from a template the
// editor sends (published as is, without a draft); neither = the default.
type CreateLiveScreenInput struct {
	Name     string
	CopyFrom *uuid.UUID
	Layout   *eventContentModel.LiveLayout
}

func toLiveScreenSummary(display eventContentModel.LiveDisplay) LiveScreenSummaryView {
	return LiveScreenSummaryView{ID: display.ID, Name: display.Name, Position: display.Position, Version: display.Layout.Version, HasDraft: display.Draft != nil, UpdatedAt: display.UpdatedAt}
}

func toLiveScreenEditor(display eventContentModel.LiveDisplay) LiveScreenEditorView {
	return LiveScreenEditorView{LiveScreenSummaryView: toLiveScreenSummary(display), Published: display.Layout, Draft: display.Draft}
}

// liveDisplay resolves a screen of the event (uuid.Nil = the first one).
func (u *EventUseCase) liveDisplay(ctx context.Context, eventID, screenID uuid.UUID) (eventContentModel.LiveDisplay, error) {
	return getLiveDisplay(ctx, u.liveScreens, eventID, screenID)
}

func getLiveDisplay(ctx context.Context, repo *liveScreenRepo.Repository, eventID, screenID uuid.UUID) (eventContentModel.LiveDisplay, error) {
	var (
		display eventContentModel.LiveDisplay
		err     error
	)
	if screenID == uuid.Nil {
		display, err = repo.FirstDisplay(ctx, eventID)
	} else {
		display, err = repo.GetDisplay(ctx, eventID, screenID)
	}
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			if screenID == uuid.Nil {
				return eventContentModel.LiveDisplay{}, eventModel.ErrEventNotFound.Err()
			}
			return eventContentModel.LiveDisplay{}, eventContentModel.ErrLiveScreenNotFound.Err()
		}
		return eventContentModel.LiveDisplay{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get live screen").Err()
	}
	return display, nil
}

// ListLiveScreens is the editor's switcher; withLinks adds each screen's
// working link (for callers who manage the event).
func (u *EventUseCase) ListLiveScreens(ctx context.Context, eventID uuid.UUID, withLinks bool) ([]LiveScreenSummaryView, error) {
	displays, err := u.liveScreens.ListDisplays(ctx, eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list live screens").Err()
	}
	if len(displays) == 0 {
		// Every event has a screen; none means no event.
		return nil, eventModel.ErrEventNotFound.Err()
	}
	var links map[uuid.UUID]eventContentModel.LiveScreenLink
	if withLinks {
		if links, err = u.liveScreens.ActiveByScreen(ctx, eventID, time.Now()); err != nil {
			return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list live screen links").Err()
		}
	}
	out := make([]LiveScreenSummaryView, 0, len(displays))
	for _, display := range displays {
		view := toLiveScreenSummary(display)
		if link, ok := links[display.ID]; ok {
			linkView := toLiveScreenLinkView(link)
			view.Link = &linkView
		}
		out = append(out, view)
	}
	return out, nil
}

// GetLiveScreenEditor reads one screen with its published layout and draft.
func (u *EventUseCase) GetLiveScreenEditor(ctx context.Context, eventID, screenID uuid.UUID) (LiveScreenEditorView, error) {
	display, err := u.liveDisplay(ctx, eventID, screenID)
	if err != nil {
		return LiveScreenEditorView{}, err
	}
	return toLiveScreenEditor(display), nil
}

// withLiveScreenLock runs fn in a transaction holding the event row: screens
// are added and deleted under it, so the limit and the last screen hold.
func (u *EventUseCase) withLiveScreenLock(ctx context.Context, eventID uuid.UUID, fn func(context.Context, *liveScreenRepo.Repository) error) error {
	if u.uow == nil {
		return model.ErrPlatform.WithMessage("Event transaction is not configured").Err()
	}
	txCtx, txRepo, unit, err := u.uow.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer unit.Restore()
	repo := liveScreenRepo.New(txRepo)
	if err = repo.LockEvent(txCtx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to lock event live screens").Err()
	}
	if err = fn(txCtx, repo); err != nil {
		return err
	}
	return unit.Save()
}

// CreateLiveScreen adds a screen after the others (at most 10).
func (u *EventUseCase) CreateLiveScreen(ctx context.Context, eventID uuid.UUID, in CreateLiveScreenInput) (LiveScreenSummaryView, error) {
	if in.CopyFrom != nil && in.Layout != nil {
		return LiveScreenSummaryView{}, eventContentModel.ErrLiveLayoutInvalid.WithError(errors.New("a new screen copies a screen or takes a layout, not both")).Err()
	}
	name := ""
	// A blank name is no name: «Екран N».
	if strings.TrimSpace(in.Name) != "" {
		normalized, err := eventContentModel.NormalizeLiveDisplayName(in.Name)
		if err != nil {
			return LiveScreenSummaryView{}, err
		}
		name = normalized
	}
	var template *eventContentModel.LiveLayout
	if in.Layout != nil {
		layout := *in.Layout
		// A new screen's published layout starts its own version count.
		layout.Version = 1
		if err := layout.Validate(); err != nil {
			return LiveScreenSummaryView{}, eventContentModel.ErrLiveLayoutInvalid.WithError(err).Err()
		}
		template = &layout
	}
	var created eventContentModel.LiveDisplay
	err := u.withLiveScreenLock(ctx, eventID, func(txCtx context.Context, repo *liveScreenRepo.Repository) error {
		existing, err := repo.ListDisplays(txCtx, eventID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to list live screens").Err()
		}
		if len(existing) >= eventContentModel.LiveDisplaysMax {
			return eventContentModel.ErrLiveScreenLimit.Err()
		}
		now := time.Now().UTC()
		display := eventContentModel.LiveDisplay{ID: uuid.Must(uuid.NewV7()), EventID: eventID, Name: name, Layout: eventContentModel.DefaultLiveLayout(), CreatedAt: now}
		if display.Name == "" {
			display.Name = eventContentModel.DefaultLiveDisplayName(existing)
		}
		switch {
		case template != nil:
			display.Layout = *template
		case in.CopyFrom != nil:
			source, err := getLiveDisplay(txCtx, repo, eventID, *in.CopyFrom)
			if err != nil {
				return err
			}
			display.Layout, display.Draft = source.Layout, source.Draft
		}
		if created, err = repo.CreateDisplay(txCtx, display); err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to create live screen").Err()
		}
		return nil
	})
	if err != nil {
		return LiveScreenSummaryView{}, err
	}
	return toLiveScreenSummary(created), nil
}

// DuplicateLiveScreen copies a screen (published layout and draft, not its
// link) to a new one after the others.
func (u *EventUseCase) DuplicateLiveScreen(ctx context.Context, eventID, screenID uuid.UUID, name string) (LiveScreenSummaryView, error) {
	source, err := u.liveDisplay(ctx, eventID, screenID)
	if err != nil {
		return LiveScreenSummaryView{}, err
	}
	return u.CreateLiveScreen(ctx, eventID, CreateLiveScreenInput{Name: name, CopyFrom: &source.ID})
}

func (u *EventUseCase) RenameLiveScreen(ctx context.Context, eventID, screenID uuid.UUID, name string) (LiveScreenSummaryView, error) {
	normalized, err := eventContentModel.NormalizeLiveDisplayName(name)
	if err != nil {
		return LiveScreenSummaryView{}, err
	}
	display, err := u.liveDisplay(ctx, eventID, screenID)
	if err != nil {
		return LiveScreenSummaryView{}, err
	}
	renamed, err := u.liveScreens.RenameDisplay(ctx, eventID, display.ID, normalized, time.Now().UTC())
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return LiveScreenSummaryView{}, eventContentModel.ErrLiveScreenNotFound.Err()
		}
		return LiveScreenSummaryView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to rename live screen").Err()
	}
	return toLiveScreenSummary(renamed), nil
}

// ReorderLiveScreens sets the switcher order; screenIDs lists every screen
// of the event once.
func (u *EventUseCase) ReorderLiveScreens(ctx context.Context, eventID uuid.UUID, screenIDs []uuid.UUID) ([]LiveScreenSummaryView, error) {
	err := u.withLiveScreenLock(ctx, eventID, func(txCtx context.Context, repo *liveScreenRepo.Repository) error {
		existing, err := repo.ListDisplays(txCtx, eventID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to list live screens").Err()
		}
		known := make(map[uuid.UUID]bool, len(existing))
		for _, display := range existing {
			known[display.ID] = true
		}
		if len(screenIDs) != len(existing) {
			return eventContentModel.ErrLiveScreenOrderInvalid.Err()
		}
		for _, id := range screenIDs {
			if !known[id] {
				return eventContentModel.ErrLiveScreenOrderInvalid.Err()
			}
			delete(known, id)
		}
		for index, id := range screenIDs {
			if _, err = repo.SetDisplayPosition(txCtx, eventID, id, int32(index)); err != nil {
				return model.ErrPlatform.WithError(err).WithMessage("Failed to reorder live screens").Err()
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return u.ListLiveScreens(ctx, eventID, false)
}

// DeleteLiveScreen removes a screen and its link (its URL stops working at
// once); the last screen of the event stays.
func (u *EventUseCase) DeleteLiveScreen(ctx context.Context, eventID, screenID uuid.UUID) error {
	if screenID == uuid.Nil {
		return eventContentModel.ErrLiveScreenNotFound.Err()
	}
	err := u.withLiveScreenLock(ctx, eventID, func(txCtx context.Context, repo *liveScreenRepo.Repository) error {
		count, err := repo.CountDisplays(txCtx, eventID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to count live screens").Err()
		}
		if _, err = getLiveDisplay(txCtx, repo, eventID, screenID); err != nil {
			return err
		}
		if count <= 1 {
			return eventContentModel.ErrLiveScreenLast.Err()
		}
		deleted, err := repo.DeleteDisplay(txCtx, eventID, screenID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to delete live screen").Err()
		}
		if deleted == 0 {
			return eventContentModel.ErrLiveScreenNotFound.Err()
		}
		return nil
	})
	if err != nil {
		return err
	}
	u.liveScreenTokens.invalidate()
	return nil
}

// SaveLiveLayoutDraft stores the screen's editor layout without publishing it.
func (u *EventUseCase) SaveLiveLayoutDraft(ctx context.Context, eventID, screenID uuid.UUID, layout eventContentModel.LiveLayout) error {
	if err := layout.Validate(); err != nil {
		return eventContentModel.ErrLiveLayoutInvalid.WithError(err).Err()
	}
	display, err := u.liveDisplay(ctx, eventID, screenID)
	if err != nil {
		return err
	}
	affected, err := u.liveScreens.SaveDisplayDraft(ctx, eventID, display.ID, layout, time.Now().UTC())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save event live layout draft").Err()
	}
	if affected == 0 {
		return eventContentModel.ErrLiveScreenNotFound.Err()
	}
	return nil
}

// PublishLiveLayout promotes the screen's draft. The draft passed the
// write-side check when saved, but a legacy draft may predate it (e.g. a grid
// below 3x3), so it is checked again before it becomes the published layout.
func (u *EventUseCase) PublishLiveLayout(ctx context.Context, eventID, screenID uuid.UUID) (eventContentModel.LiveLayout, error) {
	display, err := u.liveDisplay(ctx, eventID, screenID)
	if err != nil {
		return eventContentModel.LiveLayout{}, err
	}
	// A missing draft is reported by PublishDisplay below (zero rows).
	if display.Draft != nil {
		if err = display.Draft.Validate(); err != nil {
			return eventContentModel.LiveLayout{}, eventContentModel.ErrLiveDraftInvalid.WithError(err).Err()
		}
	}
	layout, err := u.liveScreens.PublishDisplay(ctx, eventID, display.ID, time.Now().UTC())
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventContentModel.LiveLayout{}, eventContentModel.ErrLiveDraftNotFound.Err()
		}
		return eventContentModel.LiveLayout{}, model.ErrPlatform.WithError(err).WithMessage("Failed to publish event live layout").Err()
	}
	return layout, nil
}
