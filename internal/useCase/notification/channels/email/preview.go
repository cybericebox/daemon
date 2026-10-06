package emailUseCase

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

// PreviewInput is an email template draft to render (it need not be saved).
type PreviewInput struct {
	NotificationType   string
	Subject, Preheader string
	Body, Styling      json.RawMessage
	// Values are sample variable values; a variable missing here takes the
	// notification type's default (the `default` tag of its payload).
	Values map[string]string
}

// PreviewOutput is the rendered draft. HTML equals the dispatched HTML, footer
// included, except that every inline image is embedded as a data: URI instead
// of a cid: part, so the preview needs no image requests (and no cookies) to
// display.
type PreviewOutput struct{ Subject, Preheader, HTML string }

// FooterSource builds the standard footer the mail use case appends on send
// for mail routed as eventID (nil: the platform); *mailUseCase.MailUseCase
// satisfies it. Previews take the footer from it so they cannot drift from
// what is sent.
type FooterSource interface {
	Footer(ctx context.Context, eventID *uuid.UUID) (mailModel.Footer, error)
}

// PresetLister loads every block preset (*emailTemplateRepo.Repository
// satisfies it).
type PresetLister interface {
	ListPresets(ctx context.Context) ([]emailModel.BlockPreset, error)
}

// EventPreviewScope scopes a preview to an Event: the Event's brand, and only
// images usable by that Event (Source) may be embedded.
type EventPreviewScope struct {
	EventID uuid.UUID
	Source  EventImageSource
	Brands  EventBrandResolver
}

// PreviewEmail renders a platform template draft with the platform brand.
// Nothing is persisted; any uploaded PNG/JPEG may be embedded (the platform
// template write rule), whether referenced yet or not.
func (u *NotificationEmailTemplateUseCase) PreviewEmail(ctx context.Context, in PreviewInput) (PreviewOutput, error) {
	return Preview(ctx, u.templates, u.images, u.footers, nil, in)
}

// SetFooterSource wires the footer builder of the mail use case (composition
// root; it is built after this use case's dependencies).
func (u *NotificationEmailTemplateUseCase) SetFooterSource(footers FooterSource) {
	u.footers = footers
}

// Preview renders a template draft through the dispatch composition
// (composeEmail, cid: sources) with the brand of scope (nil: platform) and
// sample variables, loads the inline parts like dispatch does and embeds each
// as a data: URI in place of its cid: source, then appends the footer of
// footers for the route dispatch would take (nil footers: none, for tests of
// the body alone). Shared by the platform and Event
// preview endpoints. Invalid styling → ErrInvalidTemplateStyling; a type
// without the email channel → ErrInvalidTemplateVariables; a draft the renderer
// rejects (template syntax, a subject/preheader variable without a value,
// malformed blocks) → ErrTemplatePreviewInvalid; an image that is missing, not
// a PNG/JPEG or (with a scope) not usable by the Event →
// ErrTemplateImageInvalid; inline images over MaxInlineBytes →
// ErrTemplateInlineTooLarge. Preset and media load failures stay platform
// errors. images may be nil for a draft without uploaded images.
func Preview(ctx context.Context, presets PresetLister, images *TemplateImages, footers FooterSource, scope *EventPreviewScope, in PreviewInput) (PreviewOutput, error) {
	typ := notificationTypes.NotificationType(in.NotificationType)
	if !notificationTypes.Supports(typ, notificationTypes.NotificationChannelEmail) {
		return PreviewOutput{}, notificationModel.ErrInvalidTemplateVariables.
			WithError(fmt.Errorf("notification type %q does not support channel %q", typ, notificationTypes.NotificationChannelEmail)).Err()
	}
	if err := render.ValidateStyling(in.Styling); err != nil {
		return PreviewOutput{}, err
	}
	presetList, err := presets.ListPresets(ctx)
	if err != nil {
		return PreviewOutput{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load block presets").Err()
	}
	var scopeEventID *uuid.UUID
	if scope != nil {
		scopeEventID = &scope.EventID
	}
	var resolver EventBrandResolver
	if scope != nil {
		resolver = scope.Brands
	}
	brand, err := resolveEmailBrand(ctx, typ, scopeEventID, resolver)
	if err != nil {
		return PreviewOutput{}, err
	}
	composed, err := composeEmail(emailModel.EmailTemplate{
		NotificationType: in.NotificationType,
		Subject:          in.Subject,
		Preheader:        in.Preheader,
		Body:             in.Body,
		Styling:          in.Styling,
	}, presetMap(presetList), SampleVars(typ, in.Values), render.RenderContext{
		Brand:    brand.Colors,
		LogoAlt:  brand.LogoAlt,
		AssetSrc: cidSrc,
	})
	if err != nil {
		// composeEmail does no IO (presets are already loaded), so every
		// failure here comes from the draft itself.
		return PreviewOutput{}, previewInvalid(err)
	}
	parts, err := loadInlineParts(ctx, composed.Assets, brand, func(ctx context.Context, id uuid.UUID) ([]byte, string, error) {
		return images.previewFile(ctx, scope, id)
	})
	if errors.Is(err, errInlineTooLarge) {
		return PreviewOutput{}, templateInlineTooLarge(err)
	}
	if err != nil {
		return PreviewOutput{}, err
	}
	html := composed.HTML
	for _, part := range parts {
		html = strings.ReplaceAll(html,
			`src="cid:`+part.ContentID+`"`,
			`src="data:`+part.ContentType+`;base64,`+base64.StdEncoding.EncodeToString(part.Data)+`"`)
	}
	if footers != nil {
		// The route rule of Handle: only participant mail goes out as the Event.
		var route *uuid.UUID
		if scopeEventID != nil && notificationTypes.IsEventScoped(typ) {
			route = scopeEventID
		}
		footer, err := footers.Footer(ctx, route)
		if err != nil {
			return PreviewOutput{}, err
		}
		html, _ = footer.Append(html, "")
	}
	return PreviewOutput{Subject: composed.Subject, Preheader: composed.Preheader, HTML: html}, nil
}

// previewInvalid turns a render failure into the 4xx ErrTemplatePreviewInvalid
// whose message carries the renderer's reason (e.g. the text/template parse
// error), which the editor can show as is. Platform error wrappers are peeled
// off (their messages are generic, their Error() carries file positions); a
// plain error chain is kept whole.
func previewInvalid(cause error) error {
	reason := cause
	for {
		if _, isAppErr := reason.(appErr.Error); !isAppErr {
			break
		}
		next := errors.Unwrap(reason)
		if next == nil {
			break
		}
		reason = next
	}
	msg := reason.Error()
	if e, isAppErr := reason.(appErr.Error); isAppErr {
		msg = e.StatusCode().Message()
	}
	return notificationModel.ErrTemplatePreviewInvalid.WithError(cause).
		WithMessage("Template cannot be rendered: " + msg).Err()
}

// SampleVars returns the preview variables of typ: the type's variable
// defaults (the same values the admin editor shows) overlaid by values.
func SampleVars(typ notificationTypes.NotificationType, values map[string]string) map[string]any {
	descriptors := notificationTypes.Descriptors(typ)
	vars := make(map[string]any, len(descriptors)+len(values))
	for _, d := range descriptors {
		vars[d.Name] = d.Default
	}
	for k, v := range values {
		vars[k] = v
	}
	return vars
}
