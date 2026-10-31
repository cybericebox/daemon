package adminAudit

import (
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
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

// secretMarks are the parts of a field name that make its value a secret: passwords, tokens, keys, codes and
// the like. The match is on the lower-cased name of the field without separators.
var secretMarks = []string{
	"password", "passwd", "passphrase", "secret", "token", "credential", "apikey", "accesskey", "privatekey",
	"signingkey", "cookie", "recoverycode", "verificationcode", "confirmationcode", "invitationcode",
	"authorization", "bearer", "salt", "hash",
}

// secretNames are field names that are a secret only when they are the whole name ("key" and "code" are too
// common inside harmless names like "keyword" or "errorCode").
var secretNames = map[string]bool{"key": true, "keys": true, "code": true, "pin": true, "otp": true, "pem": true, "csr": true}

// IsSecretField reports whether a field (the last segment of its path) holds a secret.
func IsSecretField(field string) bool {
	name := strings.ToLower(field)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	name = strings.NewReplacer("_", "", "-", "").Replace(name)
	if secretNames[name] {
		return true
	}
	for _, mark := range secretMarks {
		if strings.Contains(name, mark) {
			return true
		}
	}
	return false
}

// Diff lists the fields that differ between two values (structs, maps or JSON), comparing them as JSON.
// Nested objects are flattened to dotted paths. Values of secret fields are never copied: such a change
// only says the field changed.
func Diff(before, after any) []Change {
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
		if IsSecretField(f) {
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

// sanitize is the last gate before a change is stored: whatever the caller built, a secret field loses its
// values.
func sanitize(changes []Change) []Change {
	out := make([]Change, 0, len(changes))
	for _, c := range changes {
		if IsSecretField(c.Field) {
			c = Change{Field: c.Field, Redacted: true}
		} else {
			c.Before, c.After = shorten(c.Before), shorten(c.After)
		}
		out = append(out, c)
	}
	return out
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
