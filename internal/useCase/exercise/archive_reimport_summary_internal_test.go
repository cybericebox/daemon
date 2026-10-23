package exercise

import (
	"context"
	"encoding/json"
	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	imageRegistryUseCase "github.com/cybericebox/daemon/internal/useCase/imageRegistry"
	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"
	"strings"
	"testing"
)

func summaryVersion(t *testing.T) (exerciseModel.ExerciseVersion, uuid.UUID) {
	t.Helper()
	variant, task, device, hint, file := freshID(), freshID(), freshID(), freshID(), freshID()
	return exerciseModel.ExerciseVersion{Name: "Same name", Tags: []string{"web"}, Variants: []exerciseModel.Variant{{ID: variant, Index: 3, Tasks: []exerciseModel.Task{{ID: task, Name: "Task name", Flag: []string{"ICE{flag}"}, LinkedDeviceID: uuid.NullUUID{UUID: device, Valid: true}, Attachments: []exerciseModel.AttachmentRef{{FileID: file, Name: "solve.zip"}}, Placeholders: []exerciseModel.Placeholder{{Key: task.String(), Kind: exerciseModel.PlaceholderExternalLink, DeviceName: "web"}}, Description: json.RawMessage(`{"root":{"children":[{"type":"variable","varName":"` + task.String() + `"}]}}`), Hints: []exerciseModel.Hint{{ID: hint, Level: exerciseModel.HintLevelNudge, Text: `{"root":{"children":[{"varName":"` + task.String() + `","type":"variable"}]}}`}}}}, Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{ID: device, Name: "web", Type: exerciseModel.DeviceTypeContainer, Image: "external:image", EnvVars: []exerciseModel.EnvVar{{Name: "PASSWORD", Secret: true, Value: "private value"}}}}, Connections: []exerciseModel.Connection{{Endpoints: []exerciseModel.Endpoint{{Kind: exerciseModel.EndpointDevice, DeviceID: device}}}}, VisualRender: json.RawMessage(`{"positions":{"` + device.String() + `":{"x":4,"y":6}}}`)}}}}, file
}

func TestImportSummaryCanonicalizesGeneratedReferencesFilesAndOpenedSecrets(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	cipher, err := secret.New(strings.Repeat("11", 32))
	require.NoError(t, err)
	u := &ExerciseUseCase{exercises: exerciseRepo.New(q), cipher: cipher}
	source, sourceFile := summaryVersion(t)
	target, targetFile := summaryVersion(t)
	ciphertext, err := cipher.EncryptWithContext([]byte("private value"), envSecretContext(target.Variants[0].ID, "web", "PASSWORD"))
	require.NoError(t, err)
	target.Variants[0].Topology.Devices[0].EnvVars[0].Value = ciphertext
	q.EXPECT().ListFileHashes(gomock.Any(), []uuid.UUID{targetFile}).Return([]postgres.ListFileHashesRow{{ID: targetFile, ContentHash: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}}, nil).AnyTimes()
	parsed := ParsedExerciseArchive{FilePaths: map[uuid.UUID]string{sourceFile: "files/solve.zip"}, Files: map[string][]byte{"files/solve.zip": []byte("hello")}}
	summary, err := u.importSummary(context.Background(), parsed, source, target, nil)
	require.NoError(t, err)
	require.Zero(t, summary.Variants.Changed, "new IDs, placeholder keys and ciphertext are not content changes")
	require.Zero(t, summary.FlagsChanged)
	require.Equal(t, ImportFileChange{}, summary.Files)
	require.False(t, summary.MetadataChanged)
	require.Equal(t, ciphertext, target.Variants[0].Topology.Devices[0].EnvVars[0].Value, "opening comparison secrets never mutates stored content")
	source.Variants[0].Tasks[0].Flag = []string{"ICE{different}"}
	source.Variants[0].Tasks[0].Attachments[0].Name = "renamed.zip"
	source.Name = "Draft rename"
	summary, err = u.importSummary(context.Background(), parsed, source, target, nil)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Variants.Changed)
	require.Equal(t, 1, summary.FlagsChanged)
	require.Equal(t, ImportFileChange{Added: 1, Removed: 1}, summary.Files)
	require.True(t, summary.MetadataChanged)
}

