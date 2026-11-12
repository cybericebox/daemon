package exercise

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// Fixed ids, so a test names them: no random inputs.
func tid(n byte) uuid.UUID {
	return uuid.FromBytesOrNil([]byte{0, 0, 0, 0, 0, 0, 0x70, 0, 0x80, 0, 0, 0, 0, 0, 0, n})
}

func mtask(id uuid.UUID, key, name string, hints ...uuid.UUID) exerciseModel.Task {
	task := exerciseModel.Task{ID: id, Key: key, Name: name}
	for _, hint := range hints {
		task.Hints = append(task.Hints, exerciseModel.Hint{ID: hint, Text: "h"})
	}
	return task
}

func mdevice(id uuid.UUID, name string) exerciseModel.Device {
	return exerciseModel.Device{ID: id, Name: name, Type: exerciseModel.DeviceTypeNode}
}

func mvariant(
	id uuid.UUID,
	tasks []exerciseModel.Task,
	devices ...exerciseModel.Device,
) exerciseModel.Variant {
	return exerciseModel.Variant{
		ID:       id,
		Tasks:    tasks,
		Topology: exerciseModel.Topology{Devices: devices},
	}
}

func names(items []exerciseModel.ImportItem) []string {
	out := []string{}
	for _, item := range items {
		out = append(out, item.Name)
	}
	return out
}

func TestMatchTasksByIDKeepsStoredIDAndKey(t *testing.T) {
	stored := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), []exerciseModel.Task{mtask(tid(10), "web", "Old name")}),
		},
	}
	// The document carries the stored id, but another key and a new name: id wins, the stored key stays.
	in := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), []exerciseModel.Task{mtask(tid(10), "renamed_key", "New name")}),
		},
	}
	diff, warnings, err := matchStoredIdentity(&in, stored)
	require.NoError(t, err)
	require.Empty(t, warnings)
	task := in.Variants[0].Tasks[0]
	require.Equal(t, tid(10), task.ID)
	require.Equal(t, "web", task.Key)
	kept := diff.Variants[0].Tasks.Kept
	require.Len(t, kept, 1)
	require.Equal(t, exerciseModel.ImportMatchID, kept[0].MatchedBy)
	require.Equal(t, "Old name", kept[0].PreviousName)
	require.Equal(t, "New name", kept[0].Name)
	require.Empty(t, diff.Variants[0].Tasks.Added)
	require.Empty(t, diff.Variants[0].Tasks.Removed)
}

func TestMatchTasksByKeyWhenTheDocumentHasNoStoredID(t *testing.T) {
	stored := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(
				tid(1),
				[]exerciseModel.Task{mtask(tid(10), "web", "One"), mtask(tid(11), "db", "Two")},
			),
		},
	}
	// Fresh ids, as a parse of a document without ids gives; the keys tell who is who, in another order.
	in := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(
				tid(2),
				[]exerciseModel.Task{mtask(tid(90), "db", "Two"), mtask(tid(91), "web", "One")},
			),
		},
	}
	diff, _, err := matchStoredIdentity(&in, stored)
	require.NoError(t, err)
	require.Equal(t, tid(11), in.Variants[0].Tasks[0].ID)
	require.Equal(t, tid(10), in.Variants[0].Tasks[1].ID)
	for _, item := range diff.Variants[0].Tasks.Kept {
		require.Equal(t, exerciseModel.ImportMatchKey, item.MatchedBy)
	}
	require.Empty(t, diff.Variants[0].Tasks.Removed)
}

