package emailUseCase

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/gofrs/uuid"

	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	emailModel "github.com/cybericebox/daemon/internal/model/notification/email"
	"github.com/cybericebox/daemon/internal/useCase/notification/channels/render"
	appErr "github.com/cybericebox/daemon/pkg/err"
)

// TemplateMedia is the narrow media port of the email template use cases
// (platform and Event): image upload/stream, file lookups for validation and
// ownership references so the media GC keeps referenced images.
type TemplateMedia interface {
	UploadFile(ctx context.Context, name, contentType string, r io.Reader, createdBy uuid.UUID) (mediaModel.File, error)
	StreamFile(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error)
	GetFile(ctx context.Context, id uuid.UUID) (mediaModel.File, error)
	ReplaceReferences(ctx context.Context, refType string, refID uuid.UUID, fileIDs []uuid.UUID) error
	AddReference(ctx context.Context, refType string, refID, fileID uuid.UUID) error
	RemoveReferences(ctx context.Context, refType string, refID uuid.UUID) error
}

// EventImageSource decides which files an Event's email templates may use:
// those already referenced by a platform template (scope NULL), a block preset
// or one of THIS Event's own template rows (*emailTemplateRepo.Repository
// satisfies it).
type EventImageSource interface {
	FileUsableByEvent(ctx context.Context, fileID, eventID uuid.UUID) (bool, error)
}

// PresetSource loads a block preset (*emailTemplateRepo.Repository satisfies it).
type PresetSource interface {
	GetPreset(ctx context.Context, id uuid.UUID) (emailModel.BlockPreset, error)
}

// TemplateImages is the image policy shared by the platform and Event email
// template use cases and the block preset use case: write-time file_id
// validation, publish-time inline size check and reference bookkeeping. Every
// template row / preset owns references to the files its OWN blocks use;
// presets are separate rows with their own references, but the publish-time
// size check follows preset blocks because dispatch inlines their images too.
type TemplateImages struct {
	media   TemplateMedia
	presets PresetSource
}

func NewTemplateImages(media TemplateMedia, presets PresetSource) *TemplateImages {
	return &TemplateImages{media: media, presets: presets}
}

// UploadForTemplate gives a new image to one event-owned draft immediately.
// This lets the draft preview and the following save use the image without
// exposing it to another event. SyncReferences removes unused uploads on save.
func (t *TemplateImages) UploadForTemplate(ctx context.Context, templateID uuid.UUID, r io.Reader, name string, userID uuid.UUID) (mediaModel.File, error) {
	data, contentType, err := ProcessTemplateImage(r)
	if err != nil {
		return mediaModel.File{}, err
	}
	if name == "" {
		name = templateImageName
	}
	file, err := t.media.UploadFile(ctx, name, contentType, bytes.NewReader(data), userID)
	if err != nil {
		return mediaModel.File{}, err
	}
	if err := t.media.AddReference(ctx, mediaModel.RefTypeEmailTemplate, templateID, file.ID); err != nil {
		return mediaModel.File{}, err
	}
	return file, nil
}

// ValidateBody requires every image block file_id to be the UUID of an
// existing uploaded PNG/JPEG file — the only types the upload pipeline
// produces (ErrTemplateImageInvalid otherwise).
func (t *TemplateImages) ValidateBody(ctx context.Context, body json.RawMessage) error {
	refs, err := render.ScanImages(body)
	if err != nil {
		return templateImageInvalid(err)
	}
	if len(refs.Invalid) > 0 {
		return templateImageInvalid(fmt.Errorf("image file_id %q is not a file id", refs.Invalid[0]))
	}
	_, err = t.fileSizes(ctx, refs.FileIDs)
	return err
}

// CheckInlineSize rejects a body whose inline parts — the uploaded images of
// its own blocks and of the presets it references (transitively), plus the
// logo when any of them has a logo block — exceed MaxInlineBytes
// (ErrTemplateInlineTooLarge). Each file counts once, as dispatch attaches it
// once. Run at publish time.
func (t *TemplateImages) CheckInlineSize(ctx context.Context, body json.RawMessage, logoBytes ...int64) error {
	fileIDs, hasLogo, err := t.inlineAssets(ctx, body)
	if err != nil {
		return err
	}
	total, err := t.fileSizes(ctx, fileIDs)
	if err != nil {
		return err
	}
	if hasLogo {
		if len(logoBytes) > 0 {
			total += logoBytes[0]
		} else {
			total += int64(len(branding.LogoPNG()))
		}
	}
	if total > MaxInlineBytes {
		return templateInlineTooLarge(fmt.Errorf("inline images total %d bytes, limit %d", total, MaxInlineBytes))
	}
	return nil
}

