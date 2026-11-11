package exercise

import exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"

func importItemKey(prefix string) string {
	if prefix == "" {
		return "."
	}
	return prefix
}

// workingImportVersion is the source working copy: the draft, else the published version, else the first one. An
// import (new or update) brings only this snapshot. A history-only export is already ordered newest first; do not
// reorder a manually authored archive.
func workingImportVersion(versions []exerciseModel.ExerciseVersion) int {
	for _, status := range []exerciseModel.VersionStatus{exerciseModel.VersionStatusDraft, exerciseModel.VersionStatusPublished} {
		for i := range versions {
			if versions[i].Status == status {
				return i
			}
		}
	}
	if len(versions) > 0 {
		return 0
	}
	return -1
}

// keepOneVersionAsDraft reduces a parsed archive to the one version the import brings: the working copy (see
// workingImportVersion). Whatever status the document gave it, it becomes a draft: an import never publishes. The
// other versions, and the images only they name, are dropped. Their attachments stay in Files but are not copied,
// as the copy takes only the files the kept version links.
func (p *ParsedExerciseArchive) keepOneVersionAsDraft() {
	selected := workingImportVersion(p.Versions)
	if selected < 0 {
		return
	}
	version := p.Versions[selected]
	version.Status = exerciseModel.VersionStatusDraft
	version.PublishedAt = nil
	version.Label = ""
	p.Versions = []exerciseModel.ExerciseVersion{version}
	used := map[string]bool{}
	for _, variant := range version.Variants {
		for _, device := range variant.Topology.Devices {
			used[device.Image] = true
		}
		for _, override := range variant.ImageOverrides {
			used[override.Image] = true
		}
	}
	kept := p.Images[:0]
	for _, image := range p.Images {
		if used[image.Marker] {
			kept = append(kept, image)
		}
	}
	p.Images = kept
}
