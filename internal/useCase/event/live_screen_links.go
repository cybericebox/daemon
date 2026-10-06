package event

import (
	"context"
	"errors"
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

// LiveScreenView is what a screen link opens: the event identity and the
// published live layout.
type LiveScreenView struct {
	Event  EventInfoView
	Layout eventContentModel.LiveLayout
}

// LiveScreenResultsAccess is how a valid screen link reads results: the
// read-only staff view of this event's live board (full board, freeze per
// LiveFreeze). It carries no role or session usable anywhere else.
var LiveScreenResultsAccess = ResultsAccess{Role: rbac.RolePublic, Screen: true}

func toLiveScreenLinkView(link eventContentModel.LiveScreenLink) LiveScreenLinkView {
	return LiveScreenLinkView{ID: link.ID, CreatedAt: link.CreatedAt, ExpiresAt: link.ExpiresAt}
}

// GetLiveScreenLink returns the event's working link, nil when there is none.
func (u *EventUseCase) GetLiveScreenLink(ctx context.Context, eventID uuid.UUID) (*LiveScreenLinkView, error) {
	link, err := u.liveScreens.Active(ctx, eventID, time.Now())
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return nil, nil
		}
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get live screen link").Err()
	}
	view := toLiveScreenLinkView(link)
	return &view, nil
}

// IssueLiveScreenLink creates the event's link; an existing one (working or
// expired) is revoked in the same statement.
func (u *EventUseCase) IssueLiveScreenLink(ctx context.Context, eventID, by uuid.UUID, expiry eventContentModel.LiveScreenExpiry) (IssuedLiveScreenLinkView, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return IssuedLiveScreenLinkView{}, eventModel.ErrEventNotFound.Err()
		}
		return IssuedLiveScreenLinkView{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	link, token, err := eventContentModel.NewLiveScreenLink(eventID, by, time.Now(), expiry, e.Lifecycle.EffectiveFinishAt())
	if err != nil {
		return IssuedLiveScreenLinkView{}, err
	}
	return u.storeLiveScreenLink(ctx, link, token)
}

// RegenerateLiveScreenLink («Перегенерувати») replaces the working link's
// token with the same expiry: the old URL stops working at once.
func (u *EventUseCase) RegenerateLiveScreenLink(ctx context.Context, eventID, by uuid.UUID) (IssuedLiveScreenLinkView, error) {
	now := time.Now()
	current, err := u.liveScreens.Active(ctx, eventID, now)
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
	return IssuedLiveScreenLinkView{LiveScreenLinkView: toLiveScreenLinkView(link), Token: token}, nil
}

// RevokeLiveScreenLink («Вимкнути») leaves the event without a link; with
// none it is a no-op.
func (u *EventUseCase) RevokeLiveScreenLink(ctx context.Context, eventID uuid.UUID) error {
	if _, err := u.liveScreens.Revoke(ctx, eventID, time.Now()); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to revoke live screen link").Err()
	}
	return nil
}

// ResolveLiveScreenToken checks a screen link for the event it is used on.
// Unknown, revoked, expired, malformed and foreign-event tokens all read the
// same to the client.
func (u *EventUseCase) ResolveLiveScreenToken(ctx context.Context, eventID uuid.UUID, token string) error {
	if !eventContentModel.LiveScreenTokenWellFormed(token) {
		return eventContentModel.ErrLiveScreenTokenInvalid.WithError(errors.New("malformed token")).Err()
	}
	link, err := u.liveScreens.GetByTokenHash(ctx, eventContentModel.HashLiveScreenToken(token))
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventContentModel.ErrLiveScreenTokenInvalid.WithError(errors.New("unknown token")).Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to check live screen link").Err()
	}
	if link.EventID != eventID || !link.Active(time.Now()) {
		return eventContentModel.ErrLiveScreenTokenInvalid.WithError(errors.New("token of another event, revoked or expired")).Err()
	}
	return nil
}

// GetLiveScreen opens a screen link: the event identity and the published
// layout (works before the event is published, like the staff screen).
func (u *EventUseCase) GetLiveScreen(ctx context.Context, eventID uuid.UUID, token string) (LiveScreenView, error) {
	if err := u.ResolveLiveScreenToken(ctx, eventID, token); err != nil {
		return LiveScreenView{}, err
	}
	info, err := u.GetEventInfo(ctx, eventID)
	if err != nil {
		return LiveScreenView{}, err
	}
	editor, err := u.GetLiveLayoutEditor(ctx, eventID)
	if err != nil {
		return LiveScreenView{}, err
	}
	return LiveScreenView{Event: info, Layout: editor.Published}, nil
}