func TestMatchTasksRemovedAndAdded(t *testing.T) {
	stored := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(
				tid(1),
				[]exerciseModel.Task{mtask(tid(10), "a", "Stays"), mtask(tid(11), "b", "Leaves")},
			),
		},
	}
	in := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(
				tid(1),
				[]exerciseModel.Task{mtask(tid(10), "a", "Stays"), mtask(tid(92), "c", "Arrives")},
			),
		},
	}
	diff, _, err := matchStoredIdentity(&in, stored)
	require.NoError(t, err)
	changes := diff.Variants[0].Tasks
	require.Equal(t, []string{"Stays"}, names(changes.Kept))
	require.Equal(t, []string{"Arrives"}, names(changes.Added))
	require.Equal(t, []string{"Leaves"}, names(changes.Removed))
	require.Equal(t, tid(11), changes.Removed[0].ID)
	require.Equal(t, tid(92), in.Variants[0].Tasks[1].ID, "a new task keeps the id it came with")
}

func TestMatchTaskKeyRenameRewritesReferences(t *testing.T) {
	stored := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(
				tid(1),
				[]exerciseModel.Task{mtask(tid(10), "web", "One"), mtask(tid(11), "db", "Two")},
			),
		},
	}
	first, second := mtask(tid(10), "front", "One"), mtask(tid(11), "back", "Two")
	second.Requires = []string{"front"}
	device := mdevice(tid(50), "srv")
	device.Bootstrap.Env = []exerciseModel.EnvVar{
		{Name: "FLAG", Value: "{{task.front}}/{{task.back}}"},
	}
	variant := mvariant(tid(1), []exerciseModel.Task{first, second}, device)
	in := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{variant}}
	_, warnings, err := matchStoredIdentity(&in, stored)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, "web", in.Variants[0].Tasks[0].Key)
	require.Equal(t, "db", in.Variants[0].Tasks[1].Key)
	require.Equal(t, []string{"web"}, in.Variants[0].Tasks[1].Requires)
	require.Equal(
		t,
		"{{task.web}}/{{task.db}}",
		in.Variants[0].Topology.Devices[0].Bootstrap.Env[0].Value,
	)
}

func TestMatchTaskKeyConflictKeepsDocumentKeyWithWarning(t *testing.T) {
	stored := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), []exerciseModel.Task{mtask(tid(10), "web", "One")}),
		},
	}
	// The id points at the stored task whose key is "web", but the document already uses "web" for another task.
	in := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(
				tid(1),
				[]exerciseModel.Task{mtask(tid(10), "other", "One"), mtask(tid(93), "web", "Two")},
			),
		},
	}
	diff, warnings, err := matchStoredIdentity(&in, stored)
	require.NoError(t, err)
	require.Len(t, warnings, 1)
	require.Equal(t, exerciseModel.ImportWarnKeyConflict, warnings[0].Code)
	require.Equal(t, "other", in.Variants[0].Tasks[0].Key)
	require.True(t, diff.Variants[0].Tasks.Kept[0].KeyChanged)
}

func TestMatchDeviceRenameIsRemoveAndAdd(t *testing.T) {
	stored := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), nil, mdevice(tid(50), "web"), mdevice(tid(51), "db")),
		},
	}
	in := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), nil, mdevice(tid(80), "web"), mdevice(tid(81), "database")),
		},
	}
	in.Variants[0].Topology.Connections = []exerciseModel.Connection{
		{Endpoints: []exerciseModel.Endpoint{
			{
				Kind:     exerciseModel.EndpointDevice,
				DeviceID: tid(80),
			},
			{Kind: exerciseModel.EndpointDevice, DeviceID: tid(81)},
		}},
	}
	diff, _, err := matchStoredIdentity(&in, stored)
	require.NoError(t, err)
	devices := diff.Variants[0].Devices
	require.Equal(t, []string{"web"}, names(devices.Kept))
	require.Equal(t, exerciseModel.ImportMatchName, devices.Kept[0].MatchedBy)
	require.Equal(t, []string{"database"}, names(devices.Added))
	require.Equal(t, []string{"db"}, names(devices.Removed))
	require.Equal(
		t,
		tid(50),
		in.Variants[0].Topology.Devices[0].ID,
		"the kept device takes the stored id",
	)
	require.Equal(
		t,
		tid(81),
		in.Variants[0].Topology.Devices[1].ID,
		"the renamed device is new and keeps its own id",
	)
	endpoints := in.Variants[0].Topology.Connections[0].Endpoints
	require.Equal(t, tid(50), endpoints[0].DeviceID, "references follow the stored id")
	require.Equal(t, tid(81), endpoints[1].DeviceID)
}

