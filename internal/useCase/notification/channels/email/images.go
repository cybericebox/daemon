package emailUseCase

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"strings"

	"github.com/gofrs/uuid"
	"golang.org/x/image/draw"

	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
)

// The template image limits are set once at start (EMAIL_IMAGE_UPLOAD_MAX_BYTES,
// EMAIL_IMAGE_MAX_BYTES, EMAIL_IMAGE_MAX_WIDTH, EMAIL_IMAGE_MAX_PIXELS).
var (
	// MaxTemplateImageUploadBytes caps the RAW upload (before processing); the
	// processed image must additionally fit MaxTemplateImageBytes.
	MaxTemplateImageUploadBytes = 10 << 20
	// MaxTemplateImageBytes is the per-image limit after processing.
	MaxTemplateImageBytes = 300 << 10
	// MaxTemplateImageWidth is 2× a 600 px email column.
	MaxTemplateImageWidth = 1200
	// maxTemplateImagePixels bounds the decoded size (decompression-bomb guard).
	maxTemplateImagePixels = 24_000_000
)

// TemplateImageLimits are the email template image limits.
type TemplateImageLimits struct {
	UploadBytes, Bytes, Width, Pixels int
}

// ConfigureTemplateImages sets the email template image limits once at start.
func ConfigureTemplateImages(l TemplateImageLimits) {
	MaxTemplateImageUploadBytes, MaxTemplateImageBytes = l.UploadBytes, l.Bytes
	MaxTemplateImageWidth, maxTemplateImagePixels = l.Width, l.Pixels
}

const (
	templateImageJPEGQuality = 85

	contentTypePNG  = "image/png"
	contentTypeJPEG = "image/jpeg"
	contentTypeGIF  = "image/gif"
)

// templateImageInvalid is the single raise point of ErrTemplateImageInvalid.
func templateImageInvalid(cause error) error {
	return notificationModel.ErrTemplateImageInvalid.WithError(cause).Err()
}

// templateInlineTooLarge is the single raise point of ErrTemplateInlineTooLarge
// (publish-time size check and preview).
func templateInlineTooLarge(cause error) error {
	return notificationModel.ErrTemplateInlineTooLarge.WithError(cause).Err()
}

// templateImageTooLarge is the use case's raise point of ErrTemplateImageTooLarge.
func templateImageTooLarge(cause error) error {
	return notificationModel.ErrTemplateImageTooLarge.WithError(cause).Err()
}

// ProcessTemplateImage turns an uploaded email image into its stored form:
// the SNIFFED type must be PNG, JPEG or GIF (SVG and anything else →
// ErrTemplateImageInvalid); images wider than MaxTemplateImageWidth are
// downscaled (CatmullRom); PNG and GIF (first frame) are re-encoded as PNG,
// JPEG as JPEG q85. A PNG above MaxTemplateImageBytes without transparency is
// retried as JPEG q85. A result still above the limit → ErrTemplateImageTooLarge.
func ProcessTemplateImage(r io.Reader) (data []byte, contentType string, err error) {
	raw, err := io.ReadAll(io.LimitReader(r, int64(MaxTemplateImageUploadBytes)+1))
	if err != nil {
		return nil, "", templateImageInvalid(fmt.Errorf("read image: %w", err))
	}
	if len(raw) > MaxTemplateImageUploadBytes {
		return nil, "", templateImageTooLarge(fmt.Errorf("upload exceeds %d bytes", MaxTemplateImageUploadBytes))
	}
	sniffed := http.DetectContentType(raw)
	if i := strings.IndexByte(sniffed, ';'); i >= 0 {
		sniffed = strings.TrimSpace(sniffed[:i])
	}
	cfg, _, err := decodeConfig(sniffed, raw)
	if err != nil {
		return nil, "", templateImageInvalid(err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return nil, "", templateImageInvalid(errors.New("image has no pixels"))
	}
	if cfg.Width*cfg.Height > maxTemplateImagePixels {
		return nil, "", templateImageTooLarge(fmt.Errorf("image is %dx%d pixels", cfg.Width, cfg.Height))
	}
	src, err := decodeImage(sniffed, raw)
	if err != nil {
		return nil, "", templateImageInvalid(err)
	}
	img := downscale(src, MaxTemplateImageWidth)

	if sniffed == contentTypeJPEG {
		data, err = encodeJPEG(img)
		contentType = contentTypeJPEG
	} else {
		data, err = encodePNG(img)
		contentType = contentTypePNG
		if err == nil && len(data) > MaxTemplateImageBytes && isOpaque(img) {
			data, err = encodeJPEG(img)
			contentType = contentTypeJPEG
		}
	}
	if err != nil {
		return nil, "", templateImageInvalid(err)
	}
	if len(data) > MaxTemplateImageBytes {
		return nil, "", templateImageTooLarge(fmt.Errorf("processed %s is %d bytes", contentType, len(data)))
	}
	return data, contentType, nil
}

// decodeConfig reads only the header of an allowed image type.
func decodeConfig(sniffed string, raw []byte) (image.Config, string, error) {
	switch sniffed {
	case contentTypePNG:
		c, err := png.DecodeConfig(bytes.NewReader(raw))
		return c, "png", err
	case contentTypeJPEG:
		c, err := jpeg.DecodeConfig(bytes.NewReader(raw))
		return c, "jpeg", err
	case contentTypeGIF:
		c, err := gif.DecodeConfig(bytes.NewReader(raw))
		return c, "gif", err
	default:
		return image.Config{}, "", fmt.Errorf("unsupported image type %q", sniffed)
	}
}

// decodeImage decodes an allowed image type; a GIF yields its first frame.
func decodeImage(sniffed string, raw []byte) (image.Image, error) {
	switch sniffed {
	case contentTypePNG:
		return png.Decode(bytes.NewReader(raw))
	case contentTypeJPEG:
		return jpeg.Decode(bytes.NewReader(raw))
	case contentTypeGIF:
		return gif.Decode(bytes.NewReader(raw))
	default:
		return nil, fmt.Errorf("unsupported image type %q", sniffed)
	}
}

// downscale returns src scaled to maxWidth (keeping the aspect ratio) when it
// is wider, otherwise src itself.
func downscale(src image.Image, maxWidth int) image.Image {
	b := src.Bounds()
	if b.Dx() <= maxWidth {
		return src
	}
	h := max(b.Dy()*maxWidth/b.Dx(), 1)
	dst := image.NewRGBA(image.Rect(0, 0, maxWidth, h))
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, b, draw.Src, nil)
	return dst
}

