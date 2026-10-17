package exerciseModel

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/model/flagpattern"
)

func lid(s string) uuid.UUID { return uuid.FromStringOrNil(s) }

var (
	linkBaseID   = lid("00000000-0000-0000-0000-0000000000a1")
	linkLinkedID = lid("00000000-0000-0000-0000-0000000000a2")
	linkThirdID  = lid("00000000-0000-0000-0000-0000000000a3")
	linkDevID    = lid("00000000-0000-0000-0000-0000000000d1")
	linkHintID   = lid("00000000-0000-0000-0000-0000000000b1")
	linkFileID   = lid("00000000-0000-0000-0000-0000000000f2")
	linkTaskA    = lid("00000000-0000-0000-0000-00000000aa01")
	linkTaskB    = lid("00000000-0000-0000-0000-00000000aa02")
	linkTaskC    = lid("00000000-0000-0000-0000-00000000aa03")
)

func linkedTask(id uuid.UUID, name string, flag ...string) Task {
	return Task{
		ID: id, Name: name, Difficulty: DifficultyEasy, Flag: flag,
		Description: json.RawMessage(`{"root":{"children":[{"type":"text","text":"x"}]}}`),
	}
}

func TestSyncLinkedVariants_CopiesInheritedParts(t *testing.T) {
	baseTask := Task{
		Name: "Пошук", Description: json.RawMessage(`{"root":{}}`), Difficulty: DifficultyHard,
		Placeholders:   []Placeholder{{Key: "srv", Kind: PlaceholderIP}},
		Hints:          []Hint{{ID: linkHintID, Text: "a", Level: HintLevelSteps}},
		LinkedDeviceID: uuid.NullUUID{UUID: linkDevID, Valid: true}, DeviceFlagVar: "FLAG",
	}
	version := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID, Index: 1, Tasks: []Task{baseTask}, Topology: Topology{Devices: []Device{{ID: linkDevID, Name: "srv"}}}},
		{
			ID: linkLinkedID, Index: 2, Note: "seed 2", LinkedTo: uuid.NullUUID{UUID: linkBaseID, Valid: true},
			Tasks: []Task{{Name: "", Flag: []string{"ICE{b}"}, Attachments: []AttachmentRef{{FileID: linkFileID, Name: "b.txt"}}}},
		},
	}}
	require.NoError(t, version.SyncLinkedVariants())

	linked := version.Variants[1]
	want := baseTask
	want.Flag = []string{"ICE{b}"}
	want.Attachments = []AttachmentRef{{FileID: linkFileID, Name: "b.txt"}}
	assert.Equal(t, want, linked.Tasks[0])
	baseJSON, _ := json.Marshal(version.Variants[0].Topology)
	linkedJSON, _ := json.Marshal(linked.Topology)
	require.JSONEq(t, string(baseJSON), string(linkedJSON))
	assert.Equal(t, "seed 2", linked.Note)
	assert.Equal(t, linkLinkedID, linked.ID)
	assert.Equal(t, int32(2), linked.Index)
	assert.Equal(t, uuid.NullUUID{UUID: linkBaseID, Valid: true}, linked.LinkedTo)
	assert.True(t, linked.IsLinked())
	assert.False(t, version.Variants[0].IsLinked())
}

func TestSyncLinkedVariants_FollowsBaseTaskChanges(t *testing.T) {
	a, b := linkedTask(linkTaskA, "A"), linkedTask(linkTaskB, "B")
	version := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID, Tasks: []Task{a, b}},
		{
			ID: linkLinkedID, LinkedTo: uuid.NullUUID{UUID: linkBaseID, Valid: true},
			Tasks: []Task{{ID: linkTaskA, Flag: []string{"ICE{a2}"}}, {ID: linkTaskB, Flag: []string{"ICE{b2}"}}},
		},
	}}
	version.Variants[0].Tasks = []Task{b, a, linkedTask(linkTaskC, "C")}
	require.NoError(t, version.SyncLinkedVariants())
	got := version.Variants[1].Tasks
	require.Len(t, got, 3)
	assert.Equal(t, []string{"ICE{b2}"}, got[0].Flag)
	assert.Equal(t, []string{"ICE{a2}"}, got[1].Flag)
	assert.Nil(t, got[2].Flag)
	assert.Equal(t, linkTaskC, got[2].ID)

	version.Variants[0].Tasks = []Task{a}
	require.NoError(t, version.SyncLinkedVariants())
	got = version.Variants[1].Tasks
	require.Len(t, got, 1)
	assert.Equal(t, []string{"ICE{a2}"}, got[0].Flag)
}

