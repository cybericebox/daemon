package event

import (
	"context"
	"fmt"
	"io"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	eventModel "github.com/cybericebox/daemon/internal/model/event"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	inAppModel "github.com/cybericebox/daemon/internal/model/notification/inapp"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	notificationPayloads "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
)

// Event templates inherit from the platform: for every Event-scoped type the
// Event sees its own version family when it has overridden the type, otherwise
// the platform published version as a read-only view (Source=platform).
// Mutations only ever touch Event-owned rows (ownedEvent*Template); a platform
// row becomes editable for the Event only through Customize*, which copies it
// into an Event draft.

// TemplateSourcePlatform marks an entry inherited from the platform published
// version; TemplateSourceEvent marks an Event-owned override.
const (
	TemplateSourcePlatform = "platform"
	TemplateSourceEvent    = "event"
)

// EventEmailTemplateView is an email template as an Event sees it, tagged with
// where it comes from.
type EventEmailTemplateView struct {
	emailModel.EmailTemplate
	Source string
}

// EventInAppTemplateView is an in-app template as an Event sees it, tagged
// with where it comes from.
type EventInAppTemplateView struct {
	inAppModel.InAppTemplate
	Source string
}

func (u *EventUseCase) ListEventEmailTemplates(ctx context.Context, eventID uuid.UUID, filter emailModel.ListFilter) ([]EventEmailTemplateView, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return nil, err
	}
	// Event rows are loaded regardless of status: any Event row means the type
	// is overridden, so a status filter must not make it fall back to platform.
	eventRows, err := u.emailTemplates.List(ctx, filter.Type, "", &eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event email templates").Err()
	}
	platformRows, err := u.emailTemplates.List(ctx, filter.Type, notificationModel.TemplateStatusPublished, nil)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list platform email templates").Err()
	}
	status := notificationModel.TemplateStatus(filter.Status)
	return resolveEventTemplates(eventRows, platformRows, filter.Type,
		func(t emailModel.EmailTemplate) string { return t.NotificationType },
		func(t emailModel.EmailTemplate) bool { return status == "" || t.Status == status },
		func(t emailModel.EmailTemplate, source string) EventEmailTemplateView {
			return EventEmailTemplateView{EmailTemplate: t, Source: source}
		}), nil
}

func (u *EventUseCase) CreateEventEmailTemplate(ctx context.Context, eventID uuid.UUID, in emailModel.CreateTemplateInput) (emailModel.EmailTemplate, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if !notificationTypes.IsEventScoped(notificationTypes.NotificationType(in.NotificationType)) {
		return emailModel.EmailTemplate{}, notificationModel.ErrTemplateTypeNotEventScoped.Err()
	}
	if err := validateEventEmailTemplate(notificationTypes.NotificationType(in.NotificationType), in.Subject, in.Preheader, in.Body, in.Styling); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := u.emailImages.ValidateEventBody(ctx, u.emailTemplates, eventID, in.Body, nil); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	in.ScopeEventID = &eventID
	created, err := u.emailTemplates.CreateDraft(ctx, emailModel.NewDraft(in))
	if err != nil {
		return emailModel.EmailTemplate{}, classifyTemplateWriteError(err, "create event email template")
	}
	if err := u.emailImages.SyncReferences(ctx, created.ID, created.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return created, nil
}

// GetEventEmailTemplate returns an Event-owned row or the platform published
// row of an Event-scoped type (read-only inheritance view).
func (u *EventUseCase) GetEventEmailTemplate(ctx context.Context, eventID, templateID uuid.UUID) (EventEmailTemplateView, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return EventEmailTemplateView{}, err
	}
	template, err := u.emailTemplates.GetByID(ctx, templateID)
	if err != nil {
		return EventEmailTemplateView{}, classifyTemplateReadError(err)
	}
	source, ok := eventTemplateSource(template.ScopeEventID, template.NotificationType, template.Status, eventID)
	if !ok {
		return EventEmailTemplateView{}, notificationModel.ErrTemplateNotFound.Err()
	}
	return EventEmailTemplateView{EmailTemplate: template, Source: source}, nil
}