// inlineAssets collects the unique uploaded files and logo usage of body and of
// every preset it reaches. Unknown or unparsable preset ids are skipped, as
// RenderEmail renders them as nothing; cycles are visited once.
func (t *TemplateImages) inlineAssets(ctx context.Context, body json.RawMessage) ([]uuid.UUID, bool, error) {
	var (
		fileIDs []uuid.UUID
		hasLogo bool
	)
	seenFile := make(map[uuid.UUID]bool)
	seenPreset := make(map[uuid.UUID]bool)
	queue := []json.RawMessage{body}
	for len(queue) > 0 {
		blocks := queue[0]
		queue = queue[1:]
		refs, err := render.ScanImages(blocks)
		if err != nil {
			return nil, false, templateImageInvalid(err)
		}
		hasLogo = hasLogo || refs.HasLogo
		for _, id := range refs.FileIDs {
			if !seenFile[id] {
				seenFile[id] = true
				fileIDs = append(fileIDs, id)
			}
		}
		for _, raw := range refs.PresetIDs {
			id, err := uuid.FromString(raw)
			if err != nil || seenPreset[id] {
				continue
			}
			seenPreset[id] = true
			preset, err := t.presets.GetPreset(ctx, id)
			if err != nil {
				if repositoryTools.IsObjectNotFoundError(err) {
					continue
				}
				return nil, false, model.ErrPlatform.WithError(err).WithMessage("Failed to get email block preset").Err()
			}
			queue = append(queue, preset.Blocks)
		}
	}
	return fileIDs, hasLogo, nil
}

// fileSizes sums the sizes of ids; an unknown file or one that is not a
// PNG/JPEG template image is ErrTemplateImageInvalid.
func (t *TemplateImages) fileSizes(ctx context.Context, ids []uuid.UUID) (int64, error) {
	var total int64
	for _, id := range ids {
		f, err := t.media.GetFile(ctx, id)
		if err != nil {
			if mediaModel.ErrFileNotFound.Err().Is(err) {
				return 0, templateImageInvalid(fmt.Errorf("image file %s not found", id))
			}
			return 0, err
		}
		if !isTemplateImageType(f.ContentType) {
			return 0, templateImageInvalid(fmt.Errorf("file %s is %q, not a template image", id, f.ContentType))
		}
		total += f.SizeBytes
	}
	return total, nil
}

// SyncReferences makes template row templateID own exactly the files body uses.
func (t *TemplateImages) SyncReferences(ctx context.Context, templateID uuid.UUID, body json.RawMessage) error {
	return t.media.ReplaceReferences(ctx, mediaModel.RefTypeEmailTemplate, templateID, render.ImageFileIDs(body))
}

// RemoveReferences drops every reference owned by a deleted template row.
func (t *TemplateImages) RemoveReferences(ctx context.Context, templateID uuid.UUID) error {
	return t.media.RemoveReferences(ctx, mediaModel.RefTypeEmailTemplate, templateID)
}

// SyncPresetReferences makes block preset presetID own exactly the files its
// blocks use.
func (t *TemplateImages) SyncPresetReferences(ctx context.Context, presetID uuid.UUID, blocks json.RawMessage) error {
	return t.media.ReplaceReferences(ctx, mediaModel.RefTypeEmailBlockPreset, presetID, render.ImageFileIDs(blocks))
}

// RemovePresetReferences drops every reference owned by a deleted preset.
func (t *TemplateImages) RemovePresetReferences(ctx context.Context, presetID uuid.UUID) error {
	return t.media.RemoveReferences(ctx, mediaModel.RefTypeEmailBlockPreset, presetID)
}

// Stream opens an uploaded template image. Only files of the image types the
// upload pipeline stores are served, so an image route cannot be used to read
// arbitrary media files (e.g. exercise attachments). Caller closes the reader.
func (t *TemplateImages) Stream(ctx context.Context, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	rc, f, err := t.media.StreamFile(ctx, fileID)
	if err != nil {
		return nil, mediaModel.File{}, err
	}
	if !isTemplateImageType(f.ContentType) {
		_ = rc.Close()
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return rc, f, nil
}

// ValidateEventBody is ValidateBody for an Event template write, plus
// ownership: every image file_id the write ADDS (absent from previous, the
// body of the row being updated; nil on create) must be usable by eventID.
// Without it an Event manager could embed — and have the Event image route
// and emails serve — any uploaded PNG/JPEG (another user's avatar, an
// exercise image, another Event's template image) by guessing its id.
func (t *TemplateImages) ValidateEventBody(ctx context.Context, source EventImageSource, eventID uuid.UUID, body, previous json.RawMessage) error {
	if err := t.ValidateBody(ctx, body); err != nil {
		return err
	}
	had := make(map[uuid.UUID]bool)
	for _, id := range render.ImageFileIDs(previous) {
		had[id] = true
	}
	for _, id := range render.ImageFileIDs(body) {
		if had[id] {
			continue
		}
		ok, err := source.FileUsableByEvent(ctx, id, eventID)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to check event email template image").Err()
		}
		if !ok {
			return templateImageInvalid(fmt.Errorf("image file %s is not available to event %s", id, eventID))
		}
	}
	return nil
}

