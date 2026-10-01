package event

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/png"
	"io"

	_ "golang.org/x/image/webp"
	_ "image/jpeg"

	"github.com/gofrs/uuid"
	"golang.org/x/image/draw"

	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/model/notification/branding"
	emailUseCase "github.com/cybericebox/daemon/internal/useCase/notification/channels/email"
)

// ResolveEventEmailBrand is shared by template preview, logo endpoint and
// delivery. A missing event logo uses the platform crest; colors always come
// from the published event theme.
func (u *EventUseCase) ResolveEventEmailBrand(ctx context.Context, eventID uuid.UUID) (emailUseCase.Brand, error) {
	brand := emailUseCase.PlatformBrand()
	if u.brandMedia == nil {
		return brand, nil
	}
	config, err := u.GetEventConfig(ctx, eventID)
	if err != nil {
		return emailUseCase.Brand{}, err
	}
	brand.Colors.Brand = config.Theme.Brand
	brand.Colors.Accent = config.Theme.Accent
	if brand.Colors.Accent == "" {
		brand.Colors.Accent = brand.Colors.Brand
	}
	brand.Colors.OnAccent = config.Theme.EmailOnAccent()

	ids, err := u.brandMedia.GetReferences(ctx, mediaModel.RefTypeEventLogo, eventID)
	if err != nil {
		return emailUseCase.Brand{}, err
	}
	if len(ids) == 0 {
		return brand, nil
	}
	reader, _, err := u.brandMedia.StreamFile(ctx, ids[0])
	if err != nil {
		return emailUseCase.Brand{}, err
	}
	defer func() { _ = reader.Close() }()
	data, err := io.ReadAll(io.LimitReader(reader, maxEventLogoBytes+1))
	if err != nil {
		return emailUseCase.Brand{}, err
	}
	if int64(len(data)) > maxEventLogoBytes {
		return emailUseCase.Brand{}, fmt.Errorf("event logo exceeds %d bytes", maxEventLogoBytes)
	}
	if e, err := u.events.GetByID(ctx, eventID); err == nil {
		brand.LogoAlt = e.Name
	}
	brand.Logo, err = emailLogoPNG(data)
	if err != nil {
		return emailUseCase.Brand{}, err
	}
	brand.LogoContentType = branding.LogoContentType
	return brand, nil
}

// emailLogoPNG keeps the logo's aspect ratio and alpha while bounding its
// inline payload. WebP is converted because email clients support PNG more
// consistently. The uploaded source is left untouched.
func emailLogoPNG(data []byte) ([]byte, error) {
	info, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	if info.Width <= 0 || info.Height <= 0 || int64(info.Width)*int64(info.Height) > 32<<20 {
		return nil, fmt.Errorf("event logo dimensions are invalid")
	}
	source, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	width, height := info.Width, info.Height
	if width > 256 || height > 256 {
		if width >= height {
			height = max(1, height*256/width)
			width = 256
		} else {
			width = max(1, width*256/height)
			height = 256
		}
	}
	output := image.NewNRGBA(image.Rect(0, 0, width, height))
	draw.ApproxBiLinear.Scale(output, output.Bounds(), source, source.Bounds(), draw.Over, nil)
	var encoded bytes.Buffer
	if err := png.Encode(&encoded, output); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}