// ownedEventEmailTemplate is used by every mutation: only rows owned by this
// Event may be changed through the Event API.
func (u *EventUseCase) ownedEventEmailTemplate(ctx context.Context, eventID, templateID uuid.UUID) (emailModel.EmailTemplate, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	template, err := u.emailTemplates.GetByID(ctx, templateID)
	if err != nil {
		return emailModel.EmailTemplate{}, classifyTemplateReadError(err)
	}
	if !isEventScope(template.ScopeEventID, eventID) {
		return emailModel.EmailTemplate{}, notificationModel.ErrTemplateNotFound.Err()
	}
	return template, nil
}

// CustomizeEventEmailTemplate overrides an inherited type for this Event: the
// platform published version is copied into a new Event draft. A type the
// Event already overrides (any Event row) is rejected with
// ErrTemplateDraftExists; a concurrent customize that slips past that check
// hits the one-draft-per-(type, scope) unique index, which
// classifyTemplateWriteError maps to the same 409. The copied images need no
// ownership check: the platform row already references them.
func (u *EventUseCase) CustomizeEventEmailTemplate(ctx context.Context, eventID, platformTemplateID, updatedBy uuid.UUID) (emailModel.EmailTemplate, error) {
	view, err := u.GetEventEmailTemplate(ctx, eventID, platformTemplateID)
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if view.Source != TemplateSourcePlatform {
		return emailModel.EmailTemplate{}, notificationModel.ErrTemplateNotFound.Err()
	}
	source := view.EmailTemplate
	if err := u.ensureEmailTypeNotOverridden(ctx, eventID, source.NotificationType); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	created, err := u.emailTemplates.CreateDraft(ctx, emailModel.NewDraft(emailModel.CreateTemplateInput{
		ScopeEventID:     &eventID,
		NotificationType: source.NotificationType,
		Subject:          source.Subject,
		Preheader:        source.Preheader,
		Body:             source.Body,
		Styling:          source.Styling,
		UpdatedBy:        updatedBy,
	}))
	if err != nil {
		return emailModel.EmailTemplate{}, classifyTemplateWriteError(err, "customize event email template")
	}
	if err := u.emailImages.SyncReferences(ctx, created.ID, created.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return created, nil
}

func (u *EventUseCase) UpdateEventEmailTemplate(ctx context.Context, eventID uuid.UUID, in emailModel.UpdateTemplateInput) (emailModel.EmailTemplate, error) {
	template, err := u.ownedEventEmailTemplate(ctx, eventID, in.ID)
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := validateEventEmailTemplate(notificationTypes.NotificationType(template.NotificationType), in.Subject, in.Preheader, in.Body, in.Styling); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := u.emailImages.ValidateEventBody(ctx, u.emailTemplates, eventID, in.Body, template.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	updated, err := u.emailTemplates.UpdateDraft(ctx, in)
	if err != nil {
		return emailModel.EmailTemplate{}, classifyTemplateWriteError(err, "update event email template")
	}
	if err := u.emailImages.SyncReferences(ctx, updated.ID, updated.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return updated, nil
}

func (u *EventUseCase) DeleteEventEmailTemplate(ctx context.Context, eventID, templateID uuid.UUID) error {
	if _, err := u.ownedEventEmailTemplate(ctx, eventID, templateID); err != nil {
		return err
	}
	affected, err := u.emailTemplates.DeleteDraft(ctx, templateID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event email template").Err()
	}
	if affected == 0 {
		return notificationModel.ErrTemplateNotFound.Err()
	}
	return u.emailImages.RemoveReferences(ctx, templateID)
}

// ResetEventEmailTemplateType drops this Event's override of an Event-scoped
// type: every Event row of it (draft, published and history) is deleted in
// one statement, together with the rows' image references, so the Event falls
// back to the platform template (spec §1). Idempotent: resetting a type the
// Event does not override succeeds.
func (u *EventUseCase) ResetEventEmailTemplateType(ctx context.Context, eventID uuid.UUID, notificationType string) error {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return err
	}
	if !notificationTypes.IsEventScoped(notificationTypes.NotificationType(notificationType)) {
		return notificationModel.ErrTemplateTypeNotEventScoped.Err()
	}
	deleted, err := u.emailTemplates.DeleteEventType(ctx, eventID, notificationType)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reset event email template type").Err()
	}
	for _, id := range deleted {
		if err := u.emailImages.RemoveReferences(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

func (u *EventUseCase) PublishEventEmailTemplate(ctx context.Context, eventID, templateID, updatedBy uuid.UUID) (emailModel.EmailTemplate, error) {
	template, err := u.ownedEventEmailTemplate(ctx, eventID, templateID)
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := validateEventEmailTemplate(notificationTypes.NotificationType(template.NotificationType), template.Subject, template.Preheader, template.Body, template.Styling); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	brand, err := u.ResolveEventEmailBrand(ctx, eventID)
	if err != nil {
		return emailModel.EmailTemplate{}, err
	}
	if err := u.emailImages.CheckInlineSize(ctx, template.Body, int64(len(brand.Logo))); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	published, err := u.emailTemplates.Publish(ctx, templateID, updatedBy)
	if err != nil {
		return emailModel.EmailTemplate{}, classifyTemplateWriteError(err, "publish event email template")
	}
	return published, nil
}

func (u *EventUseCase) RollbackEventEmailTemplate(ctx context.Context, eventID, sourceID, updatedBy uuid.UUID) (emailModel.EmailTemplate, error) {
	if _, err := u.ownedEventEmailTemplate(ctx, eventID, sourceID); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	draft, err := u.emailTemplates.Rollback(ctx, sourceID, updatedBy)
	if err != nil {
		return emailModel.EmailTemplate{}, classifyTemplateWriteError(err, "rollback event email template")
	}
	if err := u.emailImages.SyncReferences(ctx, draft.ID, draft.Body); err != nil {
		return emailModel.EmailTemplate{}, err
	}
	return draft, nil
}

// PreviewEventEmail renders an Event email template draft (saved or not) with
// the brand of this Event, through the same composition as dispatch. Only
// Event-scoped types can be previewed here; nothing is persisted. Images are
// embedded as data: URIs; every uploaded image the draft renders (presets
// included) must be usable by this Event (ErrTemplateImageInvalid otherwise).
func (u *EventUseCase) PreviewEventEmail(ctx context.Context, eventID uuid.UUID, in emailUseCase.PreviewInput) (emailUseCase.PreviewOutput, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return emailUseCase.PreviewOutput{}, err
	}
	if !notificationTypes.IsEventScoped(notificationTypes.NotificationType(in.NotificationType)) {
		return emailUseCase.PreviewOutput{}, notificationModel.ErrTemplateTypeNotEventScoped.Err()
	}
	return emailUseCase.Preview(ctx, u.emailTemplates, u.emailImages, u.emailFooters,
		&emailUseCase.EventPreviewScope{EventID: eventID, Source: u.emailTemplates, Brands: u}, in)
}

// StreamEventEmailImage opens an image used by a platform email template, a
// block preset or one of this Event's own template rows (ErrFileNotFound
// otherwise — including images of OTHER Events), for the Event template
// preview. Caller closes the reader.
func (u *EventUseCase) StreamEventEmailImage(ctx context.Context, eventID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return nil, mediaModel.File{}, err
	}
	return u.emailImages.StreamForEvent(ctx, u.emailTemplates, eventID, fileID)
}

// UploadEventEmailImage accepts a new image only for an Event-owned draft.
// Its draft reference also grants the event preview/image route access.
func (u *EventUseCase) UploadEventEmailImage(ctx context.Context, eventID, templateID, userID uuid.UUID, reader io.Reader, name string) (mediaModel.File, error) {
	template, err := u.ownedEventEmailTemplate(ctx, eventID, templateID)
	if err != nil {
		return mediaModel.File{}, err
	}
	if !template.IsDraft() {
		return mediaModel.File{}, notificationModel.ErrTemplateNotFound.Err()
	}
	return u.emailImages.UploadForTemplate(ctx, templateID, reader, name, userID)
}

// EventEmailBrandLogo returns the same logo that preview and dispatch inline.
func (u *EventUseCase) EventEmailBrandLogo(ctx context.Context, eventID uuid.UUID) (data []byte, contentType string, err error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return nil, "", err
	}
	brand, err := u.ResolveEventEmailBrand(ctx, eventID)
	if err != nil {
		return nil, "", err
	}
	return brand.Logo, brand.LogoContentType, nil
}

func (u *EventUseCase) ListEventInAppTemplates(ctx context.Context, eventID uuid.UUID, filter inAppModel.ListFilter) ([]EventInAppTemplateView, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return nil, err
	}
	eventRows, err := u.inAppTemplates.List(ctx, filter.Type, "", &eventID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list event in-app templates").Err()
	}
	platformRows, err := u.inAppTemplates.List(ctx, filter.Type, notificationModel.TemplateStatusPublished, nil)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list platform in-app templates").Err()
	}
	status := notificationModel.TemplateStatus(filter.Status)
	return resolveEventTemplates(eventRows, platformRows, filter.Type,
		func(t inAppModel.InAppTemplate) string { return t.NotificationType },
		func(t inAppModel.InAppTemplate) bool { return status == "" || t.Status == status },
		func(t inAppModel.InAppTemplate, source string) EventInAppTemplateView {
			return EventInAppTemplateView{InAppTemplate: t, Source: source}
		}), nil
}

