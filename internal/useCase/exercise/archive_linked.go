package exercise

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
)

// A linked variant of the archive carries linked_to (the 1-based position of its base in the version) and only
// its own parts: note (with optional id, index and weight), device_images (its own image for base devices, by
// device name) and, per task, flag and attachments (with an optional id).
// Everything else is the base's: SyncLinkedVariants fills it in on import.
var (
	linkedVariantKeys = map[string]bool{"linked_to": true, "id": true, "index": true, "note": true, "weight": true, "device_images": true, "tasks": true}
	linkedTaskKeys    = map[string]bool{"id": true, "flag": true, "attachments": true}
)

// archiveLinkedVariantField is the one place ErrArchiveLinkedVariantField is raised; field is the 1-based path.
func archiveLinkedVariantField(field string) error {
	return exerciseModel.ErrArchiveLinkedVariantField.WithPublicContext("field", field).Err()
}

// archiveLinkedToInvalid is the one place ErrArchiveLinkedToInvalid is raised.
func archiveLinkedToInvalid(variant string, linkedTo int) error {
	return exerciseModel.ErrArchiveLinkedToInvalid.WithPublicContext("variant", variant).WithPublicContext("linked_to", linkedTo).Err()
}

// checkLinkedVariantKeys names the first key (alphabetical) a linked variant or one of its tasks may not carry.
// A document of another shape is left to the strict decode, which reports it.
func checkLinkedVariantKeys(payload []byte) error {
	var doc struct {
		Versions []struct {
			Variants []map[string]json.RawMessage `json:"variants"`
		} `json:"versions"`
	}
	if json.Unmarshal(payload, &doc) != nil {
		return nil
	}
	for vi, version := range doc.Versions {
		for xi, variant := range version.Variants {
			if raw, ok := variant["linked_to"]; !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				continue
			}
			path := fmt.Sprintf("versions[%d].variants[%d]", vi+1, xi+1)
			if key := firstForeignKey(variant, linkedVariantKeys); key != "" {
				return archiveLinkedVariantField(path + "." + key)
			}
			var tasks []map[string]json.RawMessage
			if json.Unmarshal(variant["tasks"], &tasks) != nil {
				continue
			}
			for ti, task := range tasks {
				if key := firstForeignKey(task, linkedTaskKeys); key != "" {
					return archiveLinkedVariantField(fmt.Sprintf("%s.tasks[%d].%s", path, ti+1, key))
				}
			}
		}
	}
	return nil
}

// firstForeignKey is the alphabetically first key of object not in allowed, "" when none.
func firstForeignKey(object map[string]json.RawMessage, allowed map[string]bool) string {
	var foreign []string
	for key := range object {
		if !allowed[key] {
			foreign = append(foreign, key)
		}
	}
	if len(foreign) == 0 {
		return ""
	}
	sort.Strings(foreign)
	return foreign[0]
}

// toLinkedDomain maps a linked variant (at 0-based position in variants, ids the final variant ids, parsed the
// unlinked variants already mapped) to a variant holding only its own parts and the link to its base. The task
// list must match the base's by count and, where given, by task id; absent tasks are one empty task per base task.
func (v archiveVariantV2) toLinkedDomain(path string, position int, variants []archiveVariantV2, parsed []exerciseModel.Variant, ids []uuid.UUID, links *archiveLinks) (exerciseModel.Variant, error) {
	target := *v.LinkedTo
	if target < 1 || target > len(variants) || target == position+1 || variants[target-1].LinkedTo != nil {
		return exerciseModel.Variant{}, archiveLinkedToInvalid(path, target)
	}
	base := variants[target-1]
	variantsPath := path[:strings.LastIndex(path, "[")] // versions[V].variants
	if err := checkArchiveWeight(path, v.Weight); err != nil {
		return exerciseModel.Variant{}, err
	}
	variant := exerciseModel.Variant{
		ID: ids[position], Index: v.Index, Note: v.Note, Weight: v.Weight,
		LinkedTo: uuid.NullUUID{UUID: ids[target-1], Valid: true},
	}
	overrides, err := linkedDeviceImages(path, v.DeviceImages, parsed[target-1].Topology.Devices, links)
	if err != nil {
		return exerciseModel.Variant{}, err
	}
	variant.ImageOverrides = overrides
	if v.Tasks == nil {
		variant.Tasks = make([]exerciseModel.Task, len(base.Tasks))
		return variant, nil
	}
	if len(v.Tasks) != len(base.Tasks) {
		return exerciseModel.Variant{}, archiveLinkedVariantField(path + ".tasks")
	}
	for ti, task := range v.Tasks {
		taskPath := fmt.Sprintf("%s.tasks[%d]", path, ti+1)
		id, err := optionalUUID(taskPath+".id", task.ID)
		if err != nil {
			return exerciseModel.Variant{}, err
		}
		if id != uuid.Nil {
			// The base's own id is checked first, so an invalid one is reported at the base.
			baseID, baseErr := optionalUUID(fmt.Sprintf("%s[%d].tasks[%d].id", variantsPath, target, ti+1), base.Tasks[ti].ID)
			if baseErr != nil {
				return exerciseModel.Variant{}, baseErr
			}
			if id != baseID {
				return exerciseModel.Variant{}, archiveLinkedVariantField(taskPath + ".id")
			}
		}
		mapped := exerciseModel.Task{ID: id, Flag: task.Flag}
		for ai, attachment := range task.Attachments {
			ref, linkErr := links.attachment(fmt.Sprintf("%s.attachments[%d]", taskPath, ai+1), attachment)
			if linkErr != nil {
				return exerciseModel.Variant{}, linkErr
			}
			mapped.Attachments = append(mapped.Attachments, ref)
		}
		variant.Tasks = append(variant.Tasks, mapped)
	}
	return variant, nil
}

