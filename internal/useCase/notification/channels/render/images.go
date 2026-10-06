package render

import (
	"encoding/json"
	"fmt"

	"github.com/gofrs/uuid"
)

// ImageRefs is what a block array's OWN blocks reference. Preset blocks are
// not followed: presets are separate rows with their own lifecycle.
type ImageRefs struct {
	// FileIDs are the uploaded files of image blocks, unique, first-use order.
	FileIDs []uuid.UUID
	// Invalid lists non-empty file_id values that are not a non-nil UUID.
	Invalid []string
	// HasLogo is true when the body has a logo block.
	HasLogo bool
	// PresetIDs are the preset_id values of preset blocks, unique, first-use
	// order, unvalidated (RenderEmail skips an unknown preset).
	PresetIDs []string
}

// ScanImages walks body's top-level blocks and collects its image references.
// An empty / null body has none; a body that is not a block array is an error.
func ScanImages(body json.RawMessage) (ImageRefs, error) {
	var refs ImageRefs
	if len(body) == 0 || string(body) == "null" {
		return refs, nil
	}
	var blocks []block
	if err := json.Unmarshal(body, &blocks); err != nil {
		return refs, fmt.Errorf("render: invalid block array: %w", err)
	}
	seen := make(map[uuid.UUID]bool)
	seenPreset := make(map[string]bool)
	for _, b := range blocks {
		switch b.Type {
		case "logo":
			refs.HasLogo = true
		case "preset":
			if b.PresetID != "" && !seenPreset[b.PresetID] {
				seenPreset[b.PresetID] = true
				refs.PresetIDs = append(refs.PresetIDs, b.PresetID)
			}
		case "image":
			if b.FileID == "" {
				continue
			}
			id, err := uuid.FromString(b.FileID)
			if err != nil || id.IsNil() {
				refs.Invalid = append(refs.Invalid, b.FileID)
				continue
			}
			if !seen[id] {
				seen[id] = true
				refs.FileIDs = append(refs.FileIDs, id)
			}
		}
	}
	return refs, nil
}

// ImageFileIDs returns the uploaded files body's own image blocks reference
// (unique, first-use order) — the set a template row owns references to.
// Unparsable bodies and file_ids are ignored.
func ImageFileIDs(body json.RawMessage) []uuid.UUID {
	refs, _ := ScanImages(body)
	return refs.FileIDs
}
