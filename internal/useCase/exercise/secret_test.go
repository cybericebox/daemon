package exercise_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/pkg/secret"
)

const testKey = "6368616e676520746869732070617373776f726420746f206120736563726574"

func errNoRows() error { return pgx.ErrNoRows }

func containsStr(haystack, needle string) bool { return strings.Contains(haystack, needle) }

func mustMarshal(t *testing.T, vs []exerciseModel.Variant) []byte {
	t.Helper()
	b, err := json.Marshal(vs)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// defaultVariantID keeps single-variant fixtures matched by id across
// separate draftVariants() calls (e.g. a "prior" build and an "incoming"
// build) without every call site having to plumb an explicit id — secret
// merge/encrypt keys by variant ID (Change B), so a "prior" and "incoming"
// fixture representing the SAME logical variant must share one.
var defaultVariantID = uuid.Must(uuid.NewV7())

// draftVariants builds a one-variant, one-task, one-device fixture with a
// single env var, using defaultVariantID. Flag is left nil (empty slice →
// random flag at deploy) per the approved design: exerciseModel.Task.Flag is
// []string, FlagSource/FlagMode no longer exist.
func draftVariants(envValue string, secretFlag bool) []exerciseModel.Variant {
	return draftVariantsWithID(defaultVariantID, envValue, secretFlag)
}

// draftVariantsWithID is draftVariants with an explicit variant id, for tests
// that need multiple, distinctly-identified variants (cross-contamination and
// reorder regression tests).
func draftVariantsWithID(id uuid.UUID, envValue string, secretFlag bool) []exerciseModel.Variant {
	return []exerciseModel.Variant{{
		ID: id,
		Tasks: []exerciseModel.Task{{
			Name:       "Find the flag",
			Difficulty: exerciseModel.DifficultyEasy,
		}},
		Topology: exerciseModel.Topology{Devices: []exerciseModel.Device{{
			ID: uuid.Must(uuid.NewV7()), Name: "web", Type: exerciseModel.DeviceTypeContainer,
			Image:   "img",
			EnvVars: []exerciseModel.EnvVar{{Name: "DB_PASS", Value: envValue, Secret: secretFlag}},
		}}},
	}}
}

func expectUpsertCapture(t *testing.T, q *postgresMocks.MockQuerier, captured *postgres.UpsertExerciseDraftParams) {
	t.Helper()
	q.EXPECT().UpsertExerciseDraft(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpsertExerciseDraftParams) (postgres.UpsertExerciseDraftRow, error) {
			*captured = arg
			return postgres.UpsertExerciseDraftRow{ID: arg.NewID, ExerciseID: arg.ExerciseID,
				Status: "draft", AdminNote: arg.AdminNote,
				Variants: arg.Variants, CreatedAt: arg.CreatedAt, CreatedBy: arg.CreatedBy}, nil
		})
}

// SaveDraft must write ciphertext, never the plaintext, into the stored blob.
func TestSaveDraft_EncryptsSecrets(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, _ := secret.New(testKey)
	m := newFakeMedia()
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: m})
	exID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows())
	// No draft: secretMergeSource falls back to checking for a published
	// version — this exercise has none, so nothing to merge from.
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil)
	var captured postgres.UpsertExerciseDraftParams
	expectUpsertCapture(t, q, &captured)

	_, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{
		Variants: draftVariants("plaintext-pass", true),
	})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	blob := string(captured.Variants)
	if len(blob) == 0 || containsStr(blob, "plaintext-pass") {
		t.Fatalf("stored blob must not contain the plaintext secret: %s", blob)
	}
}

func TestSaveDraft_SecretWithoutCipher(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: nil, Media: newFakeMedia()})
	exID := uuid.Must(uuid.NewV7())

	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows()).AnyTimes()
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{ID: exID}, nil).AnyTimes()

	_, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{
		Variants: draftVariants("v", true),
	})
	if !errors.Is(err, exerciseModel.ErrSecretsNotConfigured.Err()) {
		t.Fatalf("want ErrSecretsNotConfigured, got %v", err)
	}
}

