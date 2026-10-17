package exercise

import (
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

// fileUse is one attachment of the exported document: the content it links (Hash), the name shown for it and
// where it sits, as 1-based positions in the exported order.
type fileUse struct {
	Hash    string
	Name    string
	Version int
	Variant int
	Task    int
	// Attachment is the 1-based position in the subtask's attachment list.
	Attachment int
	// Device is the 1-based position of the device in its variant, for a device image (Task and Attachment are then
	// 0): the image sits with its variant, not with a subtask.
	Device int
}

// assignExportPaths places every content (hash) once under root: a content used by one subtask goes to
// <root>/v<V>/variant-<N>/<T>/, one used by several goes to the shared/ folder of the lowest level covering all
// its uses (the variant, the version or root itself). The file takes the name of its first use; a name taken in
// the folder gets _2, _3 before the extension. The uses are ordered by position first (version, variant, task,
// attachment; name and hash only break ties of equal positions), so the result does not depend on the order
// they come in.
func assignExportPaths(root string, uses []fileUse) map[string]string {
	sorted := append([]fileUse(nil), uses...)
	sort.Slice(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Version != b.Version {
			return a.Version < b.Version
		}
		if a.Variant != b.Variant {
			return a.Variant < b.Variant
		}
		if a.Task != b.Task {
			return a.Task < b.Task
		}
		if a.Device != b.Device {
			return a.Device < b.Device
		}
		if a.Attachment != b.Attachment {
			return a.Attachment < b.Attachment
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.Hash < b.Hash
	})
	var order []string
	byHash := map[string][]fileUse{}
	for _, use := range sorted {
		if _, seen := byHash[use.Hash]; !seen {
			order = append(order, use.Hash)
		}
		byHash[use.Hash] = append(byHash[use.Hash], use)
	}
	taken := map[string]map[string]bool{}
	paths := make(map[string]string, len(order))
	for _, hash := range order {
		group := byHash[hash]
		folder := exportFolder(root, group)
		if taken[folder] == nil {
			taken[folder] = map[string]bool{}
		}
		name := freeName(exportName(group[0].Name), taken[folder])
		paths[hash] = folder + "/" + name
	}
	return paths
}

// exportFolder is the lowest level that covers every use of one content.
func exportFolder(root string, uses []fileUse) string {
	first := uses[0]
	sameVersion, sameVariant, sameTask, sameDevice := true, true, true, true
	for _, use := range uses[1:] {
		if use.Version != first.Version {
			sameVersion = false
		}
		if !sameVersion || use.Variant != first.Variant {
			sameVariant = false
		}
		if !sameVariant || use.Task != first.Task {
			sameTask = false
		}
		if !sameTask || use.Device != first.Device {
			sameDevice = false
		}
	}
	switch {
	case sameDevice && first.Task == 0:
		// A device image used by one device lies with its variant.
		return fmt.Sprintf("%s/v%d/variant-%d", root, first.Version, first.Variant)
	case sameDevice:
		return fmt.Sprintf("%s/v%d/variant-%d/%d", root, first.Version, first.Variant, first.Task)
	case sameVariant:
		return fmt.Sprintf("%s/v%d/variant-%d/shared", root, first.Version, first.Variant)
	case sameVersion:
		return fmt.Sprintf("%s/v%d/shared", root, first.Version)
	}
	return root + "/shared"
}

// freeName returns name, or name with _2, _3, ... before the extension, whichever is not taken yet, and takes
// it. Names differing only in case clash too: a case-insensitive file system would unpack them onto one file.
func freeName(name string, taken map[string]bool) string {
	ext := path.Ext(name)
	if ext == name {
		ext = "" // .bashrc: a name, not an extension
	}
	stem := strings.TrimSuffix(name, ext)
	candidate := name
	for n := 2; taken[strings.ToLower(candidate)]; n++ {
		suffix := fmt.Sprintf("_%d", n)
		candidate = truncateBytes(stem, max(maxExportNameBytes-len(suffix+ext), 0)) + suffix + ext
	}
	taken[strings.ToLower(candidate)] = true
	return candidate
}

// maxExportNameBytes caps one exported file or folder name: 255 bytes is what ext4 and APFS accept for one
// name (and it stays within the 255 characters of a shown name).
const maxExportNameBytes = 255

// exportName turns a shown name into one safe path segment the import accepts: the base name in NFC, with
// backslashes and control characters as _, at most 255 bytes with the extension kept; empty is "file", "."
// and ".." are never returned. Names the import treats specially are renamed: .DS_Store (dropped as macOS
// litter), exercise.json and exercise.yaml (they would make the folder a nested task).
func exportName(name string) string {
	name = norm.NFC.String(name)
	if i := strings.LastIndex(name, "/"); i >= 0 {
		name = name[i+1:]
	}
	name = strings.Map(func(r rune) rune {
		if r == '\\' || r == '/' || unicode.IsControl(r) {
			return '_'
		}
		return r
	}, name)
	name = capExportName(name)
	switch name {
	case "":
		return "file"
	case ".":
		return "_"
	case "..":
		return "__"
	case ".DS_Store", "exercise.json", "exercise.yaml":
		return "_" + name
	}
	return name
}

// capExportName cuts a name to maxExportNameBytes on a character boundary, keeping a short extension.
func capExportName(name string) string {
	if len(name) <= maxExportNameBytes {
		return name
	}
	ext := path.Ext(name)
	if ext == name || len(ext) > maxExportNameBytes/4 {
		ext = ""
	}
	return truncateBytes(strings.TrimSuffix(name, ext), maxExportNameBytes-len(ext)) + ext
}

// truncateBytes cuts s to at most n bytes without splitting a character.
func truncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
