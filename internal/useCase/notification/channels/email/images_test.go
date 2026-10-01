package emailUseCase_test

import (
	"bytes"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand/v2"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

func solidRGBA(w, h int, c color.Color) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, c)
		}
	}
	return img
}

// noiseRGBA is deterministic per-pixel random noise: incompressible for both
// PNG and JPEG, so the size rule's outcome does not depend on encoder tuning.
func noiseRGBA(w, h int, alpha uint8) *image.RGBA {
	r := rand.New(rand.NewPCG(1, 2))
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i < len(img.Pix); i += 4 {
		img.Pix[i] = uint8(r.UintN(256))
		img.Pix[i+1] = uint8(r.UintN(256))
		img.Pix[i+2] = uint8(r.UintN(256))
		img.Pix[i+3] = alpha
	}
	return img
}

func encodePNG(t *testing.T, img image.Image) []byte {
	t.Helper()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, img))
	return b.Bytes()
}

func TestProcessTemplateImage_DownscalesWidePNGTo1200(t *testing.T) {
	in := encodePNG(t, solidRGBA(2000, 1000, color.RGBA{R: 200, G: 10, B: 10, A: 255}))

	data, contentType, err := emailUseCase.ProcessTemplateImage(bytes.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, "image/png", contentType)
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
	assert.Equal(t, 1200, cfg.Width)
	assert.Equal(t, 600, cfg.Height)
}

func TestProcessTemplateImage_KeepsNarrowImageSize(t *testing.T) {
	in := encodePNG(t, solidRGBA(300, 100, color.White))

	data, _, err := emailUseCase.ProcessTemplateImage(bytes.NewReader(in))
	require.NoError(t, err)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, 300, cfg.Width)
	assert.Equal(t, 100, cfg.Height)
}

func TestProcessTemplateImage_JPEGStaysJPEG(t *testing.T) {
	var in bytes.Buffer
	require.NoError(t, jpeg.Encode(&in, solidRGBA(640, 480, color.RGBA{R: 10, G: 120, B: 10, A: 255}), &jpeg.Options{Quality: 90}))

	data, contentType, err := emailUseCase.ProcessTemplateImage(&in)
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", contentType)
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "jpeg", format)
}

func TestProcessTemplateImage_GIFBecomesPNG(t *testing.T) {
	pal := image.NewPaletted(image.Rect(0, 0, 40, 20), []color.Color{color.Black, color.White})
	var in bytes.Buffer
	require.NoError(t, gif.EncodeAll(&in, &gif.GIF{Image: []*image.Paletted{pal, pal}, Delay: []int{10, 10}}))

	data, contentType, err := emailUseCase.ProcessTemplateImage(&in)
	require.NoError(t, err)
	assert.Equal(t, "image/png", contentType)
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	assert.Equal(t, "png", format)
}

func TestProcessTemplateImage_RejectsSVG(t *testing.T) {
	svg := []byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"><script>alert(1)</script></svg>`)

	_, _, err := emailUseCase.ProcessTemplateImage(bytes.NewReader(svg))
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err))
}

func TestProcessTemplateImage_RejectsUndecodablePNG(t *testing.T) {
	// A valid PNG signature (sniffs as image/png) followed by garbage.
	bad := append([]byte("\x89PNG\r\n\x1a\n"), bytes.Repeat([]byte{0xAB}, 64)...)

	_, _, err := emailUseCase.ProcessTemplateImage(bytes.NewReader(bad))
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateImageInvalid.Err().Is(err))
}

func TestProcessTemplateImage_OpaqueNoiseTooLargeEvenAsJPEG(t *testing.T) {
	// 1200×1200 opaque noise: PNG ≈ 4.3 MB (> 300 KB, no alpha → retried as
	// JPEG q85), and incompressible noise stays far above 300 KB as JPEG too.
	in := encodePNG(t, noiseRGBA(1200, 1200, 0xFF))
	require.Greater(t, len(in), 300<<10)

	_, _, err := emailUseCase.ProcessTemplateImage(bytes.NewReader(in))
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateImageTooLarge.Err().Is(err))
}

func TestProcessTemplateImage_TranslucentLargePNGIsNotConvertedToJPEG(t *testing.T) {
	// Alpha must survive: a big translucent PNG is rejected, never flattened.
	in := encodePNG(t, noiseRGBA(400, 400, 0x80))
	require.Greater(t, len(in), 300<<10)

	_, _, err := emailUseCase.ProcessTemplateImage(bytes.NewReader(in))
	require.Error(t, err)
	assert.True(t, notificationModel.ErrTemplateImageTooLarge.Err().Is(err))
}

func TestProcessTemplateImage_OpaqueLargePNGFallsBackToJPEG(t *testing.T) {
	// 400×400 opaque noise: PNG ≈ 480 KB (> 300 KB), JPEG q85 of 160k pixels
	// of noise is well under 300 KB.
	in := encodePNG(t, noiseRGBA(400, 400, 0xFF))
	require.Greater(t, len(in), 300<<10)

	data, contentType, err := emailUseCase.ProcessTemplateImage(bytes.NewReader(in))
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", contentType)
	assert.LessOrEqual(t, len(data), 300<<10)
}