// isOpaque reports whether img has no transparent pixel (JPEG cannot keep one).
func isOpaque(img image.Image) bool {
	if o, ok := img.(interface{ Opaque() bool }); ok {
		return o.Opaque()
	}
	b := img.Bounds()
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			if _, _, _, a := img.At(x, y).RGBA(); a != 0xffff {
				return false
			}
		}
	}
	return true
}

func encodePNG(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestCompression}
	if err := enc.Encode(&b, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return b.Bytes(), nil
}

func encodeJPEG(img image.Image) ([]byte, error) {
	var b bytes.Buffer
	if err := jpeg.Encode(&b, img, &jpeg.Options{Quality: templateImageJPEGQuality}); err != nil {
		return nil, fmt.Errorf("encode jpeg: %w", err)
	}
	return b.Bytes(), nil
}

// ── Use case: upload / stream ──

// templateImageName is the stored name when the client sends none.
const templateImageName = "email-image"

// MaxEmailImageUploadBytes exposes the raw upload cap so the multipart handler
// enforces the same limit at the HTTP boundary.
func (u *NotificationEmailTemplateUseCase) MaxEmailImageUploadBytes() int64 {
	return int64(MaxTemplateImageUploadBytes)
}

// UploadEmailImage processes an uploaded image (ProcessTemplateImage) and
// stores the result through the media subsystem. The file stays unreferenced
// (GC-eligible after the grace window) until a template draft that uses it is
// saved.
func (u *NotificationEmailTemplateUseCase) UploadEmailImage(ctx context.Context, r io.Reader, name string, userID uuid.UUID) (mediaModel.File, error) {
	data, contentType, err := ProcessTemplateImage(r)
	if err != nil {
		return mediaModel.File{}, err
	}
	if name == "" {
		name = templateImageName
	}
	return u.media.UploadFile(ctx, name, contentType, bytes.NewReader(data), userID)
}

// StreamEmailImage opens an uploaded template image. Only files of the image
// types this use case stores are served, so the route cannot be used to read
// arbitrary media files (e.g. exercise attachments). Caller closes the reader.
func (u *NotificationEmailTemplateUseCase) StreamEmailImage(ctx context.Context, fileID uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	return u.images.Stream(ctx, fileID)
}

// isTemplateImageType reports whether contentType is one the upload pipeline
// stores (PNG or JPEG; GIF uploads are re-encoded as PNG).
func isTemplateImageType(contentType string) bool {
	return contentType == contentTypePNG || contentType == contentTypeJPEG
}