func (u *EventUseCase) CreateEventInAppTemplate(ctx context.Context, eventID uuid.UUID, in inAppModel.CreateTemplateInput) (inAppModel.InAppTemplate, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	if !notificationTypes.IsEventScoped(notificationTypes.NotificationType(in.NotificationType)) {
		return inAppModel.InAppTemplate{}, notificationModel.ErrTemplateTypeNotEventScoped.Err()
	}
	if err := validateEventInAppTemplate(notificationTypes.NotificationType(in.NotificationType), in.Title, in.Body, in.Link); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	in.ScopeEventID = &eventID
	created, err := u.inAppTemplates.CreateDraft(ctx, inAppModel.NewDraft(in))
	if err != nil {
		return inAppModel.InAppTemplate{}, classifyTemplateWriteError(err, "create event in-app template")
	}
	return created, nil
}

// GetEventInAppTemplate returns an Event-owned row or the platform published
// row of an Event-scoped type (read-only inheritance view).
func (u *EventUseCase) GetEventInAppTemplate(ctx context.Context, eventID, templateID uuid.UUID) (EventInAppTemplateView, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return EventInAppTemplateView{}, err
	}
	template, err := u.inAppTemplates.GetByID(ctx, templateID)
	if err != nil {
		return EventInAppTemplateView{}, classifyTemplateReadError(err)
	}
	source, ok := eventTemplateSource(template.ScopeEventID, template.NotificationType, template.Status, eventID)
	if !ok {
		return EventInAppTemplateView{}, notificationModel.ErrTemplateNotFound.Err()
	}
	return EventInAppTemplateView{InAppTemplate: template, Source: source}, nil
}

