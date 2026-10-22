package event

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/cybericebox/daemon/internal/model/rbac"
)

// LiveScreenLinkView is the event's working screen link; the token is never
// readable again after it was issued.
type LiveScreenLinkView struct {
	ID        uuid.UUID
	CreatedAt time.Time
	// ExpiresAt nil = «Без обмеження».
	ExpiresAt *time.Time
}

// IssuedLiveScreenLinkView carries the token once, right after it is issued.
type IssuedLiveScreenLinkView struct {
	LiveScreenLinkView
	Token string
}

// LiveScreenView is what a screen link opens: the event identity, its
// screen with the published layout and the stages the timer counts by.
type LiveScreenView struct {
	Event      EventInfoView
	ScreenID   uuid.UUID
	ScreenName string
	Layout     eventContentModel.LiveLayout
	Stages     []LiveScreenStageView
}

// LiveScreenVersionView is the light check of an open screen: reload the
// layout only when the version changes.
type LiveScreenVersionView struct {
	ScreenID uuid.UUID
	Version  int64
}

// LiveScreenStageView is a stage as the live screen timer needs it: its name
// and window, in the event order.
type LiveScreenStageView struct {
	Name     string
	OpensAt  time.Time
	ClosesAt time.Time
}

// LiveScreenResultsAccess is how a valid screen link reads results: the
// read-only staff view of this event's live board (full board, freeze per
// LiveFreeze). It carries no role or session usable anywhere else.
var LiveScreenResultsAccess = ResultsAccess{Role: rbac.RolePublic, Screen: true}

func toLiveScreenLinkView(link eventContentModel.LiveScreenLink) LiveScreenLinkView {
	return LiveScreenLinkView{ID: link.ID, CreatedAt: link.CreatedAt, ExpiresAt: link.ExpiresAt}
}

// GetLiveScreenLink returns the screen's working link (uuid.Nil = the first
// screen), nil when there is none.
func (u *EventUseCase) GetLiveScreenLink(ctx context.Context, eventID, screenID uuid.UUID) (*LiveScreenLinkView, error) {
	display, err := u.liveDisplay(ctx, eventID, screenID)
	if err != nil {
		return nil, err
	}
	link, err := u.liveScreens.Active(ctx, eventID, display.ID, time.Now())
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, nil
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get live screen link").Err()
	}
	view := toLiveScreenLinkView(link)
	return &view, nil
}

// IssueLiveScreenLink creates the screen's link; an existing one (working or
// expired) is revoked in the same statement.
func (u *EventUseCase) IssueLiveScreenLink(ctx context.Context, eventID, screenID, by uuid.UUID, expiry eventContentModel.LiveScreenExpiry) (IssuedLiveScreenLinkView, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return IssuedLiveScreenLinkView{}, eventModel.ErrEventNotFound.Err()
		}
		return IssuedLiveScreenLinkView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	display, err := u.liveDisplay(ctx, eventID, screenID)
	if err != nil {
		return IssuedLiveScreenLinkView{}, err
	}
	link, token, err := eventContentModel.NewLiveScreenLink(eventID, display.ID, by, time.Now(), expiry, e.Lifecycle.EffectiveFinishAt())
	if err != nil {
		return IssuedLiveScreenLinkView{}, err
	}
	return u.storeLiveScreenLink(ctx, link, token)
}

// RegenerateLiveScreenLink («Перегенерувати») replaces the working link's
// token with the same expiry: the old URL stops working at once.
func (u *EventUseCase) RegenerateLiveScreenLink(ctx context.Context, eventID, screenID, by uuid.UUID) (IssuedLiveScreenLinkView, error) {
	display, err := u.liveDisplay(ctx, eventID, screenID)
	if err != nil {
		return IssuedLiveScreenLinkView{}, err
	}
	now := time.Now()
	current, err := u.liveScreens.Active(ctx, eventID, display.ID, now)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return IssuedLiveScreenLinkView{}, eventContentModel.ErrLiveScreenLinkNotFound.Err()
		}
		return IssuedLiveScreenLinkView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get live screen link").Err()
	}
	link, token, err := current.Regenerate(by, now)
	if err != nil {
		return IssuedLiveScreenLinkView{}, err
	}
	return u.storeLiveScreenLink(ctx, link, token)
}

func (u *EventUseCase) storeLiveScreenLink(ctx context.Context, link eventContentModel.LiveScreenLink, token string) (IssuedLiveScreenLinkView, error) {
	if err := u.liveScreens.Issue(ctx, link); err != nil {
		return IssuedLiveScreenLinkView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save live screen link").Err()
	}
	u.liveScreenTokens.invalidate()
	return IssuedLiveScreenLinkView{LiveScreenLinkView: toLiveScreenLinkView(link), Token: token}, nil
}