// ValidateBroadcastBody is the image policy of a broadcast body, whose files must belong to the broadcast's
// scope: an event broadcast may embed what an event template may (ValidateEventBody: platform template and
// preset images, the event's own), a platform broadcast the platform templates' and presets' images and the
// sender's own uploads. Every file must be an uploaded PNG/JPEG. Without it a manager could mail themselves
// any media file (an avatar, an answer file, another event's image) by its id.
func (t *TemplateImages) ValidateBroadcastBody(ctx context.Context, source EventImageSource, scopeEventID *uuid.UUID, actor uuid.UUID, body json.RawMessage) error {
	if scopeEventID != nil {
		return t.ValidateEventBody(ctx, source, *scopeEventID, body, nil)
	}
	if err := t.ValidateBody(ctx, body); err != nil {
		return err
	}
	for _, id := range render.ImageFileIDs(body) {
		// The nil event matches only platform templates and presets.
		ok, err := source.FileUsableByEvent(ctx, id, uuid.Nil)
		if err != nil {
			return model.ErrPlatform.WithError(err).WithMessage("Failed to check broadcast image").Err()
		}
		if ok {
			continue
		}
		f, err := t.media.GetFile(ctx, id)
		if err != nil {
			return err
		}
		if !f.CreatedBy.Valid || f.CreatedBy.UUID != actor {
			return templateImageInvalid(fmt.Errorf("image file %s is not available to this broadcast", id))
		}
	}
	return nil
}

// BroadcastImages is the image policy of broadcast bodies over one image source (*emailTemplateRepo.Repository
// satisfies both ports).
type BroadcastImages struct {
	images *TemplateImages
	source EventImageSource
}

func NewBroadcastImages(media TemplateMedia, presets PresetSource, source EventImageSource) *BroadcastImages {
	return &BroadcastImages{images: NewTemplateImages(media, presets), source: source}
}

func (b *BroadcastImages) ValidateBroadcastBody(ctx context.Context, scopeEventID *uuid.UUID, actor uuid.UUID, body json.RawMessage) error {
	return b.images.ValidateBroadcastBody(ctx, b.source, scopeEventID, actor, body)
}

// StreamForEvent is Stream restricted to files usable by eventID's templates
// (ErrFileNotFound otherwise): the Event image route serves saved platform,
// preset and own-Event template images only — never fresh uploads, other
// Events' images or unrelated media.
func (t *TemplateImages) StreamForEvent(ctx context.Context, source EventImageSource, eventID, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	ok, err := source.FileUsableByEvent(ctx, fileID, eventID)
	if err != nil {
		return nil, mediaModel.File{}, model.ErrPlatform.WithError(err).WithMessage("Failed to check event email template image").Err()
	}
	if !ok {
		return nil, mediaModel.File{}, mediaModel.ErrFileNotFound.Err()
	}
	return t.Stream(ctx, fileID)
}

// previewFile reads an uploaded file for a preview's inline data: URI. Preview
// input is user-typed, so a file that is missing, not a PNG/JPEG template image
// or — with a scope — not usable by the Event (the ValidateEventBody ownership
// rule) is ErrTemplateImageInvalid; lookup and read failures stay platform
// errors.
func (t *TemplateImages) previewFile(ctx context.Context, scope *EventPreviewScope, id uuid.UUID) ([]byte, string, error) {
	if t == nil {
		return nil, "", model.ErrPlatform.WithMessage("Template images are not configured").Err()
	}
	if scope != nil {
		ok, err := scope.Source.FileUsableByEvent(ctx, id, scope.EventID)
		if err != nil {
			return nil, "", model.ErrPlatform.WithError(err).WithMessage("Failed to check event email template image").Err()
		}
		if !ok {
			return nil, "", templateImageInvalid(fmt.Errorf("image file %s is not available to event %s", id, scope.EventID))
		}
	}
	data, f, err := readInlineFile(ctx, t.media, id)
	if err != nil {
		if mediaModel.ErrFileNotFound.Err().Is(err) {
			return nil, "", templateImageInvalid(fmt.Errorf("image file %s not found", id))
		}
		var ae appErr.Error
		if errors.As(err, &ae) {
			return nil, "", err
		}
		return nil, "", model.ErrPlatform.WithError(err).WithMessage("Failed to load template image").Err()
	}
	if !isTemplateImageType(f.ContentType) {
		return nil, "", templateImageInvalid(fmt.Errorf("file %s is %q, not a template image", id, f.ContentType))
	}
	return data, f.ContentType, nil
}
