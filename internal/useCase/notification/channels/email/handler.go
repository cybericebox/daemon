package emailUseCase

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/emailTemplateRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	mailModel "github.com/cybericebox/daemon/internal/model/mail"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/notification"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	"github.com/cybericebox/daemon/internal/model/notification/types"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
	"github.com/cybericebox/daemon/pkg/email"
)

// MaxInlineBytes caps the total size of the inline images of one email.
const MaxInlineBytes = 1 << 20

// mailer is the email handler's own send port; the concrete mailer (the mail
// use case) is injected from the aggregator (composition root). eventID is
// set only for participant mail of an Event: it picks the Event sender and
// the Event SMTP; nil sends as the platform.
type mailer interface {
	Deliver(ctx context.Context, eventID *uuid.UUID, msg email.Message) error
}

// mediaReader loads uploaded files referenced by image blocks.
type mediaReader interface {
	StreamFile(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error)
}

type Handler struct {
	templates *emailTemplateRepo.Repository
	mailer    mailer
	media     mediaReader
	brands    EventBrandResolver
}

func NewHandler(repo emailTemplateRepo.Queries, m mailer, media mediaReader, brands ...EventBrandResolver) *Handler {
	h := &Handler{templates: emailTemplateRepo.New(repo), mailer: m, media: media}
	if len(brands) > 0 {
		h.brands = brands[0]
	}
	return h
}

func (h *Handler) Channel() notificationTypes.NotificationChannel {
	return notificationTypes.NotificationChannelEmail
}

func (h *Handler) Handle(
	ctx context.Context,
	user userModel.User,
	t notificationTypes.NotificationType,
	vars map[string]any,
	templateID *uuid.UUID,
	scopeEventID ...*uuid.UUID,
) error {
	var (
		tmpl emailModel.EmailTemplate
		err  error
	)
	if bc, ok := dispatchModel.BroadcastFrom(ctx); ok {
		// A broadcast carries its own content instead of a stored template.
		tmpl = bc.AsEmailTemplate()
	} else {
		if templateID != nil {
			tmpl, err = h.templates.GetByID(ctx, *templateID)
		} else {
			tmpl, err = h.templates.GetPublishedByType(ctx, string(t), firstScope(scopeEventID))
		}
		if err != nil {
			if repositoryTools.IsObjectNotFoundError(err) {
				return notificationModel.ErrTemplateNotFound.Err()
			}
			return model.ErrPlatform.WithError(err).WithMessage("Failed to load email template").Err()
		}
		if templateID != nil && tmpl.NotificationType != string(t) {
			return notificationModel.ErrTemplateNotFound.Err()
		}
	}
	presetList, err := h.templates.ListPresets(ctx)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to load block presets").Err()
	}

	eventID := firstScope(scopeEventID)
	if eventID == nil {
		eventID = tmpl.ScopeEventID
	}
	brand, err := resolveEmailBrand(ctx, t, eventID, h.brands)
	if err != nil {
		return err
	}
	composed, err := composeEmail(tmpl, presetMap(presetList), vars, render.RenderContext{
		Brand:    brand.Colors,
		LogoAlt:  brand.LogoAlt,
		AssetSrc: cidSrc,
	})
	if err != nil {
		return err
	}
	inline, err := h.loadInline(ctx, composed.Assets, brand)
	if err != nil {
		return err
	}
	// Moderator and account mail inside an Event stays platform mail.
	var route *uuid.UUID
	if eventID != nil && notificationTypes.IsEventScoped(t) {
		route = eventID
	}
	return h.mailer.Deliver(ctx, route, email.Message{
		To:      user.Email,
		Subject: composed.Subject,
		HTML:    composed.HTML,
		Text:    composed.Text,
		Inline:  inline,
	})
}

// cidSrc is the dispatch <img src> of an asset: its inline part's Content-ID.
func cidSrc(a render.Asset) string { return "cid:" + render.CID(a) }

// composedEmail is a rendered email: HTML carries the hidden preheader span
// before the body, Text is the plain-text alternative of the body alone.
type composedEmail struct {
	Subject   string
	Preheader string
	HTML      string
	Text      string
	Assets    []render.Asset
}

