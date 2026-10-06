package auth

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	userModel "github.com/cybericebox/daemon/internal/model/user"
)

// MaxAvatarBytes caps avatar size (upload and provider download); AVATAR_MAX_BYTES sets it once at start.
var MaxAvatarBytes int64 = 5 << 20 // 5 MiB

// allowedImageTypes is the raster allowlist for avatars. SVG is deliberately
// excluded: an SVG can carry <script>, and since avatars are served from the
// auth subdomain (id.<domain>) that would be stored XSS against the session
// origin. The stored/served Content-Type is the SNIFFED type, never the client's.
var allowedImageTypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/gif":  true,
	"image/webp": true,
}

// sniffImage reads the leading bytes of r to detect the true content type
// (ignoring any client-supplied value), validates it against allowedImageTypes,
// and returns the peeked head so the caller can re-assemble the full stream via
// io.MultiReader. Returns a domain error for a disallowed/unreadable image.
func sniffImage(r io.Reader) (head []byte, contentType string, err error) {
	buf := make([]byte, 512)
	n, rerr := io.ReadFull(r, buf)
	if rerr != nil && rerr != io.ErrUnexpectedEOF && rerr != io.EOF {
		return nil, "", model.ErrPlatform.WithError(rerr).WithMessage("Failed to read image").Err()
	}
	head = buf[:n]
	contentType = http.DetectContentType(head)
	if i := strings.IndexByte(contentType, ';'); i >= 0 { // strip "; charset=..."
		contentType = strings.TrimSpace(contentType[:i])
	}
	if !allowedImageTypes[contentType] {
		return nil, "", authModel.ErrInvalidAvatarType.Err()
	}
	return head, contentType, nil
}

// avatarDownloadClient fetches provider (e.g. Google) avatars for re-hosting. A redirect is followed
// only to another allowed avatar host.
var avatarDownloadClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 || !isProviderAvatarURL(req.URL.String()) {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

// isProviderAvatarURL allows only Google's avatar hosts (https, no credentials, no port). The
// "picture" field is the one value of the Google profile the daemon fetches, so it must not be
// able to point the backend at any other address.
func isProviderAvatarURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	return host == "googleusercontent.com" || strings.HasSuffix(host, ".googleusercontent.com")
}

// avatarKey is the LEGACY object-store key a user's avatar used to be stored
// under, before avatars moved onto the shared media service. Reads fall back
// to it when no media reference exists yet (a pre-migration avatar); removal
// best-effort cleans it up so old-style avatars actually disappear.
func avatarKey(userID uuid.UUID) string {
	return "avatars/" + userID.String()
}

// avatarURL returns the relative public path stored in users.picture.  Using a
// relative path means the avatar loads correctly on every subdomain / edge
// without knowing the current hostname.  The GET proxy route
// (/api/auth/avatar/:id) is public on all edges and streams the object from
// storage.  The `v` cache-buster changes on every store so the path can be
// cached immutably while a replaced photo still appears immediately (its path
// differs).
func (u *AuthUseCase) avatarURL(userID uuid.UUID, version int64) string {
	return fmt.Sprintf("/api/auth/avatar/%s?v=%d", userID, version)
}

// UploadAvatar stores a user-provided image through the shared media service
// and points users.picture at it. The previous avatar (if any) loses its
// reference in the same call, so the media GC reclaims it once its grace
// window passes — no direct blob deletion happens here.
func (u *AuthUseCase) UploadAvatar(
	ctx context.Context, userID uuid.UUID, r io.Reader, size int64, contentType string,
) error {
	if !u.storageConfigured {
		return authModel.ErrStorageUnavailable.WithError(errors.New("avatar-upload: object storage not configured")).Err()
	}
	if size > MaxAvatarBytes {
		return authModel.ErrAvatarTooLarge.Err()
	}
	// Never trust the client Content-Type: sniff the actual bytes and store the
	// canonical sniffed type. Rejects SVG and any non-raster-image content.
	head, sniffed, err := sniffImage(io.LimitReader(r, MaxAvatarBytes))
	if err != nil {
		return err
	}
	rest, err := io.ReadAll(io.LimitReader(r, MaxAvatarBytes))
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to read image").Err()
	}
	data := append(head, rest...)
	if err = mediaModel.CheckImagePixels(data); err != nil {
		return err
	}
	file, err := u.avatar.UploadFile(ctx, "avatar", sniffed, bytes.NewReader(data), userID)
	if err != nil {
		return err
	}
	if err = u.avatar.ReplaceReferences(ctx, mediaModel.RefTypeUserAvatar, userID, []uuid.UUID{file.ID}); err != nil {
		return err
	}
	return u.mutateUser(ctx, userID, func(user *userModel.User) error {
		user.SetPicture(u.avatarURL(userID, time.Now().Unix()), time.Now())
		return nil
	})
}

