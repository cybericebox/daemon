// Package mediaModel is the DOMAIN layer of the media subsystem:
// content-addressed files (dedup by sha256) with refcounted ownership links.
package mediaModel

import (
	"fmt"
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

// ChunkKey returns the S3 key of one received chunk of a resumable upload.
func ChunkKey(uploadID uuid.UUID, index int) string {
	return fmt.Sprintf("media/uploads/%s/%d", uploadID, index)
}

// Upload is a resumable chunked upload in progress: a file sent in order, in chunks of ChunkBytes (the last one
// is shorter), through the API. Chunks are stored as temporary objects and assembled, size- and hash-checked when
// the last one has arrived.
type Upload struct {
	ID             uuid.UUID
	CreatedBy      uuid.UUID
	Name           string
	ContentType    string
	SizeBytes      int64
	ChunkBytes     int64
	ChunksReceived int
	CreatedAt      time.Time
	ExpiresAt      time.Time
}

// NewUpload opens an upload. ttl is how long it waits for the next chunk.
func NewUpload(id, createdBy uuid.UUID, name, contentType string, size, chunkBytes int64, ttl time.Duration, now time.Time) Upload {
	return Upload{
		ID: id, CreatedBy: createdBy, Name: name, ContentType: contentType, SizeBytes: size, ChunkBytes: chunkBytes,
		CreatedAt: now, ExpiresAt: now.Add(ttl),
	}
}

// ChunkCount is how many chunks the whole file takes.
func (u Upload) ChunkCount() int {
	return int((u.SizeBytes + u.ChunkBytes - 1) / u.ChunkBytes)
}

// ChunkSize is the exact size chunk index must have: ChunkBytes, or what is left for the last one.
func (u Upload) ChunkSize(index int) int64 {
	if index == u.ChunkCount()-1 {
		return u.SizeBytes - int64(index)*u.ChunkBytes
	}
	return u.ChunkBytes
}

// Complete reports whether every chunk has arrived.
func (u Upload) Complete() bool { return u.ChunksReceived >= u.ChunkCount() }

// ReceivedBytes is how much of the file is stored: where a resumed upload continues.
func (u Upload) ReceivedBytes() int64 {
	if u.Complete() {
		return u.SizeBytes
	}
	return int64(u.ChunksReceived) * u.ChunkBytes
}

// Expired reports whether the upload waited too long for its next chunk.
func (u Upload) Expired(now time.Time) bool { return !now.Before(u.ExpiresAt) }

// OwnedBy reports whether the upload was started by userID.
func (u Upload) OwnedBy(userID uuid.UUID) bool { return u.CreatedBy == userID }