func TestSyncLinkedVariants_MatchesByPositionWithoutIDs(t *testing.T) {
	version := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID, Tasks: []Task{linkedTask(uuid.Nil, "A"), linkedTask(uuid.Nil, "B")}},
		{
			ID: linkLinkedID, LinkedTo: uuid.NullUUID{UUID: linkBaseID, Valid: true},
			Tasks: []Task{{Flag: []string{"ICE{1}"}}, {Flag: []string{"ICE{2}"}}},
		},
	}}
	require.NoError(t, version.SyncLinkedVariants())
	assert.Equal(t, []string{"ICE{1}"}, version.Variants[1].Tasks[0].Flag)
	assert.Equal(t, []string{"ICE{2}"}, version.Variants[1].Tasks[1].Flag)
}

func TestSyncLinkedVariants_NilLinkedTaskIDMatchesByPosition(t *testing.T) {
	version := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID, Tasks: []Task{linkedTask(linkTaskA, "A"), linkedTask(linkTaskB, "B")}},
		{
			ID: linkLinkedID, LinkedTo: uuid.NullUUID{UUID: linkBaseID, Valid: true},
			Tasks: []Task{
				{Flag: []string{"ICE{1}"}, Attachments: []AttachmentRef{{FileID: linkFileID, Name: "1.txt"}}},
				{Flag: []string{"ICE{2}"}},
			},
		},
	}}
	require.NoError(t, version.SyncLinkedVariants())
	got := version.Variants[1].Tasks
	assert.Equal(t, []string{"ICE{1}"}, got[0].Flag)
	assert.Equal(t, []AttachmentRef{{FileID: linkFileID, Name: "1.txt"}}, got[0].Attachments)
	assert.Equal(t, []string{"ICE{2}"}, got[1].Flag)
	assert.Equal(t, linkTaskB, got[1].ID)
}

func TestSyncLinkedVariants_UnlinksMissingBase(t *testing.T) {
	version := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID, Tasks: []Task{linkedTask(linkTaskA, "A")}},
		{
			ID: linkLinkedID, Note: "n", LinkedTo: uuid.NullUUID{UUID: linkThirdID, Valid: true},
			Tasks: []Task{linkedTask(linkTaskB, "Own", "ICE{o}")},
		},
	}}
	require.NoError(t, version.SyncLinkedVariants())
	v := version.Variants[1]
	assert.False(t, v.LinkedTo.Valid)
	assert.Equal(t, "Own", v.Tasks[0].Name)
	assert.Equal(t, "n", v.Note)
}

func TestSyncLinkedVariants_RejectsSelfAndChain(t *testing.T) {
	self := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID, LinkedTo: uuid.NullUUID{UUID: linkBaseID, Valid: true}},
	}}
	err := self.SyncLinkedVariants()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrVariantLinkInvalid.Err()))

	chain := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID},
		{ID: linkLinkedID, LinkedTo: uuid.NullUUID{UUID: linkBaseID, Valid: true}},
		{ID: linkThirdID, LinkedTo: uuid.NullUUID{UUID: linkLinkedID, Valid: true}},
	}}
	err = chain.SyncLinkedVariants()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrVariantLinkInvalid.Err()))
	assert.Contains(t, err.Error(), "VARIANT:=3")
}

func TestSyncLinkedVariants_DeepCopy(t *testing.T) {
	task := linkedTask(linkTaskA, "A")
	task.Hints = []Hint{{ID: linkHintID, Text: "h", Level: HintLevelNudge}}
	version := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID, Tasks: []Task{task}, Topology: Topology{Devices: []Device{{ID: linkDevID, Name: "d"}}}},
		{ID: linkLinkedID, LinkedTo: uuid.NullUUID{UUID: linkBaseID, Valid: true}},
	}}
	originalDescription := string(task.Description)
	require.NoError(t, version.SyncLinkedVariants())
	base := &version.Variants[0]
	base.Topology.Devices[0].Name = "x"
	base.Tasks[0].Hints[0].Text = "x"
	base.Tasks[0].Description[2] = 'X'

	linked := version.Variants[1]
	assert.Equal(t, "d", linked.Topology.Devices[0].Name)
	assert.Equal(t, "h", linked.Tasks[0].Hints[0].Text)
	assert.JSONEq(t, originalDescription, string(linked.Tasks[0].Description))
}

