package event_test

import (
	"bytes"
	"image"
	"image/png"
)

// realPNG is a genuine 1x1 picture: uploads read the header for its size, so a bare signature is not enough.
func realPNG(trailer string) []byte {
	var buf bytes.Buffer
	_ = png.Encode(&buf, image.NewGray(image.Rect(0, 0, 1, 1)))
	return append(buf.Bytes(), trailer...)
}
