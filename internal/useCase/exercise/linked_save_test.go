package exercise_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/secret"
)

var (
	saveExerciseID = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000c000")
	saveBaseID     = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000c001")
	saveLinkedID   = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000c002")
	saveThirdID    = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000c003")
	saveTaskID     = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000c011")
	saveDeviceID   = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000c021")
	saveDraftID    = uuid.FromStringOrNil("0192f0a0-0000-7000-8000-00000000c031")
)

const saveDevice, saveEnv = "web", "PASS"

// saveVariant: one task, one device with the secret env PASS.
func saveVariant(id uuid.UUID, name, description, flag, pass string) exerciseModel.Variant {
	task := exerciseModel.Task{ID: saveTaskID, Name: name, Difficulty: exerciseModel.DifficultyEasy}
	if description != "" {
		task.Description = json.RawMessage(description)
	}
	if flag != "" {
		task.Flag = []string{flag}
	}
	return exerciseModel.Variant{
		ID:    id,
		Tasks: []exerciseModel.Task{task},
		Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{
			ID: saveDeviceID, Name: saveDevice, Type: exerciseModel.DeviceTypeContainer, Image: "img",
			EnvVars: []exerciseModel.EnvVar{{Name: saveEnv, Value: pass, Secret: true}},
		}}},
	}
}

func linkTo(v exerciseModel.Variant, base uuid.UUID) exerciseModel.Variant {
	v.LinkedTo = uuid.NullUUID{UUID: base, Valid: true}
	return v
}

type linkedSave struct {
	uc      *exercise.ExerciseUseCase
	q       *postgresMocks.MockQuerier
	cipher  *secret.Cipher
	written *postgres.UpsertExerciseDraftParams
	called  bool
}

// newLinkedSave mocks an editable exercise whose stored draft is `stored` (nil = no draft, nothing published).
func newLinkedSave(t *testing.T, stored []exerciseModel.Variant) *linkedSave {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	cipher, err := secret.New(testKey)
	require.NoError(t, err)
	s := &linkedSave{q: q, cipher: cipher, written: &postgres.UpsertExerciseDraftParams{}}
	s.uc = newTestExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: newFakeMedia()})
	q.EXPECT().GetExerciseByID(gomock.Any(), saveExerciseID).Return(postgres.Exercise{ID: saveExerciseID}, nil).AnyTimes()
	if stored == nil {
		q.EXPECT().GetDraftVersion(gomock.Any(), saveExerciseID).Return(postgres.ExerciseVersion{}, errNoRows()).AnyTimes()
	} else {
		q.EXPECT().GetDraftVersion(gomock.Any(), saveExerciseID).Return(postgres.ExerciseVersion{
			ID: saveDraftID, ExerciseID: saveExerciseID, Status: "draft", Variants: mustMarshal(t, stored),
		}, nil).AnyTimes()
	}
	q.EXPECT().UpsertExerciseDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpsertExerciseDraftParams) (postgres.UpsertExerciseDraftRow, error) {
			*s.written = arg
			s.called = true
			return postgres.UpsertExerciseDraftRow{ID: arg.NewID, ExerciseID: arg.ExerciseID, Status: "draft",
				Variants: arg.Variants, CreatedAt: arg.CreatedAt}, nil
		}).AnyTimes()
	return s
}

// seal encrypts like the use case does (the same key as newLinkedSave's cipher).
func seal(t *testing.T, variantID uuid.UUID, plain string) string {
	t.Helper()
	cipher, err := secret.New(testKey)
	require.NoError(t, err)
	sealed, err := cipher.EncryptWithContext([]byte(plain), exercise.EnvSecretContext(variantID, saveDevice, saveEnv))
	require.NoError(t, err)
	return sealed
}

func (s *linkedSave) open(t *testing.T, variantID uuid.UUID, sealed string) string {
	t.Helper()
	plain, err := s.cipher.DecryptWithContext(sealed, exercise.EnvSecretContext(variantID, saveDevice, saveEnv))
	require.NoError(t, err)
	return string(plain)
}

func (s *linkedSave) save(t *testing.T, in exercise.SaveDraftInput) []exerciseModel.Variant {
	t.Helper()
	_, err := s.uc.SaveDraft(context.Background(), saveExerciseID, in)
	require.NoError(t, err)
	var written []exerciseModel.Variant
	require.NoError(t, json.Unmarshal(s.written.Variants, &written))
	return written
}

func secretOf(v exerciseModel.Variant) string { return v.Topology.Devices[0].EnvVars[0].Value }

