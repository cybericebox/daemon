package exercise

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"time"

	"go.yaml.in/yaml/v3"
)

// maxExerciseDocBytes caps one exercise document (JSON or YAML) before it is parsed.
const maxExerciseDocBytes = 4 << 20

// maxYAMLDepth bounds nesting so the tree walk cannot be driven deep.
const maxYAMLDepth = 64

var yamlLineRE = regexp.MustCompile(`line (\d+)`)

// yamlToJSON turns one YAML document into the JSON the strict decoder reads. Comments vanish in
// the YAML parser. Anchors and aliases are refused outright (a document needs none), which also
// shuts out expansion bombs. The YAML type of every scalar is kept: a number stays a number, a
// bool a bool, so a string field that receives one fails strict decoding by name. Timestamps
// are refused rather than turned into strings, and so are non-string keys and more than one
// document.
func yamlToJSON(data []byte) ([]byte, error) {
	if len(data) > maxExerciseDocBytes {
		return nil, archiveInvalid("the document is too large")
	}
	var root yaml.Node
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	if err := decoder.Decode(&root); err != nil {
		return nil, yamlFailure(err)
	}
	var extra yaml.Node
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, archiveInvalid("the document must be a single YAML document")
	}
	value, err := yamlNodeValue(&root, "", 0)
	if err != nil {
		return nil, err
	}
	out, err := json.Marshal(value)
	if err != nil {
		return nil, archiveInvalid("the document holds a value JSON cannot carry")
	}
	return out, nil
}

// yamlFailure reports a YAML syntax error by line only; the library text stays out.
func yamlFailure(err error) error {
	if m := yamlLineRE.FindStringSubmatch(err.Error()); m != nil {
		if line, convErr := strconv.Atoi(m[1]); convErr == nil {
			return archiveInvalid(fmt.Sprintf("invalid YAML at line %d", line))
		}
	}
	return archiveInvalid("invalid YAML")
}

func yamlNodeError(node *yaml.Node, path, what string) error {
	where := path
	if where == "" {
		where = "the document"
	}
	return archiveInvalid(fmt.Sprintf("%s at line %d: %s", where, node.Line, what))
}

func yamlNodeValue(node *yaml.Node, path string, depth int) (any, error) {
	if depth > maxYAMLDepth {
		return nil, archiveInvalid("the document is nested too deeply")
	}
	switch node.Kind {
	case yaml.DocumentNode:
		if len(node.Content) != 1 {
			return nil, archiveInvalid("the document is empty")
		}
		return yamlNodeValue(node.Content[0], path, depth+1)
	case yaml.AliasNode:
		return nil, yamlNodeError(node, path, "anchors and aliases are not supported")
	case yaml.SequenceNode:
		items := make([]any, 0, len(node.Content))
		for i, child := range node.Content {
			if child.Anchor != "" {
				return nil, yamlNodeError(child, path, "anchors and aliases are not supported")
			}
			v, err := yamlNodeValue(child, joinYAMLPath(path, strconv.Itoa(i+1)), depth+1)
			if err != nil {
				return nil, err
			}
			items = append(items, v)
		}
		return items, nil
	case yaml.MappingNode:
		object := make(map[string]any, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			key, val := node.Content[i], node.Content[i+1]
			if key.Kind != yaml.ScalarNode || (key.Tag != "!!str" && key.Tag != "") {
				return nil, yamlNodeError(key, path, "keys must be plain text")
			}
			if val.Anchor != "" || key.Anchor != "" {
				return nil, yamlNodeError(val, path, "anchors and aliases are not supported")
			}
			if _, dup := object[key.Value]; dup {
				return nil, yamlNodeError(key, path, fmt.Sprintf("key %q is repeated", key.Value))
			}
			v, err := yamlNodeValue(val, joinYAMLPath(path, key.Value), depth+1)
			if err != nil {
				return nil, err
			}
			object[key.Value] = v
		}
		return object, nil
	case yaml.ScalarNode:
		return yamlScalarValue(node, path)
	}
	return nil, yamlNodeError(node, path, "unsupported YAML node")
}

func joinYAMLPath(path, key string) string {
	if path == "" {
		return key
	}
	return path + "." + key
}

// yamlScalarValue decodes one scalar with the library's own resolution (YAML 1.2 core booleans:
// only true/false, so `no`/`yes`/`on` stay text) and refuses what JSON would turn into another value.
func yamlScalarValue(node *yaml.Node, path string) (any, error) {
	if node.Anchor != "" {
		return nil, yamlNodeError(node, path, "anchors and aliases are not supported")
	}
	var v any
	if err := node.Decode(&v); err != nil {
		return nil, yamlNodeError(node, path, "invalid value")
	}
	switch typed := v.(type) {
	case time.Time:
		return nil, yamlNodeError(node, path, "a date or time must be written in quotes")
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return nil, yamlNodeError(node, path, "not a valid number")
		}
	case []byte:
		return nil, yamlNodeError(node, path, "binary values are not supported")
	}
	return v, nil
}