func TestMatchDeviceIDsThatSwapDoNotMeetHalfway(t *testing.T) {
	stored := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), nil, mdevice(tid(50), "web"), mdevice(tid(51), "db")),
		},
	}
	// The document carries the two ids the other way round: web has db's id and db has web's. Names decide, so the
	// ids swap, and every reference follows its device (no id may end up on both).
	in := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), nil, mdevice(tid(51), "web"), mdevice(tid(50), "db")),
		},
	}
	in.Variants[0].Topology.Connections = []exerciseModel.Connection{
		{Endpoints: []exerciseModel.Endpoint{
			{
				Kind:     exerciseModel.EndpointDevice,
				DeviceID: tid(51),
			},
			{Kind: exerciseModel.EndpointDevice, DeviceID: tid(50)},
		}},
	}
	_, _, err := matchStoredIdentity(&in, stored)
	require.NoError(t, err)
	devices := in.Variants[0].Topology.Devices
	require.Equal(t, tid(50), devices[0].ID)
	require.Equal(t, tid(51), devices[1].ID)
	endpoints := in.Variants[0].Topology.Connections[0].Endpoints
	require.Equal(t, tid(50), endpoints[0].DeviceID)
	require.Equal(t, tid(51), endpoints[1].DeviceID)
}

func TestMatchVariantsByIDThenOrder(t *testing.T) {
	stored := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{
		mvariant(tid(1), []exerciseModel.Task{mtask(tid(10), "a", "A")}),
		mvariant(tid(2), []exerciseModel.Task{mtask(tid(10), "a", "A")}),
		mvariant(tid(3), []exerciseModel.Task{mtask(tid(10), "a", "A")}),
	}}
	in := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{
		mvariant(
			tid(70),
			[]exerciseModel.Task{mtask(tid(10), "a", "A")},
		), // no id match: first free stored one by order
		mvariant(tid(3), []exerciseModel.Task{mtask(tid(10), "a", "A")}), // matches by id
	}}
	diff, _, err := matchStoredIdentity(&in, stored)
	require.NoError(t, err)
	require.Len(t, diff.Variants, 3)
	require.Equal(t, exerciseModel.ImportVariantKept, diff.Variants[0].Status)
	require.Equal(t, 1, diff.Variants[0].StoredPosition)
	require.Equal(t, exerciseModel.ImportMatchOrder, diff.Variants[0].MatchedBy)
	require.Equal(t, 3, diff.Variants[1].StoredPosition)
	require.Equal(t, exerciseModel.ImportMatchID, diff.Variants[1].MatchedBy)
	require.Equal(t, exerciseModel.ImportVariantRemoved, diff.Variants[2].Status)
	require.Equal(t, 2, diff.Variants[2].StoredPosition)
	require.Equal(t, []string{"A"}, names(diff.Variants[2].Tasks.Removed))
	require.Equal(t, tid(1), in.Variants[0].ID)
}

func TestMatchHintsByIDThenPosition(t *testing.T) {
	stored := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), []exerciseModel.Task{mtask(tid(10), "a", "A", tid(20), tid(21))}),
		},
	}
	in := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), []exerciseModel.Task{mtask(tid(10), "a", "A", tid(95), tid(96))}),
		},
	}
	_, _, err := matchStoredIdentity(&in, stored)
	require.NoError(t, err)
	require.Equal(t, tid(20), in.Variants[0].Tasks[0].Hints[0].ID)
	require.Equal(t, tid(21), in.Variants[0].Tasks[0].Hints[1].ID)
}

