package render

import (
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestImageFileIDs_UniqueInOrderTopLevelOnly(t *testing.T) {
	a := uuid.Must(uuid.NewV7())
	b := uuid.Must(uuid.NewV7())
	body := json.RawMessage(`[
		{"type":"image","file_id":"` + b.String() + `"},
		{"type":"image","url":"https://example.com/x.png"},
		{"type":"image","file_id":"` + a.String() + `"},
		{"type":"image","file_id":"` + b.String() + `"},
		{"type":"preset","preset_id":"p1"},
		{"type":"logo"}
	]`)

	assert.Equal(t, []uuid.UUID{b, a}, ImageFileIDs(body))
}

func TestImageFileIDs_EmptyAndInvalidBodies(t *testing.T) {
	assert.Empty(t, ImageFileIDs(json.RawMessage(`[]`)))
	assert.Empty(t, ImageFileIDs(nil))
	assert.Empty(t, ImageFileIDs(json.RawMessage(`not json`)))
	assert.Empty(t, ImageFileIDs(json.RawMessage(`[{"type":"image","file_id":"nope"}]`)))
}

func TestScanImages_ReportsInvalidFileIDsAndLogo(t *testing.T) {
	a := uuid.Must(uuid.NewV7())
	body := json.RawMessage(`[
		{"type":"image","file_id":"nope"},
		{"type":"image","file_id":"` + uuid.Nil.String() + `"},
		{"type":"image","file_id":""},
		{"type":"image","file_id":"` + a.String() + `"},
		{"type":"logo"}
	]`)

	refs, err := ScanImages(body)
	require.NoError(t, err)
	assert.Equal(t, []uuid.UUID{a}, refs.FileIDs)
	assert.Equal(t, []string{"nope", uuid.Nil.String()}, refs.Invalid)
	assert.True(t, refs.HasLogo)
}

func TestScanImages_NoLogo(t *testing.T) {
	refs, err := ScanImages(json.RawMessage(`[{"type":"divider"}]`))
	require.NoError(t, err)
	assert.False(t, refs.HasLogo)
	assert.Empty(t, refs.FileIDs)
	assert.Empty(t, refs.Invalid)
}

func TestScanImages_InvalidJSON(t *testing.T) {
	_, err := ScanImages(json.RawMessage(`{`))
	require.Error(t, err)
}

func TestScanImages_ReportsPresetIDs(t *testing.T) {
	p := uuid.Must(uuid.NewV7())
	refs, err := ScanImages(json.RawMessage(`[
		{"type":"preset","preset_id":"` + p.String() + `"},
		{"type":"preset","preset_id":"` + p.String() + `"},
		{"type":"preset","preset_id":"junk"}
	]`))
	require.NoError(t, err)
	assert.Equal(t, []string{p.String(), "junk"}, refs.PresetIDs)
}