// linkedDeviceImages maps device_images (device name → image) to overrides of the base's devices, in name order.
// The name must belong to exactly one device of the base, not a switch or hub.
func linkedDeviceImages(path string, images map[string]archiveImageV2, baseDevices []exerciseModel.Device, links *archiveLinks) ([]exerciseModel.ImageOverride, error) {
	names := make([]string, 0, len(images))
	for name := range images {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []exerciseModel.ImageOverride
	for _, name := range names {
		where := path + ".device_images." + name
		var match []exerciseModel.Device
		for _, device := range baseDevices {
			if device.Name == name {
				match = append(match, device)
			}
		}
		if len(match) != 1 {
			return nil, archiveInvalid(where + " must name exactly one device of the base variant")
		}
		if match[0].Type.IsForwarding() {
			return nil, archiveInvalid(where + " names a switch or hub, which has no image")
		}
		image := images[name]
		ref := image.Ref
		switch {
		case image.File != "":
			if links.images == nil {
				return nil, archiveImageFileUnsupported(name)
			}
			marker, linkErr := links.image(name, image.File)
			if linkErr != nil {
				return nil, linkErr
			}
			ref = marker
		case ref == "" || strings.HasPrefix(ref, imageFileMarker):
			return nil, archiveInvalid(where + " is not a valid image reference")
		}
		out = append(out, exerciseModel.ImageOverride{DeviceID: match[0].ID, Image: ref})
	}
	return out, nil
}

// exportLinkedVariantV2 writes a linked variant by its own parts when its base is an unlinked variant of the
// same version and its tasks match the base's (count and ids); ok is false otherwise, and the variant is then
// written whole without the link.
func exportLinkedVariantV2(variant exerciseModel.Variant, variants []exerciseModel.Variant, paths map[uuid.UUID]string, imagePaths map[string]string) (archiveVariantV2, bool) {
	if !variant.LinkedTo.Valid {
		return archiveVariantV2{}, false
	}
	position := -1
	for i, candidate := range variants {
		if candidate.ID == variant.LinkedTo.UUID {
			position = i
			break
		}
	}
	if position < 0 || variants[position].LinkedTo.Valid || variants[position].ID == variant.ID {
		return archiveVariantV2{}, false
	}
	base := variants[position]
	if len(base.Tasks) != len(variant.Tasks) {
		return archiveVariantV2{}, false
	}
	linkedTo := position + 1
	out := archiveVariantV2{ID: optionalID(variant.ID), Index: variant.Index, Note: variant.Note, Weight: variant.Weight, LinkedTo: &linkedTo}
	// An own image is written by the device's name, so that name must be the base's alone.
	for _, override := range variant.ImageOverrides {
		name, count := "", 0
		for _, device := range base.Topology.Devices {
			if device.ID == override.DeviceID {
				name = device.Name
			}
		}
		for _, device := range base.Topology.Devices {
			if name != "" && device.Name == name {
				count++
			}
		}
		if count != 1 {
			return archiveVariantV2{}, false
		}
		if out.DeviceImages == nil {
			out.DeviceImages = map[string]archiveImageV2{}
		}
		out.DeviceImages[name] = exportDeviceImage(override.Image, imagePaths)
	}
	for ti, task := range variant.Tasks {
		if task.ID != uuid.Nil && task.ID != base.Tasks[ti].ID {
			return archiveVariantV2{}, false
		}
		out.Tasks = append(out.Tasks, archiveTaskV2{ID: optionalID(task.ID), Flag: task.Flag, Attachments: exportAttachmentRefsV2(task.Attachments, paths)})
	}
	return out, true
}

// archiveLinkedVariantOutV2 is the written form of a linked variant: its own parts only.
type (
	archiveLinkedVariantOutV2 struct {
		ID           string                    `json:"id,omitempty"`
		Index        int32                     `json:"index,omitempty"`
		Note         string                    `json:"note,omitempty"`
		Weight       int                       `json:"weight,omitempty"`
		LinkedTo     int                       `json:"linked_to"`
		DeviceImages map[string]archiveImageV2 `json:"device_images,omitempty"`
		Tasks        []archiveLinkedTaskOutV2  `json:"tasks,omitempty"`
	}
	archiveLinkedTaskOutV2 struct {
		ID          string                `json:"id,omitempty"`
		Flag        []string              `json:"flag,omitempty"`
		Attachments []archiveAttachmentV2 `json:"attachments,omitempty"`
	}
)

// MarshalJSON writes a linked variant in its compact form and any other variant in full. HTML characters are
// not escaped, like the rest of the document.
func (v archiveVariantV2) MarshalJSON() ([]byte, error) {
	type plain archiveVariantV2 // without this method
	var value any = plain(v)
	if v.LinkedTo != nil {
		out := archiveLinkedVariantOutV2{ID: v.ID, Index: v.Index, Note: v.Note, Weight: v.Weight, LinkedTo: *v.LinkedTo, DeviceImages: v.DeviceImages}
		for _, task := range v.Tasks {
			out.Tasks = append(out.Tasks, archiveLinkedTaskOutV2{ID: task.ID, Flag: task.Flag, Attachments: task.Attachments})
		}
		value = out
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buffer.Bytes(), "\n"), nil
}
