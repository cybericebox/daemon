package exercise

import exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"

func importItemKey(prefix string) string {
	if prefix == "" {
		return "."
	}
	return prefix
}

// workingImportVersion is the source working copy. Updates import one snapshot,
// while a new import continues to restore every archived version. A history-only
// export is already ordered newest first; do not reorder a manually authored archive.
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