func TestImportSummaryCountsPairedStructureAndUniqueEmbeddedImages(t *testing.T) {
	u := &ExerciseUseCase{}
	source := exerciseModel.ExerciseVersion{Name: "Same", Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{{Name: "one"}, {Name: "two"}}}, {Tasks: []exerciseModel.Task{{Name: "one"}, {Name: "two"}}}}}
	target := exerciseModel.ExerciseVersion{Name: "Same", Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{{Name: "one"}}}}}
	parsed := ParsedExerciseArchive{Images: []ParsedArchiveImage{{Marker: "a"}, {Marker: "b"}, {Marker: "c"}}}
	images := map[string]imageRegistryUseCase.ArchiveImageInspection{"a": {Digest: "sha256:same", Reused: true}, "b": {Digest: "sha256:same", Reused: true}, "c": {Digest: "sha256:new"}}
	summary, err := u.importSummary(context.Background(), parsed, source, target, images)
	require.NoError(t, err)
	require.Equal(t, ImportCountChange{Added: 1, Changed: 1}, summary.Variants)
	require.Equal(t, ImportSubtaskCount{Before: 1, After: 2}, summary.Subtasks)
	require.Equal(t, ImportImageChange{New: 1, Reused: 1}, summary.Images)
}

func TestImportSummaryRebuildsLinkedVariantsAndNormalizesImageNames(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	u := &ExerciseUseCase{exercises: exerciseRepo.New(q)}
	source, sourceFile := summaryVersion(t)
	target, targetFile := summaryVersion(t)
	source.Variants[0].Topology.Devices[0].EnvVars = nil
	target.Variants[0].Topology.Devices[0].EnvVars = nil
	digest := "sha256:" + strings.Repeat("a", 64)
	source.Variants[0].Topology.Devices[0].Image = "image-file-marker"
	target.Variants[0].Topology.Devices[0].Image = "platform:old-display-name@" + digest
	for _, v := range []*exerciseModel.ExerciseVersion{&source, &target} {
		base := v.Variants[0]
		v.Variants = append(v.Variants, exerciseModel.Variant{ID: freshID(), Index: 7, LinkedTo: uuid.NullUUID{UUID: base.ID, Valid: true}, Tasks: []exerciseModel.Task{{ID: base.Tasks[0].ID, Flag: []string{"ICE{other}"}}}})
	}
	q.EXPECT().ListFileHashes(gomock.Any(), []uuid.UUID{targetFile}).Return([]postgres.ListFileHashesRow{{ID: targetFile, ContentHash: "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"}}, nil)
	parsed := ParsedExerciseArchive{FilePaths: map[uuid.UUID]string{sourceFile: "files/a"}, Files: map[string][]byte{"files/a": []byte("hello")}}
	summary, err := u.importSummary(context.Background(), parsed, source, target, map[string]imageRegistryUseCase.ArchiveImageInspection{"image-file-marker": {Digest: digest}})
	require.NoError(t, err)
	require.Equal(t, ImportCountChange{}, summary.Variants)
	require.Zero(t, summary.FlagsChanged)
}

func TestImportSummaryCountsAWeightChangeAsAChangedVariant(t *testing.T) {
	u := &ExerciseUseCase{}
	variants := func(baseWeight, linkedWeight int) exerciseModel.ExerciseVersion {
		base := exerciseModel.Variant{ID: freshID(), Weight: baseWeight, Tasks: []exerciseModel.Task{{Name: "one"}}}
		linked := exerciseModel.Variant{ID: freshID(), Weight: linkedWeight, LinkedTo: uuid.NullUUID{UUID: base.ID, Valid: true}, Tasks: []exerciseModel.Task{{}}}
		return exerciseModel.ExerciseVersion{Name: "Same", Variants: []exerciseModel.Variant{base, linked}}
	}
	summary, err := u.importSummary(context.Background(), ParsedExerciseArchive{}, variants(2, 3), variants(2, 3), nil)
	require.NoError(t, err)
	require.Zero(t, summary.Variants.Changed)
	summary, err = u.importSummary(context.Background(), ParsedExerciseArchive{}, variants(1, 3), variants(2, 3), nil)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Variants.Changed, "the base's weight changed, the linked variant keeps its own")
	summary, err = u.importSummary(context.Background(), ParsedExerciseArchive{}, variants(2, 0), variants(2, 3), nil)
	require.NoError(t, err)
	require.Equal(t, 1, summary.Variants.Changed, "a linked variant's own weight changed")
	summary, err = u.importSummary(context.Background(), ParsedExerciseArchive{}, variants(1, 0), variants(0, 1), nil)
	require.NoError(t, err)
	require.Zero(t, summary.Variants.Changed, "an unset weight is weight 1")
}