func TestSaveDraft_RewritesLinkedVariantFromBase(t *testing.T) {
	s := newLinkedSave(t, nil)
	base := saveVariant(saveBaseID, "Нове", `{"d":"D"}`, "ICE{a}", "")
	base.Tasks[0].Hints = []exerciseModel.Hint{{Text: "h", Level: exerciseModel.HintLevelNudge}}
	linked := linkTo(saveVariant(saveLinkedID, "Старе", `{"d":"інше"}`, "ICE{b}", ""), saveBaseID)

	written := s.save(t, exercise.SaveDraftInput{Variants: []exerciseModel.Variant{base, linked}})

	require.Len(t, written, 2)
	got := written[1]
	require.Equal(t, "Нове", got.Tasks[0].Name)
	require.JSONEq(t, `{"d":"D"}`, string(got.Tasks[0].Description))
	require.Len(t, got.Tasks[0].Hints, 1)
	require.Equal(t, "h", got.Tasks[0].Hints[0].Text)
	require.Equal(t, []string{"ICE{b}"}, got.Tasks[0].Flag)
	require.Equal(t, uuid.NullUUID{UUID: saveBaseID, Valid: true}, got.LinkedTo)
}

func TestSaveDraft_NewLinkedVariantInheritsNewBaseTask(t *testing.T) {
	s := newLinkedSave(t, nil)
	base := saveVariant(saveBaseID, "Нове", "", "", "")
	base.Tasks = append(base.Tasks, exerciseModel.Task{Name: "Друге", Difficulty: exerciseModel.DifficultyEasy,
		Placeholders: []exerciseModel.Placeholder{{Kind: exerciseModel.PlaceholderVPNSubnet}}})
	linked := linkTo(exerciseModel.Variant{ID: saveLinkedID}, saveBaseID)

	written := s.save(t, exercise.SaveDraftInput{Variants: []exerciseModel.Variant{base, linked}})

	require.Len(t, written[1].Tasks, 2)
	newTask, inherited := written[0].Tasks[1], written[1].Tasks[1]
	require.NotEqual(t, uuid.Nil, newTask.ID)
	require.Equal(t, newTask.ID, inherited.ID)
	require.NotEmpty(t, newTask.Placeholders[0].Key)
	require.Equal(t, newTask.Placeholders[0].Key, inherited.Placeholders[0].Key)
}

func TestSaveDraft_RejectsChainLink(t *testing.T) {
	s := newLinkedSave(t, nil)
	base := saveVariant(saveBaseID, "Нове", "", "", "")
	linked := linkTo(saveVariant(saveLinkedID, "Нове", "", "", ""), saveBaseID)
	chained := linkTo(saveVariant(saveThirdID, "Нове", "", "", ""), saveLinkedID)

	_, err := s.uc.SaveDraft(context.Background(), saveExerciseID, exercise.SaveDraftInput{
		Variants: []exerciseModel.Variant{base, linked, chained},
	})

	require.ErrorIs(t, err, exerciseModel.ErrVariantLinkInvalid.Err())
	require.False(t, s.called, "nothing is written")
}

func TestSaveDraft_UnlinksDeletedBase(t *testing.T) {
	stored := []exerciseModel.Variant{
		saveVariant(saveBaseID, "Нове", "", "ICE{a}", ""),
		linkTo(saveVariant(saveLinkedID, "Нове", "", "ICE{b}", ""), saveBaseID),
	}
	s := newLinkedSave(t, stored)
	sent := linkTo(saveVariant(saveLinkedID, "Своє", `{"d":"своє"}`, "ICE{b}", ""), saveBaseID)

	written := s.save(t, exercise.SaveDraftInput{Variants: []exerciseModel.Variant{sent}})

	require.Len(t, written, 1)
	require.False(t, written[0].IsLinked())
	require.Equal(t, "Своє", written[0].Tasks[0].Name)
	require.JSONEq(t, `{"d":"своє"}`, string(written[0].Tasks[0].Description))
	require.Equal(t, []string{"ICE{b}"}, written[0].Tasks[0].Flag)
}