// ownedEventInAppTemplate is used by every mutation: only rows owned by this
// Event may be changed through the Event API.
func (u *EventUseCase) ownedEventInAppTemplate(ctx context.Context, eventID, templateID uuid.UUID) (inAppModel.InAppTemplate, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	template, err := u.inAppTemplates.GetByID(ctx, templateID)
	if err != nil {
		return inAppModel.InAppTemplate{}, classifyTemplateReadError(err)
	}
	if !isEventScope(template.ScopeEventID, eventID) {
		return inAppModel.InAppTemplate{}, notificationModel.ErrTemplateNotFound.Err()
	}
	return template, nil
}

// CustomizeEventInAppTemplate overrides an inherited type for this Event: the
// platform published version is copied into a new Event draft. A type the
// Event already overrides (any Event row) is rejected with
// ErrTemplateDraftExists; a concurrent customize that slips past that check
// hits the one-draft-per-(type, scope) unique index, which
// classifyTemplateWriteError maps to the same 409.
func (u *EventUseCase) CustomizeEventInAppTemplate(ctx context.Context, eventID, platformTemplateID, updatedBy uuid.UUID) (inAppModel.InAppTemplate, error) {
	view, err := u.GetEventInAppTemplate(ctx, eventID, platformTemplateID)
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	if view.Source != TemplateSourcePlatform {
		return inAppModel.InAppTemplate{}, notificationModel.ErrTemplateNotFound.Err()
	}
	source := view.InAppTemplate
	if err := u.ensureInAppTypeNotOverridden(ctx, eventID, source.NotificationType); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	created, err := u.inAppTemplates.CreateDraft(ctx, inAppModel.NewDraft(inAppModel.CreateTemplateInput{
		ScopeEventID:     &eventID,
		NotificationType: source.NotificationType,
		Title:            source.Title,
		Body:             source.Body,
		Link:             source.Link,
		Icon:             source.Icon,
		Tone:             source.Tone,
		AccentColor:      source.AccentColor,
		Surface:          source.Surface,
		AutoDismissMs:    source.AutoDismissMs,
		Actions:          source.Actions,
		Dismissible:      source.Dismissible,
		UpdatedBy:        updatedBy,
	}))
	if err != nil {
		return inAppModel.InAppTemplate{}, classifyTemplateWriteError(err, "customize event in-app template")
	}
	return created, nil
}

