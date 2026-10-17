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
}

// assignExportPaths places every content (hash) once under root: a content used by one subtask goes to
// <root>/v<V>/variant-<N>/<T>/, one used by several goes to the shared/ folder of the lowest level covering all
// its uses (the variant, the version or root itself). The file takes the name of its first use; a name taken in
// the folder gets _2, _3 before the extension. The uses are ordered first, so the result does not depend on the
// order they come in.
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
	sameVersion, sameVariant, sameTask := true, true, true
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
	}
	switch {
	case sameTask:
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
		room := maxExportNameRunes - utf8.RuneCountInString(suffix+ext)
		candidate = truncateRunes(stem, max(room, 0)) + suffix + ext
	}
	taken[strings.ToLower(candidate)] = true
	return candidate
}

// maxExportNameRunes caps one exported file or folder name, like the shown name of an attachment.
const maxExportNameRunes = 255

// exportName turns a shown name into one safe path segment the import accepts: the base name in NFC, with
// backslashes and control characters as _; empty is "file", "." and ".." are never returned, and .DS_Store
// (which the import drops as macOS litter) is renamed.
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
	name = truncateRunes(name, maxExportNameRunes)
	switch name {
	case "":
		return "file"
	case ".":
		return "_"
	case "..":
		return "__"
	case ".DS_Store":
		return "_.DS_Store"
	}
	return name
}