func TestMatchWithNothingStoredAddsEverything(t *testing.T) {
	in := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(
				tid(1),
				[]exerciseModel.Task{mtask(tid(10), "a", "A")},
				mdevice(tid(50), "web"),
			),
		},
	}
	before, err := copyImportVersion(in)
	require.NoError(t, err)
	diff, _, err := matchStoredIdentity(&in, exerciseModel.ExerciseVersion{})
	require.NoError(t, err)
	require.Equal(t, exerciseModel.ImportVariantAdded, diff.Variants[0].Status)
	require.Equal(t, []string{"A"}, names(diff.Variants[0].Tasks.Added))
	require.Equal(t, []string{"web"}, names(diff.Variants[0].Devices.Added))
	require.Equal(t, before.Variants[0].ID, in.Variants[0].ID)
	require.Equal(t, before.Variants[0].Tasks[0].ID, in.Variants[0].Tasks[0].ID)
	require.Equal(
		t,
		before.Variants[0].Topology.Devices[0].ID,
		in.Variants[0].Topology.Devices[0].ID,
	)
}

func TestEventTaskChangeFollowsTheEventsRule(t *testing.T) {
	old := []exerciseModel.Task{
		mtask(tid(10), "a", "Same id"),
		mtask(tid(11), "b", "Renamed id"),
		mtask(tid(12), "c", "Gone"),
	}
	next := []exerciseModel.Task{
		mtask(tid(10), "a", "Same id"),
		mtask(tid(99), "b", " renamed ID "),
		mtask(tid(98), "d", "New"),
	}
	added, removed := eventTaskChange(old, next)
	require.Len(t, added, 1)
	require.Equal(t, "New", added[0].Name)
	require.Len(t, removed, 1)
	require.Equal(t, "Gone", removed[0].Name)
}

func TestUsageOfAttachmentBlocksOnRecordedAnswersAndMissingVariant(t *testing.T) {
	attachment := exerciseRepo.ActiveAttachment{
		ID:          tid(200),
		EventID:     tid(201),
		EventName:   "Cup",
		RunningLabs: 3,
	}
	pinned := exerciseModel.ExerciseVersion{Variants: []exerciseModel.Variant{
		mvariant(
			tid(1),
			[]exerciseModel.Task{
				mtask(tid(10), "a", "Solved task"),
				mtask(tid(11), "b", "Untouched task"),
			},
		),
		mvariant(tid(2), []exerciseModel.Task{mtask(tid(10), "a", "Solved task")}),
	}}
	result := exerciseModel.ExerciseVersion{
		Variants: []exerciseModel.Variant{
			mvariant(tid(1), []exerciseModel.Task{mtask(tid(12), "c", "Other")}),
		},
	}
	counts := map[[2]uuid.UUID]exerciseRepo.AnswerCount{
		{tid(200), tid(10)}: {Attempts: 5, Solves: 2},
	}

	event := usageOfAttachment(
		attachment,
		pinned,
		uuid.NullUUID{UUID: tid(9), Valid: true},
		result,
		counts,
	)
	require.Equal(t, 3, event.RunningLabs)
	require.False(t, event.VersionPublished)
	require.True(
		t,
		event.Blocked,
		"a task with recorded answers leaves: the event could not move to the result",
	)
	require.Equal(
		t,
		[]exerciseModel.ImportUsageTask{
			{Name: "Solved task", Attempts: 5, Solves: 2},
			{Name: "Untouched task"},
		},
		event.Removed,
	)
	require.Equal(t, []exerciseModel.ImportUsageTask{{Name: "Other"}}, event.Added)

	untouched := map[[2]uuid.UUID]exerciseRepo.AnswerCount{}
	require.False(
		t,
		usageOfAttachment(attachment, pinned, uuid.NullUUID{}, result, untouched).Blocked,
	)

	fixed := int32(1)
	attachment.FixedVariantIndex = &fixed
	missing := usageOfAttachment(attachment, pinned, uuid.NullUUID{}, result, untouched)
	require.True(t, missing.VariantMissing, "the event's fixed variant would not exist")
	require.True(t, missing.Blocked)
}