// composeEmail renders subject, preheader and body of tpl exactly as dispatch
// sends them. Dispatch and preview both use it with cid: sources; preview then
// swaps each cid: source for the data: URI of the same inline part.
func composeEmail(tpl emailModel.EmailTemplate, presets map[string]json.RawMessage, vars map[string]any, rc render.RenderContext) (composedEmail, error) {
	subject, err := render.RenderText(tpl.Subject, vars)
	if err != nil {
		return composedEmail{}, err
	}
	rendered, err := render.RenderEmail(tpl.Body, tpl.Styling, presets, vars, rc)
	if err != nil {
		return composedEmail{}, err
	}
	out := composedEmail{Subject: subject, Text: mailModel.KeepBrandText(render.PlainText(rendered.HTML)), Assets: rendered.Assets}
	if tpl.Preheader != "" {
		pre, err := render.RenderText(tpl.Preheader, vars)
		if err != nil {
			return composedEmail{}, err
		}
		out.Preheader = mailModel.KeepBrandText(pre)
	}
	out.HTML = mailModel.KeepBrandHTML(render.Shell("uk", out.Preheader, rendered.HTML))
	return out, nil
}

// presetMap indexes block presets by id, as RenderEmail expects.
func presetMap(list []emailModel.BlockPreset) map[string]json.RawMessage {
	presets := make(map[string]json.RawMessage, len(list))
	for _, p := range list {
		presets[p.ID.String()] = p.Blocks
	}
	return presets
}

// loadInline loads the bytes of every asset the rendered body references, as
// inline parts whose Content-IDs match the body's cid: sources. The total is
// capped at MaxInlineBytes.
func (h *Handler) loadInline(ctx context.Context, assets []render.Asset, brand Brand) ([]email.InlinePart, error) {
	parts, err := loadInlineParts(ctx, assets, brand, func(ctx context.Context, id uuid.UUID) ([]byte, string, error) {
		data, f, err := readInlineFile(ctx, h.media, id)
		return data, f.ContentType, err
	})
	if errors.Is(err, errInlineTooLarge) {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Inline images exceed email size limit").Err()
	}
	return parts, err
}

// errInlineTooLarge marks an inline image total above MaxInlineBytes; each
// caller maps it to its own error (dispatch: platform, preview: invalid input).
var errInlineTooLarge = errors.New("inline images exceed the per-email size limit")

// inlineFileReader returns the bytes and content type of an uploaded file.
type inlineFileReader func(ctx context.Context, id uuid.UUID) (data []byte, contentType string, err error)

// loadInlineParts is the inline-part loading shared by dispatch and preview:
// the logo is the brand logo, a file is read through readFile, and the total
// is capped at MaxInlineBytes (errInlineTooLarge).
func loadInlineParts(ctx context.Context, assets []render.Asset, brand Brand, readFile inlineFileReader) ([]email.InlinePart, error) {
	parts := make([]email.InlinePart, 0, len(assets))
	total := 0
	for _, a := range assets {
		part := email.InlinePart{ContentID: render.CID(a)}
		if a.Kind == render.AssetLogo {
			part.ContentType, part.Data = brand.LogoContentType, brand.Logo
		} else {
			data, contentType, err := readFile(ctx, a.FileID)
			if err != nil {
				return nil, err
			}
			part.ContentType, part.Data = contentType, data
		}
		total += len(part.Data)
		if total > MaxInlineBytes {
			return nil, fmt.Errorf("%w: %d+ bytes, limit %d", errInlineTooLarge, total, MaxInlineBytes)
		}
		parts = append(parts, part)
	}
	return parts, nil
}

// readInlineFile reads at most MaxInlineBytes+1 bytes of an uploaded file —
// enough for the caller to detect an oversized payload without reading it
// whole. Stream errors are returned as is.
func readInlineFile(ctx context.Context, media mediaReader, id uuid.UUID) ([]byte, mediaModel.File, error) {
	rc, f, err := media.StreamFile(ctx, id)
	if err != nil {
		return nil, mediaModel.File{}, err
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(io.LimitReader(rc, MaxInlineBytes+1))
	if err != nil {
		return nil, mediaModel.File{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read inline image").Err()
	}
	return data, f, nil
}

func firstScope(scopes []*uuid.UUID) *uuid.UUID {
	if len(scopes) == 0 {
		return nil
	}
	return scopes[0]
}
