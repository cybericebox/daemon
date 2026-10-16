package event

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	eventContentModel "github.com/cybericebox/daemon/internal/model/eventContent"
	"github.com/gofrs/uuid"
)

// ErrRenderRevisionChanged asks the caller to load a fresh manifest, never cache a mixed bundle.
var ErrRenderRevisionChanged = errors.New("event common page revision changed")

type RenderPageInfo struct {
	Slug  string `json:"Slug"`
	Title string `json:"Title"`
}
type EventRenderManifestView struct {
	Event      EventInfoView
	Revision   string
	Audience   string
	Page       RenderPageInfo
	CacheUntil int64
}
type EventRenderPageView struct {
	Images    map[string]RenderImageDimensions
	Revision  string
	Event     EventInfoView
	Page      RenderPageInfo
	Document  eventContentModel.Document
	Variables map[string]any
}

func renderRevision(revision string, phase eventModel.LifecycleStatus) string {
	hash := sha256.Sum256([]byte(fmt.Sprintf("%s:%d", revision, phase)))
	return hex.EncodeToString(hash[:])
}

// GetEventRenderManifest is intentionally independent of GetEventInfo (which
// resolves infrastructure) and GetEventContent (which reads live statistics).
// preview is an authority already authenticated by the dedicated render delivery
// path. It is never an identity or permission for any other application method.
func (u *EventUseCase) GetEventRenderManifest(ctx context.Context, eventID uuid.UUID, slug string, preview bool, now time.Time) (EventRenderManifestView, error) {
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventRenderManifestView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventRenderManifestView{}, model.ErrPlatform.WithError(err).Err()
	}
	phase := e.Lifecycle.Status(now)
	hidden := phase == eventModel.LifecycleNotPublished || phase == eventModel.LifecycleWithdrawn
	if hidden && !preview {
		return EventRenderManifestView{}, eventModel.ErrEventNotFound.Err()
	}
	meta, err := u.content.RenderMetadata(ctx, eventID, slug)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return EventRenderManifestView{}, eventModel.ErrEventNotFound.Err()
		}
		return EventRenderManifestView{}, model.ErrPlatform.WithError(err).Err()
	}
	if !meta.Allowed {
		return EventRenderManifestView{}, eventModel.ErrEventNotFound.Err()
	}
	cfg, err := u.configs.Get(ctx, eventID)
	if err != nil {
		return EventRenderManifestView{}, model.ErrPlatform.WithError(err).Err()
	}
	info := toEventInfoView(e, now, cfg, EventInfrastructurePlan{})
	if u.brandMedia != nil {
		info.LogoURL, err = u.EventLogoURL(ctx, eventID)
		if err != nil {
			return EventRenderManifestView{}, err
		}
		info.FaviconURL, err = u.EventFaviconURL(ctx, eventID)
		if err != nil {
			return EventRenderManifestView{}, err
		}
	}
	until := now.Add(time.Second)
	for _, at := range []*time.Time{&e.Lifecycle.PublishAt, &e.Lifecycle.StartAt, e.Lifecycle.EffectiveFinishAt(), e.Lifecycle.WithdrawAt, &e.AvailableFrom, &e.ArchiveAt} {
		if at != nil && at.After(now) && at.Before(until) {
			until = *at
		}
	}
	audience := "public"
	if preview {
		audience = "staff"
	}
	return EventRenderManifestView{Event: info, Revision: renderRevision(meta.Revision, phase), Audience: audience, Page: RenderPageInfo{Slug: meta.Slug, Title: meta.Title}, CacheUntil: until.UnixMilli()}, nil
}

func (u *EventUseCase) GetEventRenderPage(ctx context.Context, eventID uuid.UUID, slug, revision string, preview bool, now time.Time) (EventRenderPageView, error) {
	before, err := u.GetEventRenderManifest(ctx, eventID, slug, preview, now)
	if err != nil {
		return EventRenderPageView{}, err
	}
	if before.Revision != revision {
		return EventRenderPageView{}, ErrRenderRevisionChanged
	}
	meta, document, err := u.content.RenderDocument(ctx, eventID, slug)
	if err != nil {
		return EventRenderPageView{}, model.ErrPlatform.WithError(err).Err()
	}
	if !meta.Allowed {
		return EventRenderPageView{}, eventModel.ErrEventNotFound.Err()
	}
	if renderRevision(meta.Revision, before.Event.Status) != revision {
		return EventRenderPageView{}, ErrRenderRevisionChanged
	}
	if err = validateContentAudience(document, eventContentModel.PageVisibilityPublic); err != nil {
		return EventRenderPageView{}, eventContentModel.ErrPageInvalid.WithError(err).Err()
	}
	content, err := u.GetEventContent(ctx, eventID)
	if err != nil {
		return EventRenderPageView{}, err
	}
	values := projectContentVariables(document, content.Variables, eventContentModel.PageVisibilityPublic)
	for key, value := range values {
		if at, ok := value.(time.Time); ok {
			values[key] = at.Format(time.RFC3339Nano)
		}
	}
	images := u.renderBannerDimensions(ctx, eventID, document, before.Event.PreviewPicture)
	after, err := u.GetEventRenderManifest(ctx, eventID, slug, preview, time.Now().UTC())
	if err != nil {
		return EventRenderPageView{}, err
	}
	if after.Revision != revision || after.Audience != before.Audience {
		return EventRenderPageView{}, ErrRenderRevisionChanged
	}
	return EventRenderPageView{Revision: revision, Event: after.Event, Page: after.Page, Document: document, Variables: values, Images: images}, nil
}