// Empty secret value on save keeps the previously stored ciphertext.
func TestSaveDraft_KeepsPriorSecret(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, _ := secret.New(testKey)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: newFakeMedia()})
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)

	priorCipher, _ := cipher.Encrypt("old-secret")
	prior := draftVariants(priorCipher, true)
	priorBlob := mustMarshal(t, prior)
	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "draft", Variants: priorBlob,
	}, nil)
	var captured postgres.UpsertExerciseDraftParams
	expectUpsertCapture(t, q, &captured)

	incoming := draftVariants("", true) // same device/var name, empty value = keep
	incoming[0].Topology.Devices[0].Name = "web"
	_, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{Variants: incoming})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if !containsStr(string(captured.Variants), priorCipher) {
		t.Fatal("prior ciphertext must be preserved for empty incoming secret value")
	}
}

// TestSaveDraft_FallsBackToPublishedWhenNoDraft is the regression test for
// silent secret loss after publish: PublishDraft clears the draft slot, so an
// admin who opens the (secret-masked) published version and PUTs it back
// unchanged has no draft to merge kept secrets from. Before the fix, that
// meant every "blank" secret field was stored as literal empty string with no
// error. secretMergeSource must fall back to the published version.
func TestSaveDraft_FallsBackToPublishedWhenNoDraft(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, _ := secret.New(testKey)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: newFakeMedia()})
	exID := uuid.Must(uuid.NewV7())
	publishedID := uuid.Must(uuid.NewV7())

	priorCipher, _ := cipher.Encrypt("published-secret")
	published := draftVariants(priorCipher, true)
	publishedBlob := mustMarshal(t, published)

	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{}, errNoRows())
	q.EXPECT().GetExerciseByID(gomock.Any(), exID).Return(postgres.Exercise{
		ID: exID, PublishedVersionID: uuid.NullUUID{UUID: publishedID, Valid: true},
	}, nil)
	q.EXPECT().GetExerciseVersionByID(gomock.Any(), publishedID).Return(postgres.ExerciseVersion{
		ID: publishedID, ExerciseID: exID, Status: "published", Variants: publishedBlob,
	}, nil)
	var captured postgres.UpsertExerciseDraftParams
	expectUpsertCapture(t, q, &captured)

	incoming := draftVariants("", true) // blank-keep, same device/var name
	_, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{Variants: incoming})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}
	if !containsStr(string(captured.Variants), priorCipher) {
		t.Fatal("published ciphertext must be carried over when there is no draft to merge from")
	}
}

// TestSaveDraft_TwoVariants_NoCrossContamination is the regression test for
// the cross-variant secret key collision: two variants sharing the same
// device/env names must NOT share a merge/encrypt key. Variant A submits
// fresh plaintext, variant B blank-keeps its own previously stored
// ciphertext. Before the fix, mergeKeptSecrets/submittedSecretKeys keyed only
// on (device, env) — B would be filled with A's OLD stored ciphertext (or
// vice versa, "last wins" in the flat map) and then encryptSecrets would
// re-encrypt it because the flat key was also marked "submitted" by A. Either
// way, B's stored value must be preserved byte-for-byte and A's must be a
// freshly, individually encrypted ciphertext of its own submitted plaintext.
func TestSaveDraft_TwoVariants_NoCrossContamination(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, _ := secret.New(testKey)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: newFakeMedia()})
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)
	idA, idB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	// Prior draft: two variants, both device "web" / env "DB_PASS", each with
	// its OWN stored ciphertext.
	priorCipherA, _ := cipher.Encrypt("old-secret-A")
	priorCipherB, _ := cipher.Encrypt("old-secret-B")
	prior := append(draftVariantsWithID(idA, priorCipherA, true), draftVariantsWithID(idB, priorCipherB, true)...)
	priorBlob := mustMarshal(t, prior)
	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "draft", Variants: priorBlob,
	}, nil)
	var captured postgres.UpsertExerciseDraftParams
	expectUpsertCapture(t, q, &captured)

	// Incoming: same variant ids, same order — variant A submits new
	// plaintext, variant B blank-keeps.
	incoming := append(draftVariantsWithID(idA, "new-plaintext-A", true), draftVariantsWithID(idB, "", true)...)
	_, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{Variants: incoming})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	var stored []exerciseModel.Variant
	if uErr := json.Unmarshal(captured.Variants, &stored); uErr != nil {
		t.Fatalf("unmarshal stored variants: %v", uErr)
	}
	if len(stored) != 2 {
		t.Fatalf("want 2 stored variants, got %d", len(stored))
	}

	gotA := stored[0].Topology.Devices[0].EnvVars[0].Value
	gotB := stored[1].Topology.Devices[0].EnvVars[0].Value

	// B must keep its OWN original ciphertext, byte-for-byte — no re-encryption.
	if gotB != priorCipherB {
		t.Fatalf("variant B must keep its original ciphertext unchanged: want %q, got %q", priorCipherB, gotB)
	}

	// A must be freshly encrypted plaintext (not the raw plaintext, not B's
	// stored ciphertext, and not A's own OLD ciphertext) and decryptable back
	// to what was submitted.
	if gotA == priorCipherA || gotA == priorCipherB || gotA == "new-plaintext-A" {
		t.Fatalf("variant A must be freshly encrypted, distinct from stale/foreign ciphertext: got %q", gotA)
	}
	plainBytes, dErr := cipher.DecryptWithContext(gotA, exercise.EnvSecretContext(idA, "web", "DB_PASS"))
	plain := string(plainBytes)
	if dErr != nil {
		t.Fatalf("variant A ciphertext must decrypt: %v", dErr)
	}
	if plain != "new-plaintext-A" {
		t.Fatalf("variant A must decrypt to the submitted plaintext, got %q", plain)
	}
}

