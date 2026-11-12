package exercise

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// The YAML keeps the order of the keys, writes a text block as a block, quotes the text YAML would read as another type,
// and reads back as the same document through the archive's own reader.
func TestJSONDocumentToYAMLRoundTrips(t *testing.T) {
	document := []byte(`{"schema":"cib-exercise/1.0","exercise":{"name":"Task"},"versions":[{"status":"draft","variants":[{"tasks":[{"name":"A","description":"line one\nline two","check":{"answer":{"type":"text","values":["true","007","1.5","","plain"]}}}],"topology":{"devices":[{"name":"web","type":"node","access":[{"kind":"http","port":80}]}]}}]}]}`)
	text, err := jsonDocumentToYAML(document)
	require.NoError(t, err)
	require.Contains(t, string(text), "description: |-\n")
	require.Less(t, indexOf(text, "schema:"), indexOf(text, "exercise:"))
	require.Less(t, indexOf(text, "type: text"), indexOf(text, "values:"))

	decoded, err := decodeDocument("exercise.yaml", text)
	require.NoError(t, err)
	task := decoded.Versions[0].Variants[0].Tasks[0]
	require.JSONEq(t, `"line one\nline two"`, string(task.Description))
	require.Equal(t, []string{"true", "007", "1.5", "", "plain"}, task.Check.Answer.Values, "texts that look like other types stay texts")
	require.Equal(t, int32(80), decoded.Versions[0].Variants[0].Topology.Devices[0].Access[0].Port)
}

func indexOf(text []byte, part string) int {
	for i := 0; i+len(part) <= len(text); i++ {
		if string(text[i:i+len(part)]) == part {
			return i
		}
	}
	return -1
}

// A file is written at the archive path the export gives it, from the stored content hash (equal contents share one path).
func TestDraftFilePathsFollowTheExportLayout(t *testing.T) {
	a, b, c := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	version := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{{
		Tasks: []exerciseModel.Task{{Attachments: []exerciseModel.AttachmentRef{{FileID: a, Name: "notes.txt"}, {FileID: b, Name: "copy.txt"}}}},
		Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{Name: "web", Bootstrap: exerciseModel.Bootstrap{
			Files: []exerciseModel.BootstrapFile{{Path: "/x", Source: &exerciseModel.AttachmentRef{FileID: c, Name: "config.yaml"}}},
		}}}},
	}}}
	paths := draftFilePaths(version, map[uuid.UUID]string{a: "h1", b: "h1", c: "h2"})
	require.Len(t, paths, 3)
	require.Equal(t, paths[a], paths[b], "equal contents share one path")
	require.NotEqual(t, paths[a], paths[c])
	for _, p := range paths {
		require.Regexp(t, `^files/`, p)
	}
}