func (u *EventUseCase) UpdateEventInAppTemplate(ctx context.Context, eventID uuid.UUID, in inAppModel.UpdateTemplateInput) (inAppModel.InAppTemplate, error) {
	template, err := u.ownedEventInAppTemplate(ctx, eventID, in.ID)
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	if err := validateEventInAppTemplate(notificationTypes.NotificationType(template.NotificationType), in.Title, in.Body, in.Link); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	updated, err := u.inAppTemplates.UpdateDraft(ctx, in)
	if err != nil {
		return inAppModel.InAppTemplate{}, classifyTemplateWriteError(err, "update event in-app template")
	}
	return updated, nil
}

func (u *EventUseCase) DeleteEventInAppTemplate(ctx context.Context, eventID, templateID uuid.UUID) error {
	if _, err := u.ownedEventInAppTemplate(ctx, eventID, templateID); err != nil {
		return err
	}
	affected, err := u.inAppTemplates.DeleteDraft(ctx, templateID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete event in-app template").Err()
	}
	if affected == 0 {
		return notificationModel.ErrTemplateNotFound.Err()
	}
	return nil
}

// ResetEventInAppTemplateType is the in-app counterpart of
// ResetEventEmailTemplateType.
func (u *EventUseCase) ResetEventInAppTemplateType(ctx context.Context, eventID uuid.UUID, notificationType string) error {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return err
	}
	if !notificationTypes.IsEventScoped(notificationTypes.NotificationType(notificationType)) {
		return notificationModel.ErrTemplateTypeNotEventScoped.Err()
	}
	if _, err := u.inAppTemplates.DeleteEventType(ctx, eventID, notificationType); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reset event in-app template type").Err()
	}
	return nil
}

func (u *EventUseCase) PublishEventInAppTemplate(ctx context.Context, eventID, templateID, updatedBy uuid.UUID) (inAppModel.InAppTemplate, error) {
	template, err := u.ownedEventInAppTemplate(ctx, eventID, templateID)
	if err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	if err := validateEventInAppTemplate(notificationTypes.NotificationType(template.NotificationType), template.Title, template.Body, template.Link); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	published, err := u.inAppTemplates.Publish(ctx, templateID, updatedBy)
	if err != nil {
		return inAppModel.InAppTemplate{}, classifyTemplateWriteError(err, "publish event in-app template")
	}
	return published, nil
}