func TestSyncLinkedVariants_Idempotent(t *testing.T) {
	version := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID, Tasks: []Task{linkedTask(linkTaskA, "A", "ICE{a}")}},
		{ID: linkLinkedID, LinkedTo: uuid.NullUUID{UUID: linkBaseID, Valid: true}, Tasks: []Task{{ID: linkTaskA, Flag: []string{"ICE{b}"}}}},
	}}
	require.NoError(t, version.SyncLinkedVariants())
	first, err := json.Marshal(version)
	require.NoError(t, err)
	require.NoError(t, version.SyncLinkedVariants())
	second, err := json.Marshal(version)
	require.NoError(t, err)
	assert.JSONEq(t, string(first), string(second))
}

func TestVariantJSON_OmitsUnsetLink(t *testing.T) {
	plain, err := json.Marshal(Variant{})
	require.NoError(t, err)
	assert.NotContains(t, string(plain), "linked_to")

	linked := Variant{ID: linkLinkedID, LinkedTo: uuid.NullUUID{UUID: linkBaseID, Valid: true}}
	data, err := json.Marshal(linked)
	require.NoError(t, err)
	assert.Contains(t, string(data), `"linked_to":"`+linkBaseID.String()+`"`)
	var back Variant
	require.NoError(t, json.Unmarshal(data, &back))
	assert.Equal(t, linked.LinkedTo, back.LinkedTo)
}

func TestValidateStructure_LinkedFlagsDistinct(t *testing.T) {
	link := uuid.NullUUID{UUID: linkBaseID, Valid: true}
	tpl := flagpattern.TemplatePrefix + "x"
	mk := func(id uuid.UUID, linkedTo uuid.NullUUID, flags ...string) Variant {
		tasks := make([]Task, len(flags))
		for i := range flags {
			tasks[i] = linkedTask(linkTaskA, "Task", flags[i])
			if i == 1 {
				tasks[i].ID = linkTaskB
			}
			if flags[i] == "" {
				tasks[i].Flag = nil
			}
		}
		return Variant{ID: id, LinkedTo: linkedTo, Tasks: tasks}
	}
	tests := []struct {
		name     string
		variants []Variant
		wantErr  bool
	}{
		{"linked equals base", []Variant{mk(linkBaseID, uuid.NullUUID{}, "ICE{a}"), mk(linkLinkedID, link, "ICE{a}")}, true},
		{"two linked equal", []Variant{mk(linkBaseID, uuid.NullUUID{}, "ICE{a}"), mk(linkLinkedID, link, "ICE{c}"), mk(linkThirdID, link, "ICE{c}")}, true},
		{"distinct", []Variant{mk(linkBaseID, uuid.NullUUID{}, "ICE{a}"), mk(linkLinkedID, link, "ICE{b}")}, false},
		{"independent same", []Variant{mk(linkBaseID, uuid.NullUUID{}, "ICE{a}"), mk(linkLinkedID, uuid.NullUUID{}, "ICE{a}")}, false},
		{"other task of base", []Variant{mk(linkBaseID, uuid.NullUUID{}, "ICE{a}", "ICE{z}"), mk(linkLinkedID, link, "ICE{z}", "ICE{y}")}, false},
		{"template repeated", []Variant{mk(linkBaseID, uuid.NullUUID{}, tpl), mk(linkLinkedID, link, tpl)}, false},
		{"empty flags", []Variant{mk(linkBaseID, uuid.NullUUID{}, ""), mk(linkLinkedID, link, "")}, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateLinkedFlags(tc.variants)
			if !tc.wantErr {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.True(t, errors.Is(err, ErrLinkedFlagsNotDistinct.Err()))
			assert.Contains(t, err.Error(), "TASK:=Task")
		})
	}
}

func TestValidateStructure_RunsLinkedFlagRule(t *testing.T) {
	link := uuid.NullUUID{UUID: linkBaseID, Valid: true}
	version := &ExerciseVersion{Variants: []Variant{
		{ID: linkBaseID, Tasks: []Task{linkedTask(linkTaskA, "Task", "ICE{a}")}},
		{ID: linkLinkedID, LinkedTo: link, Tasks: []Task{linkedTask(linkTaskA, "Task", "ICE{a}")}},
	}}
	err := version.ValidateStructure()
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrLinkedFlagsNotDistinct.Err()))
}
