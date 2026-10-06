package mediaModel

import (
	"bytes"
	"image"
	// The decoders of the picture types the upload routes accept; the header is read, not the pixels.
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"

	_ "golang.org/x/image/webp"
)

// MaxImagePixels is the most pixels (width times height) an uploaded picture may have (IMAGE_MAX_PIXELS, set
// once at start).
var MaxImagePixels int64 = 16_000_000

// CheckImagePixels reads the header of a raster picture and refuses one with more than MaxImagePixels pixels.
// A header that cannot be read is refused too: the type sniff looked at the first bytes only.
func CheckImagePixels(data []byte) error {
	config, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || config.Width <= 0 || config.Height <= 0 {
		return ErrImageInvalid.Err()
	}
	if int64(config.Width)*int64(config.Height) > MaxImagePixels {
		return ErrImageTooLarge.Err()
	}
	return nil
}

// IsRasterType reports a content type CheckImagePixels can read.
func IsRasterType(contentType string) bool {
	switch contentType {
	case "image/png", "image/jpeg", "image/gif", "image/webp":
		return true
	}
	return false
}
