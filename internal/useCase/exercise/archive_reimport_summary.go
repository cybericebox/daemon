package exercise

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	"github.com/gofrs/uuid"
)

// importSummary compares semantic working content, never source IDs or stored
// ciphertext. Only counts leave the server. Positional pairing is intentional:
// an archive from another installation has no shared internal identity.
func (u *ExerciseUseCase) importSummary(ctx context.Context, parsed ParsedExerciseArchive, source, target exerciseModel.ExerciseVersion, images map[string]imageRegistryUseCase.ArchiveImageInspection) (ArchiveImportSummary, error) {
	summary := ArchiveImportSummary{MetadataChanged: source.Name != target.Name || source.Description != target.Description || !slices.Equal(source.Tags, target.Tags) || source.AdminNote != target.AdminNote}
	before, after := len(target.Variants), len(source.Variants)
	summary.Variants.Added = max(after-before, 0)
	summary.Variants.Removed = max(before-after, 0)
	if before > 0 {
		summary.Subtasks.Before = len(target.Variants[0].Tasks)
	}
	if after > 0 {
		summary.Subtasks.After = len(source.Variants[0].Tasks)
	}
	// Work on independent copies before opening secrets or syncing links.
	sourceCopy, err := copyImportVersion(source)
	if err != nil {
		return summary, err
	}
	targetCopy, err := copyImportVersion(target)
	if err != nil {
		return summary, err
	}
	copies := []exerciseModel.ExerciseVersion{targetCopy}
	if err = u.decryptExportedSecrets(copies); err != nil {
		return summary, err
	}
	targetCopy = copies[0]
	if err = sourceCopy.SyncLinkedVariants(); err != nil {
		return summary, err
	}
	if err = targetCopy.SyncLinkedVariants(); err != nil {
		return summary, err
	}
	sourceHashes := map[uuid.UUID]string{}
	for id, path := range parsed.FilePaths {
		sum := sha256.Sum256(parsed.Files[path])
		sourceHashes[id] = hex.EncodeToString(sum[:])
	}
	targetHashes := map[uuid.UUID]string{}
	if ids := exerciseModel.CollectFileIDs(targetCopy.Variants); len(ids) > 0 {
		targetHashes, err = u.exercises.FileHashes(ctx, ids)
		if err != nil {
			return summary, model.ErrPlatform.WithError(err).WithMessage("Failed to compare import attachments").Err()
		}
	}
	oldFiles, newFiles := importFiles(targetCopy.Variants, targetHashes), importFiles(sourceCopy.Variants, sourceHashes)
	for file := range newFiles {
		if !oldFiles[file] {
			summary.Files.Added++
		}
	}
	for file := range oldFiles {
		if !newFiles[file] {
			summary.Files.Removed++
		}
	}
	seenImages := map[string]bool{}
	for _, image := range parsed.Images {
		info := images[image.Marker]
		if !seenImages[info.Digest] {
			seenImages[info.Digest] = true
			if info.Reused {
				summary.Images.Reused++
			} else {
				summary.Images.New++
			}
		}
	}
	sourceCanonical, err := canonicalImportVariants(sourceCopy.Variants, sourceHashes, images)
	if err != nil {
		return summary, err
	}
	targetCanonical, err := canonicalImportVariants(targetCopy.Variants, targetHashes, nil)
	if err != nil {
		return summary, err
	}
	for vi := 0; vi < min(before, after); vi++ {
		if !bytes.Equal(sourceCanonical[vi], targetCanonical[vi]) {
			summary.Variants.Changed++
		}
		for ti := 0; ti < min(len(sourceCopy.Variants[vi].Tasks), len(targetCopy.Variants[vi].Tasks)); ti++ {
			if !slices.Equal(sourceCopy.Variants[vi].Tasks[ti].Flag, targetCopy.Variants[vi].Tasks[ti].Flag) {
				summary.FlagsChanged++
			}
		}
	}
	return summary, nil
}

func copyImportVersion(version exerciseModel.ExerciseVersion) (exerciseModel.ExerciseVersion, error) {
	encoded, err := json.Marshal(version)
	if err != nil {
		return exerciseModel.ExerciseVersion{}, err
	}
	var copy exerciseModel.ExerciseVersion
	err = json.Unmarshal(encoded, &copy)
	return copy, err
}

func importFileIdentity(id uuid.UUID, name string, hashes map[uuid.UUID]string) string {
	hash, ok := hashes[id]
	if !ok {
		hash = "unresolved:" + id.String()
	} // never claim two missing files are equal
	encoded, _ := json.Marshal([]string{hash, name})
	return string(encoded)
}

func importFiles(variants []exerciseModel.Variant, hashes map[uuid.UUID]string) map[string]bool {
	files := map[string]bool{}
	for _, variant := range variants {
		for _, task := range variant.Tasks {
			for _, file := range task.Attachments {
				files[importFileIdentity(file.FileID, file.Name, hashes)] = true
			}
		}
	}
	return files
}

func comparisonID(kind string, i int) uuid.UUID {
	return uuid.NewV5(uuid.NamespaceOID, fmt.Sprintf("catalog-import-comparison:%s:%d", kind, i))
}

