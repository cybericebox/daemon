package secret

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// currentFile names, inside a key directory, the file that holds the id of the sealing key.
const currentFile = "CURRENT"

// keyFileSuffix ends the name of every key file: <key id>.key.
const keyFileSuffix = ".key"

// SpecFromDir reads a key directory and returns the keyring in the form New accepts ("id:hex,id:hex", the sealing key
// first). The directory holds one file per key, named <id>.key and containing the 64-hex-char key, and a file CURRENT
// with the id of the key that seals; the other keys only open. Names that start with a dot (the links a mounted
// Kubernetes Secret keeps) are ignored. It refuses what is ambiguous or unsafe: a missing or empty directory, a missing
// CURRENT, a CURRENT that names no key file, a key file readable by its group or by everybody, a file or a directory
// that is not a key, an id or a key that is malformed. Messages name the files, never their content.
func SpecFromDir(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("secret: the key directory %s cannot be read: %w", dir, err)
	}
	keys := map[string]string{}
	var current string
	for _, entry := range entries {
		name := entry.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		path := filepath.Join(dir, name)
		info, err := os.Stat(path) // follows the links of a mounted Secret
		if err != nil {
			return "", fmt.Errorf("secret: %s cannot be read: %w", path, err)
		}
		if !info.Mode().IsRegular() {
			return "", fmt.Errorf("secret: %s is not a regular file; the key directory holds only <id>.key files and CURRENT", path)
		}
		switch {
		case name == currentFile:
			raw, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("secret: %s cannot be read: %w", path, err)
			}
			current = strings.TrimSpace(string(raw))
		case strings.HasSuffix(name, keyFileSuffix):
			id := strings.TrimSuffix(name, keyFileSuffix)
			if !keyIDPattern.MatchString(id) {
				return "", fmt.Errorf("secret: %s: a key id is 1-16 characters of a-z, 0-9, _ and -", path)
			}
			if info.Mode().Perm()&0o077 != 0 {
				return "", fmt.Errorf("secret: %s is readable by its group or by everybody (mode %04o); make it 0400 or 0600", path, info.Mode().Perm())
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				return "", fmt.Errorf("secret: %s cannot be read: %w", path, err)
			}
			keys[id] = strings.TrimSpace(string(raw))
		default:
			return "", fmt.Errorf("secret: %s is not expected in a key directory (only <id>.key files and CURRENT)", path)
		}
	}
	if len(keys) == 0 {
		return "", fmt.Errorf("secret: the key directory %s has no <id>.key file", dir)
	}
	if current == "" {
		return "", fmt.Errorf("secret: the key directory %s has no %s file naming the sealing key", dir, currentFile)
	}
	if _, ok := keys[current]; !ok {
		return "", fmt.Errorf("secret: %s/%s names the key %q but there is no %s%s in the directory", dir, currentFile, current, current, keyFileSuffix)
	}
	ids := make([]string, 0, len(keys))
	for id := range keys {
		if id != current {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	ids = append([]string{current}, ids...)
	parts := make([]string, len(ids))
	for i, id := range ids {
		// One key at a time, so a message can name the file that is wrong without printing a key.
		if _, err := New(id + ":" + keys[id]); err != nil {
			return "", fmt.Errorf("secret: %s/%s%s: the key must be 64 hex chars (32 bytes)", dir, id, keyFileSuffix)
		}
		parts[i] = id + ":" + keys[id]
	}
	return strings.Join(parts, ","), nil
}
