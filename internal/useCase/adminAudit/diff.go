package adminAudit

import (
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	// maxValueRunes bounds a recorded text value; a longer one is cut.
	maxValueRunes = 300
	// maxListItems bounds a recorded list; a longer one is replaced by its length.
	maxListItems = 20
)

// Change is one field an administrative action changed. Before and After hold the values; a field whose name
// marks it as a secret carries Redacted instead and no value at all.
type Change struct {
	Field    string `json:"Field"`
	Before   any    `json:"Before,omitempty"`
	After    any    `json:"After,omitempty"`
	Redacted bool   `json:"Redacted,omitempty"`
}

// secretMark reports whether a field name (lower-cased, without separators) contains a part that makes its value a
// secret: passwords, tokens, keys, codes and the like.
func secretMark(name string) bool {
	for _, mark := range []string{
		"password", "passwd", "passphrase", "secret", "token", "credential", "apikey", "accesskey", "privatekey",
		"signingkey", "cookie", "recoverycode", "verificationcode", "confirmationcode", "invitationcode",
		"authorization", "bearer", "salt", "hash",
	} {
		if strings.Contains(name, mark) {
			return true
		}
	}
	return false
}

// secretName reports whether a whole field name is a secret: "key" and "code" are too common inside harmless names
// like "keyword" or "errorCode" to be matched as parts.
func secretName(name string) bool {
	switch name {
	case "key", "keys", "code", "pin", "otp", "pem", "csr":
		return true
	}
	return false
}

// IsSecretField reports whether a field (the last segment of its path) holds a secret.
func IsSecretField(field string) bool {
	name := strings.ToLower(field)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	name = strings.NewReplacer("_", "", "-", "").Replace(name)
	return secretName(name) || secretMark(name)
}

// Diff lists the fields that differ between two values (structs, maps, arrays or JSON), comparing them as JSON.
// Nested objects and arrays are walked to their leaves, named by dotted paths with the array position in
// brackets ("Tags[1]"). Only a field named in allowed (a path without array positions: "Tags") has its values
// recorded; every other changed field only says that it changed, so a new field can never leak by default.
// A secret field keeps its values out even when it is allowed.
func Diff(before, after any, allowed ...string) []Change {
	b, a := flatten(before), flatten(after)
	fields := make([]string, 0, len(b)+len(a))
	for f := range b {
		fields = append(fields, f)
	}
	for f := range a {
		if _, ok := b[f]; !ok {
			fields = append(fields, f)
		}
	}
	sort.Strings(fields)
	var out []Change
	for _, f := range fields {
		bv, bok := b[f]
		av, aok := a[f]
		if bok && aok && reflect.DeepEqual(bv, av) {
			continue
		}
		if IsSecretField(f) || !slices.Contains(allowed, withoutPositions(f)) {
			out = append(out, Change{Field: f, Redacted: true})
			continue
		}
		c := Change{Field: f}
		if bok {
			c.Before = shorten(bv)
		}
		if aok {
			c.After = shorten(av)
		}
		out = append(out, c)
	}
	return out
}

// withoutPositions drops the array positions of a path: "Tags[2].Name" is "Tags.Name".
func withoutPositions(path string) string {
	for {
		i := strings.IndexByte(path, '[')
		if i < 0 {
			return path
		}
		j := strings.IndexByte(path[i:], ']')
		if j < 0 {
			return path
		}
		path = path[:i] + path[i+j+1:]
	}
}

// sanitize is the last gate before a change is stored: whatever the caller built, a secret field loses its
// values, and a structured value loses the secret keys inside it.
func sanitize(changes []Change) []Change {
	out := make([]Change, 0, len(changes))
	for _, c := range changes {
		if c.Redacted || IsSecretField(c.Field) {
			c = Change{Field: c.Field, Redacted: true}
		} else {
			c.Before, c.After = shorten(scrub(c.Before)), shorten(scrub(c.After))
		}
		out = append(out, c)
	}
	return out
}

// scrub removes the values of secret keys at any depth of a structured value.
func scrub(v any) any {
	switch x := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, child := range x {
			if IsSecretField(k) {
				out[k] = "[hidden]"
			} else {
				out[k] = scrub(child)
			}
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i, child := range x {
			out[i] = scrub(child)
		}
		return out
	}
	return v
}

func flatten(v any) map[string]any {
	out := map[string]any{}
	if v == nil {
		return out
	}
	raw, err := json.Marshal(v)
	if err != nil {
		return out
	}
	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		return out
	}
	walk("", decoded, out)
	return out
}

func walk(path string, v any, out map[string]any) {
	if list, isList := v.([]any); isList && len(list) > 0 {
		if path == "" {
			path = "Value"
		}
		for i, child := range list {
			walk(path+"["+strconv.Itoa(i)+"]", child, out)
		}
		return
	}
	obj, ok := v.(map[string]any)
	if !ok {
		if path == "" {
			path = "Value"
		}
		out[path] = v
		return
	}
	for k, child := range obj {
		next := k
		if path != "" {
			next = path + "." + k
		}
		walk(next, child, out)
	}
}

// shorten keeps a recorded value small: long text is cut, a long list is replaced by its length.
func shorten(v any) any {
	switch x := v.(type) {
	case string:
		if utf8.RuneCountInString(x) > maxValueRunes {
			return string([]rune(x)[:maxValueRunes]) + "…"
		}
	case []any:
		if len(x) > maxListItems {
			return fmt.Sprintf("[%d items]", len(x))
		}
		out := make([]any, len(x))
		for i, item := range x {
			out[i] = shorten(item)
		}
		return out
	}
	return v
}
