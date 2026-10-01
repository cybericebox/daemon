package branding_test

import (
	"bytes"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/model/notification/branding"
)

func TestPlatform(t *testing.T) {
	assert.Equal(t, branding.Context{Brand: "#211A52", Accent: "#211A52", OnAccent: "#FFFFFF"}, branding.Platform())
}

func TestLogoPNG(t *testing.T) {
	assert.Equal(t, "image/png", branding.LogoContentType)
	cfg, err := png.DecodeConfig(bytes.NewReader(branding.LogoPNG()))
	require.NoError(t, err)
	assert.Equal(t, 128, cfg.Width)
	assert.Equal(t, 125, cfg.Height)
}
