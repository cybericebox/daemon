package mediaModel

import (
	"bytes"
	"errors"
	"image"
	"image/png"
	"testing"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewGray(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// A small file can hold a huge canvas: the header is what counts.
func TestAPictureWithTooManyPixelsIsRefused(t *testing.T) {
	if err := CheckImagePixels(pngOf(t, 100, 100)); err != nil {
		t.Fatalf("an ordinary picture: %v", err)
	}
	prev := MaxImagePixels
	MaxImagePixels = 10_000
	defer func() { MaxImagePixels = prev }()
	if err := CheckImagePixels(pngOf(t, 100, 100)); err != nil {
		t.Fatalf("exactly at the limit: %v", err)
	}
	big := pngOf(t, 101, 100)
	if err := CheckImagePixels(big); !errors.Is(err, ErrImageTooLarge.Err()) {
		t.Fatalf("one row over the limit: %v", err)
	}
	if err := CheckImagePixels([]byte("not an image")); !errors.Is(err, ErrImageInvalid.Err()) {
		t.Fatalf("garbage: %v", err)
	}
	if !IsRasterType("image/webp") || IsRasterType("image/svg+xml") {
		t.Fatal("raster types")
	}
}

func TestTheDefaultLimitIsSixteenMegapixels(t *testing.T) {
	if MaxImagePixels != 16_000_000 {
		t.Fatalf("default = %d", MaxImagePixels)
	}
	if err := CheckImagePixels(pngOf(t, 4001, 4001)); !errors.Is(err, ErrImageTooLarge.Err()) {
		t.Fatalf("4001 x 4001 passes 16 MP: %v", err)
	}
}
