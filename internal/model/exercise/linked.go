package exerciseModel

import (
	"encoding/json"
	"strings"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
)

// IsLinked reports whether the variant inherits its content from a base
// variant of the same version.
func (v Variant) IsLinked() bool { return v.LinkedTo.Valid }

// SyncLinkedVariants rebuilds every linked variant from its base. A link to a
// variant that is not in the version is dropped (the content stays as an
// independent variant); a link to itself or to a linked variant is an error.
// A linked variant keeps only its ID, Index, Note, Weight, link, image overrides
// and, per task, its own Flag and Attachments (matched by task ID, else by
// position); everything else is a deep copy of the base, with the overridden
// device images written in. An independent variant has no image overrides.
// Idempotent.
func (v *ExerciseVersion) SyncLinkedVariants() error {
	positionByID := make(map[uuid.UUID]int, len(v.Variants))
	for i, variant := range v.Variants {
		if _, seen := positionByID[variant.ID]; !seen {
			positionByID[variant.ID] = i
		}
	}
	for i := range v.Variants {
		if v.Variants[i].LinkedTo.Valid {
			if _, ok := positionByID[v.Variants[i].LinkedTo.UUID]; !ok {
				v.Variants[i].LinkedTo = uuid.NullUUID{}
			}
		}
	}
	for i, variant := range v.Variants {
		if !variant.LinkedTo.Valid {
			continue
		}
		basePosition := positionByID[variant.LinkedTo.UUID]
		if basePosition == i || v.Variants[basePosition].LinkedTo.Valid {
			return ErrVariantLinkInvalid.WithContext("variant", i+1).Err()
		}
	}
	for i := range v.Variants {
		linked := v.Variants[i]
		if !linked.LinkedTo.Valid {
			v.Variants[i].ImageOverrides = nil
			continue
		}
		rebuilt, err := deepCopyVariant(v.Variants[positionByID[linked.LinkedTo.UUID]])
		if err != nil {
			return err
		}
		for taskIndex := range rebuilt.Tasks {
			rebuilt.Tasks[taskIndex].Flag = nil
			rebuilt.Tasks[taskIndex].Attachments = nil
			if own, ok := matchLinkedTask(linked.Tasks, rebuilt.Tasks[taskIndex].ID, taskIndex); ok {
				rebuilt.Tasks[taskIndex].Flag = own.Flag
				rebuilt.Tasks[taskIndex].Attachments = own.Attachments
			}
		}
		rebuilt.ID, rebuilt.Index, rebuilt.Note, rebuilt.LinkedTo, rebuilt.Weight = linked.ID, linked.Index, linked.Note, linked.LinkedTo, linked.Weight
		rebuilt.ImageOverrides = applyImageOverrides(&rebuilt.Topology, linked.ImageOverrides)
		v.Variants[i] = rebuilt
	}
	return nil
}

// applyImageOverrides writes the overrides into the topology (a copy of the base's) and returns the ones kept, in
// the order of the devices: only a container device of the base (by a non-nil id) can take one, an empty image is
// no override, and when a device is named twice the last one wins.
func applyImageOverrides(topology *Topology, overrides []ImageOverride) []ImageOverride {
	if len(overrides) == 0 {
		return nil
	}
	images := make(map[uuid.UUID]string, len(overrides))
	for _, override := range overrides {
		if override.DeviceID != uuid.Nil && override.Image != "" {
			images[override.DeviceID] = override.Image
		}
	}
	var kept []ImageOverride
	for di := range topology.Devices {
		device := &topology.Devices[di]
		image, ok := images[device.ID]
		if !ok || device.Type != DeviceTypeContainer {
			continue
		}
		delete(images, device.ID) // a repeated device id takes the override once
		device.Image = image
		kept = append(kept, ImageOverride{DeviceID: device.ID, Image: image})
	}
	return kept
}

// validateImageOverrides: an overridden image is a valid image reference (platform:<name>@sha256:… or a registry
// reference), as a device's image must be to run. variantIndex is 0-based.
func validateImageOverrides(variantIndex int, variant Variant) error {
	if len(variant.ImageOverrides) == 0 {
		return nil
	}
	names := make(map[uuid.UUID]string, len(variant.Topology.Devices))
	for _, device := range variant.Topology.Devices {
		names[device.ID] = device.Name
	}
	for _, override := range variant.ImageOverrides {
		if !ValidImageReference(override.Image) {
			return imageRegistryModel.ErrDeviceImageInvalid.WithPublicContext("variant", variantIndex+1).
				WithPublicContext("device", names[override.DeviceID]).Err()
		}
	}
	return nil
}

// PendingArchiveImagePrefix starts the image of a device whose image file from an archive is not pushed yet (the
// rest is the file's archive path); the import replaces it with the pushed image's reference.
const PendingArchiveImagePrefix = "\x00archive-image:"

// ValidImageReference: a platform reference, a registry reference, or an archive image pending its push.
func ValidImageReference(image string) bool {
	switch {
	case strings.HasPrefix(image, PendingArchiveImagePrefix):
		return len(image) > len(PendingArchiveImagePrefix)
	case strings.HasPrefix(image, "platform:"):
		_, ok := imageRegistryModel.ParsePlatformRef(image)
		return ok
	}
	_, err := imageRegistryModel.ParseImageRef(image)
	return err == nil
}

// deepCopyVariant copies through JSON, so fields tagged json:"-" are dropped
// (Variant has none today).
func deepCopyVariant(variant Variant) (Variant, error) {
	data, err := json.Marshal(variant)
	if err != nil {
		return Variant{}, err
	}
	var out Variant
	if err = json.Unmarshal(data, &out); err != nil {
		return Variant{}, err
	}
	return out, nil
}

// matchLinkedTask finds the linked variant's own task for a base task: by task
// ID when both IDs are non-nil, else by position (a linked task that has no ID
// takes the task at the same position).
func matchLinkedTask(tasks []Task, baseID uuid.UUID, position int) (Task, bool) {
	if baseID != uuid.Nil {
		for _, task := range tasks {
			if task.ID == baseID {
				return task, true
			}
		}
	}
	if position < len(tasks) && (baseID == uuid.Nil || tasks[position].ID == uuid.Nil) {
		return tasks[position], true
	}
	return Task{}, false
}

// validateLinkedFlags: within a base and its linked variants, a fixed flag
// value of one task position must not repeat across variants (templates may).
func validateLinkedFlags(variants []Variant) error {
	groups := make(map[uuid.UUID][]Variant, len(variants))
	for _, variant := range variants {
		if variant.LinkedTo.Valid {
			groups[variant.LinkedTo.UUID] = append(groups[variant.LinkedTo.UUID], variant)
		}
	}
	for _, base := range variants {
		linked := groups[base.ID]
		if base.LinkedTo.Valid || len(linked) == 0 {
			continue
		}
		group := append([]Variant{base}, linked...)
		for taskIndex, task := range base.Tasks {
			owner := make(map[string]int)
			for variantIndex, member := range group {
				if taskIndex >= len(member.Tasks) {
					continue
				}
				for _, value := range member.Tasks[taskIndex].Flag {
					if strings.HasPrefix(value, flagpattern.TemplatePrefix) {
						continue
					}
					if previous, seen := owner[value]; seen && previous != variantIndex {
						return ErrLinkedFlagsNotDistinct.WithContext("task", task.Name).Err()
					}
					owner[value] = variantIndex
				}
			}
		}
	}
	return nil
}
