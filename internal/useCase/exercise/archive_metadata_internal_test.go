package exercise

import (
	"encoding/json"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
)

func TestArchiveVersionMetadataRoundTripAndSourceIdentity(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	entry := exerciseModel.Exercise{ID: id, Name: "Current published", Description: "published description", Tags: []string{"web"}}
	versions := []exerciseModel.ExerciseVersion{
		{Status: "draft", Name: "Working rename", Description: "draft description", Tags: []string{"crypto"}},
		{Status: "published", Name: entry.Name, Description: entry.Description, Tags: entry.Tags},
		{Status: "unpublished", Name: "Historical name", Description: "historic description", Tags: []string{"misc"}},
	}
	data, err := buildV2Document(entry, versions, nil)
	require.NoError(t, err)
	var document archiveDocV2
	require.NoError(t, json.Unmarshal(data, &document))
	require.Equal(t, id.String(), document.Exercise.ID, "a new export carries the task id for safe auto-match")
	parsed, err := document.toParsed(nil)
	require.NoError(t, err)
	for i, expected := range versions {
		require.Equal(t, expected.Name, parsed.Versions[i].Name)
		require.Equal(t, expected.Description, parsed.Versions[i].Description)
		require.Equal(t, expected.Tags, parsed.Versions[i].Tags)
	}
}

func TestArchiveLegacyMetadataFallsBackForEveryVersion(t *testing.T) {
	var document archiveDocV2
	require.NoError(t, json.Unmarshal([]byte(`{"schema":"cib-exercise/1","exercise":{"name":"Known baseline","description":"Known description","tags":["web"]},"versions":[{"status":"draft","variants":[]},{"status":"published","variants":[]}]}`), &document))
	parsed, err := document.toParsed(nil)
	require.NoError(t, err)
	for _, version := range parsed.Versions {
		require.Equal(t, "Known baseline", version.Name)
		require.Equal(t, "Known description", version.Description)
		require.Equal(t, []string{"web"}, version.Tags)
	}
}