func (u *EventUseCase) RollbackEventInAppTemplate(ctx context.Context, eventID, sourceID, updatedBy uuid.UUID) (inAppModel.InAppTemplate, error) {
	if _, err := u.ownedEventInAppTemplate(ctx, eventID, sourceID); err != nil {
		return inAppModel.InAppTemplate{}, err
	}
	draft, err := u.inAppTemplates.Rollback(ctx, sourceID, updatedBy)
	if err != nil {
		return inAppModel.InAppTemplate{}, classifyTemplateWriteError(err, "rollback event in-app template")
	}
	return draft, nil
}

// ensureEmailTypeNotOverridden rejects customizing a type the Event already
// overrides: its own version family, not platform content, is the base then.
func (u *EventUseCase) ensureEmailTypeNotOverridden(ctx context.Context, eventID uuid.UUID, notificationType string) error {
	own, err := u.emailTemplates.List(ctx, notificationType, "", &eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event email templates").Err()
	}
	if len(own) > 0 {
		return notificationModel.ErrTemplateDraftExists.Err()
	}
	return nil
}

// ensureInAppTypeNotOverridden is the in-app counterpart of
// ensureEmailTypeNotOverridden.
func (u *EventUseCase) ensureInAppTypeNotOverridden(ctx context.Context, eventID uuid.UUID, notificationType string) error {
	own, err := u.inAppTemplates.List(ctx, notificationType, "", &eventID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list event in-app templates").Err()
	}
	if len(own) > 0 {
		return notificationModel.ErrTemplateDraftExists.Err()
	}
	return nil
}

func (u *EventUseCase) ensureEvent(ctx context.Context, eventID uuid.UUID) error {
	if _, err := u.events.GetByID(ctx, eventID); err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return eventModel.ErrEventNotFound.Err()
		}
		return model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	return nil
}

func isEventScope(scope *uuid.UUID, eventID uuid.UUID) bool {
	return scope != nil && *scope == eventID
}

// eventTemplateSource decides whether a template row is visible to an Event:
// its own rows (event), or the platform published version of an Event-scoped
// type (platform). Other Events' rows, platform drafts/history and
// platform-only types are invisible.
func eventTemplateSource(scope *uuid.UUID, notificationType string, status notificationModel.TemplateStatus, eventID uuid.UUID) (string, bool) {
	if isEventScope(scope, eventID) {
		return TemplateSourceEvent, true
	}
	if scope == nil && status == notificationModel.TemplateStatusPublished &&
		notificationTypes.IsEventScoped(notificationTypes.NotificationType(notificationType)) {
		return TemplateSourcePlatform, true
	}
	return "", false
}

// resolveEventTemplates builds the Event's effective template list: one group
// per Event-scoped type (in EventScopedTypes order, narrowed by typeFilter) —
// the Event's own rows when the type is overridden (keep narrows them, e.g. by
// status), otherwise the platform published rows.
func resolveEventTemplates[T, V any](
	eventRows, platformRows []T,
	typeFilter string,
	typeOf func(T) string,
	keep func(T) bool,
	view func(T, string) V,
) []V {
	eventByType := make(map[string][]T)
	for _, row := range eventRows {
		eventByType[typeOf(row)] = append(eventByType[typeOf(row)], row)
	}
	platformByType := make(map[string][]T)
	for _, row := range platformRows {
		platformByType[typeOf(row)] = append(platformByType[typeOf(row)], row)
	}
	out := make([]V, 0)
	for _, typ := range notificationTypes.EventScopedTypes() {
		name := string(typ)
		if typeFilter != "" && typeFilter != name {
			continue
		}
		if own, ok := eventByType[name]; ok {
			for _, row := range own {
				if keep(row) {
					out = append(out, view(row, TemplateSourceEvent))
				}
			}
			continue
		}
		for _, row := range platformByType[name] {
			out = append(out, view(row, TemplateSourcePlatform))
		}
	}
	return out
}

func classifyTemplateReadError(err error) error {
	if repositoryTools.IsObjectNotFoundError(err) {
		return notificationModel.ErrTemplateNotFound.Err()
	}
	return model.ErrPlatform.WithError(err).WithMessage("Failed to get event notification template").Err()
}