// TestSaveDraft_VariantsReordered_SecretsFollowID is the regression test for
// Change B: secret merge keys by variant ID, not slice position. Two variants
// (A, B) are stored in order [A, B]; the incoming save submits them
// reordered, [B, A], both blank-keeping. Before Change B (positional keying),
// position 0 in the incoming save would have merged from position 0 in the
// stored draft — i.e. B would wrongly receive A's stored secret. Keyed by ID,
// each variant must recover its OWN stored ciphertext regardless of position.
func TestSaveDraft_VariantsReordered_SecretsFollowID(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, _ := secret.New(testKey)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: newFakeMedia()})
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)
	idA, idB := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	priorCipherA, _ := cipher.Encrypt("old-secret-A")
	priorCipherB, _ := cipher.Encrypt("old-secret-B")
	// Stored order: [A, B].
	prior := append(draftVariantsWithID(idA, priorCipherA, true), draftVariantsWithID(idB, priorCipherB, true)...)
	priorBlob := mustMarshal(t, prior)
	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "draft", Variants: priorBlob,
	}, nil)
	var captured postgres.UpsertExerciseDraftParams
	expectUpsertCapture(t, q, &captured)

	// Incoming order swapped: [B, A], both blank-keep.
	incoming := append(draftVariantsWithID(idB, "", true), draftVariantsWithID(idA, "", true)...)
	_, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{Variants: incoming})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	var stored []exerciseModel.Variant
	if uErr := json.Unmarshal(captured.Variants, &stored); uErr != nil {
		t.Fatalf("unmarshal stored variants: %v", uErr)
	}
	if len(stored) != 2 {
		t.Fatalf("want 2 stored variants, got %d", len(stored))
	}

	// stored[0] is B (per incoming order) and must keep B's OWN ciphertext,
	// even though B now occupies the position A held in the stored draft.
	if stored[0].ID != idB {
		t.Fatalf("stored[0] must be variant B, got id %v", stored[0].ID)
	}
	if got := stored[0].Topology.Devices[0].EnvVars[0].Value; got != priorCipherB {
		t.Fatalf("variant B must keep its own ciphertext despite the position swap: want %q, got %q", priorCipherB, got)
	}
	if stored[1].ID != idA {
		t.Fatalf("stored[1] must be variant A, got id %v", stored[1].ID)
	}
	if got := stored[1].Topology.Devices[0].EnvVars[0].Value; got != priorCipherA {
		t.Fatalf("variant A must keep its own ciphertext despite the position swap: want %q, got %q", priorCipherA, got)
	}
}