func TestSaveDraft_LinkedVariantGetsOwnSecretCiphertext(t *testing.T) {
	baseSealed := seal(t, saveBaseID, "base-pass")
	s := newLinkedSave(t, []exerciseModel.Variant{saveVariant(saveBaseID, "Нове", "", "", baseSealed)})
	base := saveVariant(saveBaseID, "Нове", "", "", "")
	linked := linkTo(exerciseModel.Variant{}, saveBaseID)

	written := s.save(t, exercise.SaveDraftInput{Variants: []exerciseModel.Variant{base, linked}})

	require.Equal(t, baseSealed, secretOf(written[0]))
	got := secretOf(written[1])
	require.NotEmpty(t, got)
	require.NotEqual(t, baseSealed, got)
	require.Equal(t, "base-pass", s.open(t, written[1].ID, got))
}

func TestSaveDraft_BaseSecretChangeReachesLinked(t *testing.T) {
	stored := []exerciseModel.Variant{
		saveVariant(saveBaseID, "Нове", "", "", seal(t, saveBaseID, "old")),
		linkTo(saveVariant(saveLinkedID, "Нове", "", "", seal(t, saveLinkedID, "old")), saveBaseID),
	}
	s := newLinkedSave(t, stored)
	base := saveVariant(saveBaseID, "Нове", "", "", "new")
	linked := linkTo(saveVariant(saveLinkedID, "Нове", "", "", ""), saveBaseID)

	written := s.save(t, exercise.SaveDraftInput{Variants: []exerciseModel.Variant{base, linked}})

	require.Equal(t, "new", s.open(t, saveBaseID, secretOf(written[0])))
	require.Equal(t, "new", s.open(t, saveLinkedID, secretOf(written[1])))
}

func TestSaveDraft_LinkedSecretStableAcrossSaves(t *testing.T) {
	baseSealed, linkedSealed := seal(t, saveBaseID, "pass"), seal(t, saveLinkedID, "pass")
	s := newLinkedSave(t, []exerciseModel.Variant{
		saveVariant(saveBaseID, "Нове", "", "", baseSealed),
		linkTo(saveVariant(saveLinkedID, "Нове", "", "", linkedSealed), saveBaseID),
	})
	base := saveVariant(saveBaseID, "Нове", "", "", "")
	linked := linkTo(saveVariant(saveLinkedID, "Нове", "", "", ""), saveBaseID)

	written := s.save(t, exercise.SaveDraftInput{Variants: []exerciseModel.Variant{base, linked}})

	require.Equal(t, baseSealed, secretOf(written[0]))
	require.Equal(t, linkedSealed, secretOf(written[1]))
}

// A linked variant's topology is the base's: a stored secret of its own that differs (it was linked after
// the fact, or the data predates the link) is replaced by the base's value under its own id.
func TestSaveDraft_LinkedSecretFollowsKeptBase(t *testing.T) {
	baseSealed := seal(t, saveBaseID, "base-pass")
	s := newLinkedSave(t, []exerciseModel.Variant{
		saveVariant(saveBaseID, "Нове", "", "", baseSealed),
		saveVariant(saveLinkedID, "Нове", "", "", seal(t, saveLinkedID, "own-pass")),
	})
	base := saveVariant(saveBaseID, "Нове", "", "", "")
	linked := linkTo(saveVariant(saveLinkedID, "Нове", "", "", ""), saveBaseID)

	written := s.save(t, exercise.SaveDraftInput{Variants: []exerciseModel.Variant{base, linked}})

	require.Equal(t, baseSealed, secretOf(written[0]))
	require.Equal(t, "base-pass", s.open(t, saveLinkedID, secretOf(written[1])))
}

func TestSaveDraft_CopyOfKeepsSecret(t *testing.T) {
	baseSealed := seal(t, saveBaseID, "base-pass")
	s := newLinkedSave(t, []exerciseModel.Variant{saveVariant(saveBaseID, "Нове", "", "", baseSealed)})
	base := saveVariant(saveBaseID, "Нове", "", "", "")
	copied := saveVariant(uuid.Nil, "Нове", "", "", "")

	written := s.save(t, exercise.SaveDraftInput{
		Variants: []exerciseModel.Variant{base, copied},
		CopyOf:   []uuid.NullUUID{{}, {UUID: saveBaseID, Valid: true}},
	})

	require.Equal(t, baseSealed, secretOf(written[0]))
	got := secretOf(written[1])
	require.NotEqual(t, baseSealed, got)
	require.Equal(t, "base-pass", s.open(t, written[1].ID, got))
	require.False(t, written[1].IsLinked())
}

