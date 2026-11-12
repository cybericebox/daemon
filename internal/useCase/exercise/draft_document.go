package exercise

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/gofrs/uuid"
	"go.yaml.in/yaml/v3"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// MaxDraftDocumentBytes is the largest document DraftDocument answers (the draft itself is bounded by the request limits of
// the draft save; a larger one is refused as too large, never cut).
const MaxDraftDocumentBytes = 8 << 20

// DraftDocument is the working copy of an exercise (the draft, else the published content) as the exercise document, in
// YAML: the same document the archive carries (schema cib-exercise/1.0, one version, the keys in the order of the
// specification), for the editor's «Документ» dialog. It reads, never writes:
//   - secret env values are empty (secret: true stays), as everywhere in a response;
//   - a reader who may not see flags gets the document without answer values and conditions, like GetWorkingCopy;
//   - a file of the exercise is written as the same archive path the export gives it (files/...), by its content hash; the
//     bytes are not part of it, and no image file is (a platform image is written by its reference).
func (u *ExerciseUseCase) DraftDocument(ctx context.Context, exerciseID uuid.UUID, withFlags bool) ([]byte, error) {
	view, err := u.GetWorkingCopy(ctx, exerciseID)
	if err != nil {
		return nil, err
	}
	if !withFlags {
		view = WithoutNotes(WithoutFlags(view))
	}
	e, err := u.exercises.GetByID(ctx, exerciseID)
	if err != nil {
		return nil, err
	}
	version := exerciseModel.ExerciseVersion{
		ID: view.ID, ExerciseID: view.ExerciseID, Status: exerciseModel.VersionStatus(view.Status),
		Name: view.Name, Description: view.Description, Tags: view.Tags, AdminNote: view.AdminNote, Label: view.Label,
		Variants: maskSecrets(view.Variants),
	}
	document, err := buildV2DocumentWithImages(e, []exerciseModel.ExerciseVersion{version}, draftFilePaths(version, view.FileHashes), nil)
	if err != nil {
		return nil, err
	}
	text, err := jsonDocumentToYAML(document)
	if err != nil {
		return nil, err
	}
	return text, checkArchiveSize(int64(len(text)), MaxDraftDocumentBytes)
}

// draftFilePaths gives every file of the version the archive path the export would give it, without reading any bytes: the
// content hash comes from the stored records (hashes by file id). A file whose hash is not known is told apart by its id.
func draftFilePaths(version exerciseModel.ExerciseVersion, hashes map[uuid.UUID]string) map[uuid.UUID]string {
	hashOf := func(id uuid.UUID) string {
		if hash := hashes[id]; hash != "" {
			return hash
		}
		return id.String()
	}
	var uses []fileUse
	files := map[uuid.UUID]string{}
	for xi, variant := range version.Variants {
		for ti, task := range variant.Tasks {
			for ai, ref := range task.Attachments {
				files[ref.FileID] = hashOf(ref.FileID)
				uses = append(uses, fileUse{Hash: hashOf(ref.FileID), Name: ref.Name, Version: 1, Variant: xi + 1, Task: ti + 1, Attachment: ai + 1})
			}
		}
		for di, device := range variant.Topology.Devices {
			for fi, file := range device.Bootstrap.Files {
				if file.Source == nil {
					continue
				}
				files[file.Source.FileID] = hashOf(file.Source.FileID)
				uses = append(uses, fileUse{Hash: hashOf(file.Source.FileID), Name: file.Source.Name, Version: 1, Variant: xi + 1, Device: di + 1, Attachment: fi + 1})
			}
		}
	}
	byHash := assignExportPaths("files", uses)
	paths := make(map[uuid.UUID]string, len(files))
	for id, hash := range files {
		paths[id] = byHash[hash]
	}
	return paths
}

// jsonDocumentToYAML re-writes the JSON document as YAML with its keys in the order they were written.
func jsonDocumentToYAML(document []byte) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(document))
	decoder.UseNumber()
	node, err := yamlNodeOf(decoder)
	if err != nil {
		return nil, err
	}
	var out bytes.Buffer
	encoder := yaml.NewEncoder(&out)
	encoder.SetIndent(2)
	if err = encoder.Encode(node); err != nil {
		return nil, err
	}
	if err = encoder.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// yamlNodeOf reads one JSON value into a YAML node, objects keeping their key order. Text with a line break is a literal
// block; text YAML would read as another type (a number, true, null) is quoted.
func yamlNodeOf(decoder *json.Decoder) (*yaml.Node, error) {
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	switch value := token.(type) {
	case json.Delim:
		switch value {
		case '{':
			node := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			for decoder.More() {
				key, keyErr := decoder.Token()
				name, ok := key.(string)
				if keyErr != nil || !ok {
					return nil, errors.New("the document has a key that is not text")
				}
				child, childErr := yamlNodeOf(decoder)
				if childErr != nil {
					return nil, childErr
				}
				node.Content = append(node.Content, &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: name}, child)
			}
			_, err = decoder.Token()
			return node, err
		case '[':
			node := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
			for decoder.More() {
				child, childErr := yamlNodeOf(decoder)
				if childErr != nil {
					return nil, childErr
				}
				node.Content = append(node.Content, child)
			}
			_, err = decoder.Token()
			return node, err
		}
		return nil, errors.New("unexpected delimiter in the document")
	case string:
		node := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: value}
		switch {
		case strings.Contains(value, "\n") && value != "\n":
			node.Style = yaml.LiteralStyle
		case needsQuotes(value):
			node.Style = yaml.DoubleQuotedStyle
		}
		return node, nil
	case json.Number:
		tag := "!!int"
		if strings.ContainsAny(value.String(), ".eE") {
			tag = "!!float"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: value.String()}, nil
	case bool:
		tag, text := "!!bool", "false"
		if value {
			text = "true"
		}
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: tag, Value: text}, nil
	case nil:
		return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}, nil
	}
	return nil, io.ErrUnexpectedEOF
}

// needsQuotes reports a text a plain YAML scalar would not read back as the same text.
func needsQuotes(s string) bool {
	if s == "" {
		return true
	}
	var plain any
	if err := yaml.Unmarshal([]byte(s), &plain); err != nil {
		return true
	}
	text, ok := plain.(string)
	return !ok || text != s
}