// TestSaveDraft_LegacySourceWithoutVariantIDs_KeepsSecrets is the transition
// regression for the variant-ID introduction: versions persisted BEFORE
// Variant.ID existed unmarshal with ID == uuid.Nil, while the incoming
// (ID-less) variants get FRESH UUIDv7s from normalizeContentIDs before the
// merge runs. Pure ID keying therefore never matches — every blank="keep"
// secret against a legacy version would be silently persisted as "". The
// merge must fall back to positional matching for nil-ID source variants so
// legacy blank-keeps still carry the stored ciphertext.
func TestSaveDraft_LegacySourceWithoutVariantIDs_KeepsSecrets(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, _ := secret.New(testKey)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: newFakeMedia()})
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)

	priorCipherA, _ := cipher.Encrypt("legacy-secret-A")
	priorCipherB, _ := cipher.Encrypt("legacy-secret-B")
	// Legacy stored draft: two variants, NO variant ids (pre-migration blob).
	prior := append(draftVariantsWithID(uuid.Nil, priorCipherA, true), draftVariantsWithID(uuid.Nil, priorCipherB, true)...)
	priorBlob := mustMarshal(t, prior)
	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "draft", Variants: priorBlob,
	}, nil)
	var captured postgres.UpsertExerciseDraftParams
	expectUpsertCapture(t, q, &captured)

	// Incoming: also ID-less (the editor round-trips what it loaded), same
	// order, both blank-keep.
	incoming := append(draftVariantsWithID(uuid.Nil, "", true), draftVariantsWithID(uuid.Nil, "", true)...)
	_, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{Variants: incoming})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	var stored []exerciseModel.Variant
	if uErr := json.Unmarshal(captured.Variants, &stored); uErr != nil {
		t.Fatalf("unmarshal stored variants: %v", uErr)
	}
	if len(stored) != 2 {
		t.Fatalf("want 2 stored variants, got %d", len(stored))
	}
	if got := stored[0].Topology.Devices[0].EnvVars[0].Value; got != priorCipherA {
		t.Fatalf("legacy variant at position 0 must carry its stored ciphertext: want %q, got %q", priorCipherA, got)
	}
	if got := stored[1].Topology.Devices[0].EnvVars[0].Value; got != priorCipherB {
		t.Fatalf("legacy variant at position 1 must carry its stored ciphertext: want %q, got %q", priorCipherB, got)
	}
	// The transition save must also mint ids so the NEXT save merges by ID.
	if stored[0].ID == uuid.Nil || stored[1].ID == uuid.Nil {
		t.Fatalf("stored variants must have minted ids after the transition save: %v, %v", stored[0].ID, stored[1].ID)
	}
}

// TestSaveDraft_LegacySource_AddedVariantStoresEmpty: against a legacy (nil-ID)
// merge source, positions that exist in the source carry their ciphertext,
// while a variant ADDED beyond the source's length has nothing to merge — its
// blank-keep stores empty, which is correct for a brand-new variant.
func TestSaveDraft_LegacySource_AddedVariantStoresEmpty(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	cipher, _ := secret.New(testKey)
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Cipher: cipher, Media: newFakeMedia()})
	exID := uuid.Must(uuid.NewV7())
	expectEditableExercise(q, exID)

	priorCipher, _ := cipher.Encrypt("legacy-secret")
	prior := draftVariantsWithID(uuid.Nil, priorCipher, true) // one legacy variant
	priorBlob := mustMarshal(t, prior)
	q.EXPECT().GetDraftVersion(gomock.Any(), exID).Return(postgres.ExerciseVersion{
		ID: uuid.Must(uuid.NewV7()), ExerciseID: exID, Status: "draft", Variants: priorBlob,
	}, nil)
	var captured postgres.UpsertExerciseDraftParams
	expectUpsertCapture(t, q, &captured)

	// Incoming: the original (position 0, blank-keep) plus a NEW second
	// variant (also blank-keep) with no counterpart in the source.
	incoming := append(draftVariantsWithID(uuid.Nil, "", true), draftVariantsWithID(uuid.Nil, "", true)...)
	_, err := uc.SaveDraft(context.Background(), exID, exercise.SaveDraftInput{Variants: incoming})
	if err != nil {
		t.Fatalf("SaveDraft: %v", err)
	}

	var stored []exerciseModel.Variant
	if uErr := json.Unmarshal(captured.Variants, &stored); uErr != nil {
		t.Fatalf("unmarshal stored variants: %v", uErr)
	}
	if len(stored) != 2 {
		t.Fatalf("want 2 stored variants, got %d", len(stored))
	}
	if got := stored[0].Topology.Devices[0].EnvVars[0].Value; got != priorCipher {
		t.Fatalf("existing position must carry the legacy ciphertext: want %q, got %q", priorCipher, got)
	}
	if got := stored[1].Topology.Devices[0].EnvVars[0].Value; got != "" {
		t.Fatalf("added variant has nothing to merge — blank-keep must store empty, got %q", got)
	}
}