// RemoveAvatar drops the user's current avatar reference (letting media GC
// reclaim the now-unreferenced file) and clears users.picture. It also
// best-effort removes a legacy pre-migration avatar object stored directly
// under the old key, so an old-style avatar actually disappears; that removal
// is best-effort only (a missing key is not an error, and any other failure
// is logged rather than blocking the picture clear — the media reference is
// already gone by this point).
func (u *AuthUseCase) RemoveAvatar(ctx context.Context, userID uuid.UUID) error {
	if !u.storageConfigured {
		return authModel.ErrStorageUnavailable.WithError(errors.New("avatar-remove: object storage not configured")).Err()
	}
	if err := u.avatar.ReplaceReferences(ctx, mediaModel.RefTypeUserAvatar, userID, nil); err != nil {
		return err
	}
	if err := u.storage.Remove(ctx, avatarKey(userID)); err != nil {
		log.Warn().Err(err).Str("userID", userID.String()).Msg("avatar remove: legacy object removal failed")
	}
	return u.mutateUser(ctx, userID, func(user *userModel.User) error {
		user.SetPicture("", time.Now())
		return nil
	})
}

// GetAvatar opens the current avatar stream. It resolves the user's current
// avatar file through the media reference and streams it from there; when no
// media reference exists (a pre-migration avatar, never re-uploaded since),
// it falls back to the legacy direct-storage key. The public GET proxy route
// only ever calls this with the userID from the URL — it never accepts a raw
// fileID, so a caller cannot use this route to read any other media-service
// file (e.g. an exercise attachment). Caller must Close the reader.
//
// fileIDs[0] is used as-is: GetFileReferenceIDs orders newest-file-first, so
// if a concurrent double-upload has left more than one user_avatar reference,
// the newest wins deterministically rather than whichever row the database
// happened to return first.
func (u *AuthUseCase) GetAvatar(ctx context.Context, userID uuid.UUID) (io.ReadCloser, string, error) {
	fileIDs, err := u.avatar.GetReferences(ctx, mediaModel.RefTypeUserAvatar, userID)
	if err != nil {
		return nil, "", err
	}
	if len(fileIDs) > 0 {
		rc, file, err := u.avatar.StreamFile(ctx, fileIDs[0])
		if err != nil {
			return nil, "", err
		}
		return rc, file.ContentType, nil
	}
	// Legacy fallback: no media reference means this user's avatar (if any)
	// predates the migration and still lives under the old direct-storage key.
	if !u.storageConfigured {
		return nil, "", authModel.ErrStorageUnavailable.WithError(errors.New("avatar-get: object storage not configured")).Err()
	}
	return u.storage.Get(ctx, avatarKey(userID))
}

// adoptProviderAvatar re-hosts a provider avatar into our storage and points the
// user's picture at it. Best-effort: any failure is ignored, since the avatar is
// non-critical to the auth flow (the account is still usable without a photo).
func (u *AuthUseCase) adoptProviderAvatar(ctx context.Context, userID uuid.UUID, externalURL string) {
	url := u.syncProviderAvatar(ctx, userID, externalURL)
	if url == "" {
		return
	}
	_ = u.mutateUser(ctx, userID, func(user *userModel.User) error {
		user.SetPicture(url, time.Now())
		return nil
	})
}

// syncProviderAvatar best-effort downloads an external (provider) avatar URL and
// re-hosts it through the media service, returning the backend-proxied URL. On
// any failure it returns "" so the caller can fall back (no avatar rather than
// a broken one). All user photos — provider or uploaded — live in our own
// storage, never hot-linked.
func (u *AuthUseCase) syncProviderAvatar(ctx context.Context, userID uuid.UUID, externalURL string) string {
	if !u.storageConfigured || externalURL == "" {
		return ""
	}
	if !isProviderAvatarURL(externalURL) {
		log.Warn().Msg("provider avatar skipped: the picture URL is not on an allowed avatar host")
		return ""
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, externalURL, nil)
	if err != nil {
		return ""
	}
	resp, err := avatarDownloadClient.Do(req)
	if err != nil {
		return ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return ""
	}

	// Cap the read so a hostile/huge upstream can't exhaust memory, and sniff the
	// real type rather than trusting the upstream's Content-Type header.
	body := io.LimitReader(resp.Body, MaxAvatarBytes)
	head, sniffed, err := sniffImage(body)
	if err != nil {
		return ""
	}
	rest, err := io.ReadAll(body)
	if err != nil {
		return ""
	}
	data := append(head, rest...)
	if mediaModel.CheckImagePixels(data) != nil {
		return ""
	}
	file, err := u.avatar.UploadFile(ctx, "avatar", sniffed, bytes.NewReader(data), userID)
	if err != nil {
		return ""
	}
	if err = u.avatar.ReplaceReferences(ctx, mediaModel.RefTypeUserAvatar, userID, []uuid.UUID{file.ID}); err != nil {
		return ""
	}
	return u.avatarURL(userID, time.Now().Unix())
}
