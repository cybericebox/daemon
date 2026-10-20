package exercise

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"

	"go.yaml.in/yaml/v3"
)

// ExampleArchiveFilename is the name the example archive is downloaded under.
const ExampleArchiveFilename = "example-task.cybericebox.zip"

// exampleFS holds the example task: a commented exercise.yaml (the user-facing description of the format) and
// the two files it links.
//
//go:embed example/exercise.yaml example/files/notes.txt example/files/notes-2.txt
var exampleFS embed.FS

// exampleEntries are the example's paths, relative to the zip root and to the example folder alike.
var exampleEntries = []string{"exercise.yaml", "files/notes.txt", "files/notes-2.txt"}

// ExampleArchive builds the downloadable example task: the three files at the zip root and no manifest.json,
// as an author writes an archive by hand. Imported as it is, it creates a working published task.
func ExampleArchive() ([]byte, error) {
	files := make(map[string][]byte, len(exampleEntries))
	for _, name := range exampleEntries {
		body, err := exampleFS.ReadFile("example/" + name)
		if err != nil {
			return nil, err
		}
		files[name] = body
	}
	return BuildArchiveNoManifest(files)
}

// ExampleDocuments is the example task document in both forms: the commented YAML exactly as the example archive
// holds it, and the same content as JSON (the key order of the YAML kept, comments dropped, since JSON has none).
func ExampleDocuments() (yamlText, jsonText string, err error) {
	body, err := exampleFS.ReadFile("example/exercise.yaml")
	if err != nil {
		return "", "", err
	}
	var root yaml.Node
	if err = yaml.Unmarshal(body, &root); err != nil {
		return "", "", err
	}
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return "", "", errors.New("the example is not one YAML document")
	}
	value, err := orderedValue(root.Content[0])
	if err != nil {
		return "", "", err
	}
	out, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", "", err
	}
	return string(body), string(out), nil
}

// orderedMap is a JSON object that keeps the order its keys were added in.
type orderedMap []orderedPair

type orderedPair struct {
	Key   string
	Value any
}

func (m orderedMap) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, pair := range m {
		if i > 0 {
			buf.WriteByte(',')
		}
		key, err := json.Marshal(pair.Key)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(pair.Value)
		if err != nil {
			return nil, err
		}
		buf.Write(key)
		buf.WriteByte(':')
		buf.Write(value)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

func orderedValue(node *yaml.Node) (any, error) {
	switch node.Kind {
	case yaml.MappingNode:
		out := make(orderedMap, 0, len(node.Content)/2)
		for i := 0; i+1 < len(node.Content); i += 2 {
			value, err := orderedValue(node.Content[i+1])
			if err != nil {
				return nil, err
			}
			out = append(out, orderedPair{Key: node.Content[i].Value, Value: value})
		}
		return out, nil
	case yaml.SequenceNode:
		out := make([]any, 0, len(node.Content))
		for _, item := range node.Content {
			value, err := orderedValue(item)
			if err != nil {
				return nil, err
			}
			out = append(out, value)
		}
		return out, nil
	default:
		var value any
		if err := node.Decode(&value); err != nil {
			return nil, err
		}
		return value, nil
	}
}