// RevokeLiveScreenLink («Вимкнути») leaves the screen without a link; with
// none it is a no-op.
func (u *EventUseCase) RevokeLiveScreenLink(ctx context.Context, eventID, screenID uuid.UUID) error {
	display, err := u.liveDisplay(ctx, eventID, screenID)
	if err != nil {
		return err
	}
	if _, err := u.liveScreens.Revoke(ctx, eventID, display.ID, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to revoke live screen link").Err()
	}
	u.liveScreenTokens.invalidate()
	return nil
}

// ResolveLiveScreenToken checks a screen link for the event it is used on.
// Unknown, revoked, expired, malformed and foreign-event tokens all read the
// same to the client.
func (u *EventUseCase) ResolveLiveScreenToken(ctx context.Context, eventID uuid.UUID, token string) error {
	_, err := u.resolveLiveScreenLink(ctx, eventID, token)
	return err
}

// resolveLiveScreenLink is the working link of token on this event.
func (u *EventUseCase) resolveLiveScreenLink(ctx context.Context, eventID uuid.UUID, token string) (eventContentModel.LiveScreenLink, error) {
	if !eventContentModel.LiveScreenTokenWellFormed(token) {
		return eventContentModel.LiveScreenLink{}, eventContentModel.ErrLiveScreenTokenInvalid.WithError(errors.New("malformed token")).Err()
	}
	link, err := u.liveScreens.GetByTokenHash(ctx, eventContentModel.HashLiveScreenToken(token))
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventContentModel.LiveScreenLink{}, eventContentModel.ErrLiveScreenTokenInvalid.WithError(errors.New("unknown token")).Err()
		}
		return eventContentModel.LiveScreenLink{}, model.ErrPlatform.WithError(err).WithMessage("Failed to check live screen link").Err()
	}
	if link.EventID != eventID || !link.Active(time.Now()) {
		return eventContentModel.LiveScreenLink{}, eventContentModel.ErrLiveScreenTokenInvalid.WithError(errors.New("token of another event, revoked or expired")).Err()
	}
	return link, nil
}

// linkedLiveDisplay is the screen a working link opens.
func (u *EventUseCase) linkedLiveDisplay(ctx context.Context, eventID uuid.UUID, token string) (eventContentModel.LiveDisplay, error) {
	link, err := u.resolveLiveScreenLink(ctx, eventID, token)
	if err != nil {
		return eventContentModel.LiveDisplay{}, err
	}
	display, err := u.liveScreens.GetDisplay(ctx, eventID, link.ScreenID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			// Deleted together with its links a moment ago.
			return eventContentModel.LiveDisplay{}, eventContentModel.ErrLiveScreenTokenInvalid.WithError(errors.New("screen deleted")).Err()
		}
		return eventContentModel.LiveDisplay{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get live screen").Err()
	}
	return display, nil
}

// GetLiveScreenVersion is the open screen's light check by its link.
func (u *EventUseCase) GetLiveScreenVersion(ctx context.Context, eventID uuid.UUID, token string) (LiveScreenVersionView, error) {
	display, err := u.linkedLiveDisplay(ctx, eventID, token)
	if err != nil {
		return LiveScreenVersionView{}, err
	}
	return LiveScreenVersionView{ScreenID: display.ID, Version: display.Layout.Version}, nil
}

// GetLiveScreen opens a screen link: the event identity and the published
// layout of the link's screen (works before the event is published, like
// the staff screen).
func (u *EventUseCase) GetLiveScreen(ctx context.Context, eventID uuid.UUID, token string) (LiveScreenView, error) {
	display, err := u.linkedLiveDisplay(ctx, eventID, token)
	if err != nil {
		return LiveScreenView{}, err
	}
	info, err := u.GetEventInfo(ctx, eventID)
	if err != nil {
		return LiveScreenView{}, err
	}
	stages, err := u.stages.List(ctx, eventID)
	if err != nil {
		return LiveScreenView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to list event stages").Err()
	}
	return LiveScreenView{Event: info, ScreenID: display.ID, ScreenName: display.Name, Layout: display.Layout, Stages: toLiveScreenStages(stages)}, nil
}

func toLiveScreenStages(stages []eventModel.Stage) []LiveScreenStageView {
	out := make([]LiveScreenStageView, 0, len(stages))
	for _, stage := range stages {
		out = append(out, LiveScreenStageView{Name: stage.Name, OpensAt: stage.OpensAt, ClosesAt: stage.ClosesAt})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].OpensAt.Before(out[j].OpensAt) })
	return out
}
