// Package mediaModel is the DOMAIN layer of the media subsystem:
// content-addressed files (dedup by sha256) with refcounted ownership links.
package mediaModel

import (
	"time"

	"github.com/gofrs/uuid"
)

// RefTypeExerciseVersion marks a reference owned by an exercise version
// (draft or historical) — the only consumer in v1.
const RefTypeExerciseVersion = "exercise_version"

// RefTypeUserAvatar marks a reference owned by a user's profile avatar.
// ReplaceReferences is always called with at most one fileID (or none),
// keeping the reference set a singleton per user.
const RefTypeUserAvatar = "user_avatar"

// RefTypeEventLogo is the single current logo for an event site.
const RefTypeEventLogo = "event_logo"

// RefTypeEventFavicon is the single current browser icon for an event site.
const RefTypeEventFavicon = "event_favicon"

// RefTypeEventPreviewPicture is the current image used in an event's public preview.
const RefTypeEventPreviewPicture = "event_preview_picture"

// RefTypeEventContentImage identifies images uploaded for this event's page banners.
const RefTypeEventContentImage = "event_content_image"

// RefTypeEmailTemplate marks a reference owned by a notification email
// template row (any status): the uploaded images its image blocks use.
const RefTypeEmailTemplate = "notification_email_template"

// RefTypeEmailBlockPreset marks a reference owned by a notification email
// block preset: the uploaded images its image blocks use.
const RefTypeEmailBlockPreset = "notification_email_block_preset"

// File is a logical uploaded file. The S3 object lives under
// media/blobs/<ContentHash>; several File rows may share one blob.
type File struct {
	ID          uuid.UUID
	Name        string
	ContentType string
	SizeBytes   int64
	ContentHash string

	CreatedAt time.Time
	CreatedBy uuid.NullUUID
}

// BlobKey returns the S3 object key for a content hash.
func BlobKey(contentHash string) string {
	return "media/blobs/" + contentHash
}

// TmpKey returns the S3 key for an in-flight upload.
func TmpKey(id uuid.UUID) string {
	return "media/tmp/" + id.String()
}