// The import seals secrets after the linked variants are synced, so an imported linked variant of a base with
// a secret gets its own ciphertext (no extra inheritance needed there).
func TestImportExerciseArchive_LinkedVariantGetsOwnSecretCiphertext(t *testing.T) {
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	cipher, err := secret.New(testKey)
	require.NoError(t, err)
	doc := `{"schema":"cib-exercise/1","exercise":{"name":"Пов'язані"},"versions":[{"status":"draft","variants":[
		{"tasks":[{"name":"Тест","difficulty":"easy"}],
		 "topology":{"devices":[{"name":"web","type":"container","image":"img",
		   "env_vars":[{"name":"PASS","value":"base-pass","secret":true}]}]}},
		{"linked_to":1,"tasks":[{}]}]}]}`
	archive, err := exercise.BuildProtectedArchiveV1(map[string][]byte{"exercise.json": []byte(doc)}, "pw")
	require.NoError(t, err)
	var written []exerciseModel.Variant
	expectImport(q, func(variants []exerciseModel.Variant) { written = variants })
	uc := newTestExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: newFakeMedia()})

	_, err = uc.ImportExerciseArchive(context.Background(), exercise.ImportExerciseInput{Archive: archive, Password: "pw"})
	require.NoError(t, err)

	require.Len(t, written, 2)
	base, linked := written[0], written[1]
	require.True(t, linked.IsLinked())
	require.NotEqual(t, secretOf(base), secretOf(linked))
	for _, v := range written {
		plain, openErr := cipher.DecryptWithContext(secretOf(v), exercise.EnvSecretContext(v.ID, saveDevice, saveEnv))
		require.NoError(t, openErr)
		require.Equal(t, "base-pass", string(plain))
	}
}

// Sources resolve before they are used: C links to B, a new copy of A whose secret is blank.
func TestSaveDraft_LinkedToNewCopyGetsSecret(t *testing.T) {
	baseSealed := seal(t, saveBaseID, "base-pass")
	s := newLinkedSave(t, []exerciseModel.Variant{saveVariant(saveBaseID, "Нове", "", "", baseSealed)})
	a := saveVariant(saveBaseID, "Нове", "", "", "")
	b := saveVariant(saveLinkedID, "Нове", "", "", "")
	c := linkTo(exerciseModel.Variant{ID: saveThirdID}, saveLinkedID)

	written := s.save(t, exercise.SaveDraftInput{
		Variants: []exerciseModel.Variant{a, b, c},
		CopyOf:   []uuid.NullUUID{{}, {UUID: saveBaseID, Valid: true}, {}},
	})

	require.Equal(t, "base-pass", s.open(t, saveLinkedID, secretOf(written[1])))
	require.Equal(t, "base-pass", s.open(t, saveThirdID, secretOf(written[2])))
}

// A copy of a copy, listed before its source, still gets the secret.
func TestSaveDraft_CopyOfCopyInReverseOrder(t *testing.T) {
	baseSealed := seal(t, saveBaseID, "base-pass")
	s := newLinkedSave(t, []exerciseModel.Variant{saveVariant(saveBaseID, "Нове", "", "", baseSealed)})
	second := saveVariant(saveThirdID, "Нове", "", "", "")
	first := saveVariant(saveLinkedID, "Нове", "", "", "")
	base := saveVariant(saveBaseID, "Нове", "", "", "")

	written := s.save(t, exercise.SaveDraftInput{
		Variants: []exerciseModel.Variant{second, first, base},
		CopyOf:   []uuid.NullUUID{{UUID: saveLinkedID, Valid: true}, {UUID: saveBaseID, Valid: true}, {}},
	})

	require.Equal(t, "base-pass", s.open(t, saveThirdID, secretOf(written[0])))
	require.Equal(t, "base-pass", s.open(t, saveLinkedID, secretOf(written[1])))
}

// CopyOf only fills new copies: a stored variant whose secret was left empty on purpose stays empty.
func TestSaveDraft_CopyOfIgnoredForStoredVariant(t *testing.T) {
	baseSealed := seal(t, saveBaseID, "base-pass")
	s := newLinkedSave(t, []exerciseModel.Variant{
		saveVariant(saveBaseID, "Нове", "", "", baseSealed),
		saveVariant(saveLinkedID, "Нове", "", "", ""),
	})
	base := saveVariant(saveBaseID, "Нове", "", "", "")
	existing := saveVariant(saveLinkedID, "Нове", "", "", "")

	written := s.save(t, exercise.SaveDraftInput{
		Variants: []exerciseModel.Variant{base, existing},
		CopyOf:   []uuid.NullUUID{{}, {UUID: saveBaseID, Valid: true}},
	})

	require.Empty(t, secretOf(written[1]))
}