func classifyTemplateWriteError(err error, message string) error {
	if repositoryTools.IsObjectNotFoundError(err) {
		return notificationModel.ErrTemplateNotFound.Err()
	}
	if creator, ok := repositoryTools.UniqueViolationError(err, notificationModel.ErrTemplateDraftExists); ok {
		return creator.WithError(err).Err()
	}
	return model.ErrPlatform.WithError(err).WithMessage(message).Err()
}

func validateEventEmailTemplate(typ notificationTypes.NotificationType, subject, preheader string, body, styling []byte) error {
	if err := notificationTypes.ValidateTemplateVariables(typ, notificationTypes.NotificationChannelEmail, subject, preheader); err != nil {
		return notificationModel.ErrInvalidTemplateVariables.WithError(err).Err()
	}
	if err := notificationTypes.ValidateEmailBodyVariables(typ, body); err != nil {
		return notificationModel.ErrInvalidTemplateVariables.WithError(err).Err()
	}
	return render.ValidateStyling(styling)
}

func validateEventInAppTemplate(typ notificationTypes.NotificationType, title, body, link string) error {
	if err := notificationTypes.ValidateTemplateVariables(typ, notificationTypes.NotificationChannelInApp, title, body, link); err != nil {
		return notificationModel.ErrInvalidTemplateVariables.WithError(err).Err()
	}
	return nil
}

// SendEventEmailTemplateTest queues one email of templateID (an Event row or
// the inherited platform row) to the caller with sample variables and the
// real Event name, tag and link (M8). It goes through the normal dispatcher,
// so the Event sender, Event SMTP and journal apply as for participants.
func (u *EventUseCase) SendEventEmailTemplateTest(ctx context.Context, eventID, templateID, userID uuid.UUID) (string, error) {
	view, err := u.GetEventEmailTemplate(ctx, eventID, templateID)
	if err != nil {
		return "", err
	}
	if u.invitationNotifier == nil {
		return "", model.ErrPlatform.WithMessage("Notification dispatcher is not configured").Err()
	}
	e, err := u.events.GetByID(ctx, eventID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get event").Err()
	}
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to get current user").Err()
	}
	typ := notificationTypes.NotificationType(view.NotificationType)
	vars := map[string]any{}
	for _, d := range notificationTypes.Descriptors(typ) {
		vars[d.Name] = d.Default
	}
	for key, value := range map[string]any{
		"scope_event_id": e.ID.String(), "event_name": e.Name, "event_tag": e.Tag,
		"event_url": fmt.Sprintf("https://%s.%s/", e.Tag, u.eventDomain),
		"user_id":   user.ID.String(), "user_email": user.Email, "user_first_name": user.FirstName,
		"user_last_name": user.LastName, "user_name": user.FullName(), "user_picture": user.Picture,
	} {
		vars[key] = value
	}
	payload := notificationPayloads.DefaultPayload{
		Type: typ, Variables: vars,
		Channels: []notificationTypes.NotificationChannel{notificationTypes.NotificationChannelEmail},
	}
	if err = u.invitationNotifier.Notify(ctx, userID, payload,
		dispatchModel.WithEventScope(eventID), dispatchModel.WithTemplateID(templateID),
		dispatchModel.WithOverrideChannels(notificationTypes.NotificationChannelEmail)); err != nil {
		return "", model.ErrPlatform.WithError(err).WithMessage("Failed to queue test email").Err()
	}
	return user.Email, nil
}

// ListEventEmailPresets lists the shared block presets an Event's email can
// use. Presets belong to the platform: an Event only reads them.
func (u *EventUseCase) ListEventEmailPresets(ctx context.Context, eventID uuid.UUID) ([]emailModel.BlockPreset, error) {
	if err := u.ensureEvent(ctx, eventID); err != nil {
		return nil, err
	}
	presets, err := u.emailTemplates.ListPresets(ctx)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list block presets").Err()
	}
	return presets, nil
}