// canonicalImportVariants changes only identity/reference fields. Authored
// strings that happen to contain UUIDs (names, flags, text) remain exact.
func canonicalImportVariants(variants []exerciseModel.Variant, hashes map[uuid.UUID]string, images map[string]imageRegistryUseCase.ArchiveImageInspection) ([][]byte, error) {
	variantIDs := map[uuid.UUID]uuid.UUID{}
	for i, variant := range variants {
		if variant.ID != uuid.Nil {
			variantIDs[variant.ID] = comparisonID("variant", i)
		}
	}
	out := make([][]byte, len(variants))
	for vi := range variants {
		variant := &variants[vi]
		ids := map[string]string{}
		for old, id := range variantIDs {
			ids[old.String()] = id.String()
		}
		variant.ID = comparisonID("variant", vi)
		if variant.LinkedTo.Valid {
			if id, ok := variantIDs[variant.LinkedTo.UUID]; ok {
				variant.LinkedTo.UUID = id
			}
		}
		for di := range variant.Topology.Devices {
			device := &variant.Topology.Devices[di]
			id := comparisonID("device", di)
			if device.ID != uuid.Nil {
				ids[device.ID.String()] = id.String()
			}
			device.ID = id
			if info, ok := images[device.Image]; ok {
				device.Image = info.Digest
			} else if ref, ok := imageRegistryModel.ParsePlatformRef(device.Image); ok {
				device.Image = ref.Digest
			}
		}
		for ti := range variant.Tasks {
			task := &variant.Tasks[ti]
			task.ID = comparisonID("task", ti)
			if task.LinkedDeviceID.Valid {
				if id, ok := ids[task.LinkedDeviceID.UUID.String()]; ok {
					task.LinkedDeviceID.UUID = uuid.FromStringOrNil(id)
				}
			}
			keys := map[string]string{}
			for pi := range task.Placeholders {
				placeholder := &task.Placeholders[pi]
				key := fmt.Sprintf("placeholder:%d", pi)
				if placeholder.Key != "" {
					keys[placeholder.Key] = key
				}
				placeholder.Key = key
			}
			var err error
			task.Description, err = canonicalRichDocument(task.Description, keys)
			if err != nil {
				return nil, err
			}
			for hi := range task.Hints {
				hint := &task.Hints[hi]
				hint.ID = comparisonID("hint", hi)
				if json.Valid([]byte(hint.Text)) {
					text, err := canonicalRichDocument([]byte(hint.Text), keys)
					if err != nil {
						return nil, err
					}
					hint.Text = string(text)
				}
			}
			for ai := range task.Attachments {
				file := &task.Attachments[ai]
				file.FileID = uuid.NewV5(uuid.NamespaceOID, importFileIdentity(file.FileID, file.Name, hashes))
			}
		}
		for ci := range variant.Topology.Connections {
			for ei := range variant.Topology.Connections[ci].Endpoints {
				endpoint := &variant.Topology.Connections[ci].Endpoints[ei]
				if id, ok := ids[endpoint.DeviceID.String()]; ok {
					endpoint.DeviceID = uuid.FromStringOrNil(id)
				}
			}
		}
		if len(variant.Topology.VisualRender) > 0 {
			var layout any
			if err := json.Unmarshal(variant.Topology.VisualRender, &layout); err != nil {
				return nil, err
			}
			variant.Topology.VisualRender, _ = json.Marshal(canonicalLayoutIDs(layout, ids))
		}
		encoded, err := json.Marshal(variant)
		if err != nil {
			return nil, err
		}
		var value any
		if err = json.Unmarshal(encoded, &value); err != nil {
			return nil, err
		}
		out[vi], err = json.Marshal(canonicalEmptyArrays(value))
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

func canonicalRichDocument(raw json.RawMessage, keys map[string]string) (json.RawMessage, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var document any
	if err := json.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			if node["type"] == "variable" {
				if name, ok := node["varName"].(string); ok {
					if key, known := keys[name]; known {
						node["varName"] = key
					}
				}
			}
			for _, child := range node {
				walk(child)
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(document)
	return json.Marshal(document)
}

func canonicalLayoutIDs(value any, ids map[string]string) any {
	switch node := value.(type) {
	case string:
		if id, ok := ids[node]; ok {
			return id
		}
	case []any:
		for i, child := range node {
			node[i] = canonicalLayoutIDs(child, ids)
		}
	case map[string]any:
		out := map[string]any{}
		for key, child := range node {
			if id, ok := ids[key]; ok {
				key = id
			}
			out[key] = canonicalLayoutIDs(child, ids)
		}
		return out
	}
	return value
}
func canonicalEmptyArrays(value any) any {
	switch node := value.(type) {
	case []any:
		if len(node) == 0 {
			return nil
		}
		for i, child := range node {
			node[i] = canonicalEmptyArrays(child)
		}
	case map[string]any:
		for key, child := range node {
			node[key] = canonicalEmptyArrays(child)
		}
	}
	return value
}
