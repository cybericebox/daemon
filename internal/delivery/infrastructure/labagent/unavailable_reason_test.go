package labagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
)

// featureWith builds a message that has the field unavailable_reason as the agent API 1.4 does (the pinned SDK has
// not), so the reader is checked without the new SDK.
func featureWith(t *testing.T, reason string) proto.Message {
	t.Helper()
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name: proto.String("feature_test.proto"), Package: proto.String("featuretest"), Syntax: proto.String("proto3"),
		MessageType: []*descriptorpb.DescriptorProto{{
			Name: proto.String("Feature"),
			Field: []*descriptorpb.FieldDescriptorProto{{
				Name: proto.String("unavailable_reason"), Number: proto.Int32(8),
				Type: descriptorpb.FieldDescriptorProto_TYPE_STRING.Enum(), Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
			}},
		}},
	}, nil)
	require.NoError(t, err)
	message := dynamicpb.NewMessage(file.Messages().ByName("Feature"))
	message.Set(file.Messages().ByName("Feature").Fields().ByName("unavailable_reason"), protoreflect.ValueOfString(reason))
	return message
}

func TestUnavailableReasonOf(t *testing.T) {
	assert.Equal(t, &infraModel.UnavailableReason{Code: "CGROUP_V2_MISSING", NodesFailed: 1, NodesAsked: 4}, unavailableReasonOf(featureWith(t, "CGROUP_V2_MISSING:1/4"), 8))
	assert.Nil(t, unavailableReasonOf(featureWith(t, ""), 8), "empty: nothing is held back")
	assert.Nil(t, unavailableReasonOf(featureWith(t, "node kind-worker: no cgroup tree"), 8), "free text is dropped")
	assert.Nil(t, unavailableReasonOf(nil, 8))
}

// The pinned SDK does not know the field: the reason arrives as an unknown field of the real message and is still
// read, in the persistence and the lifecycle features (the stale-report bug: the reason was always empty).
func TestUnavailableReasonOfReadsUnknownFields(t *testing.T) {
	withReason := func(message proto.Message, number protowire.Number, reason string) proto.Message {
		raw := protowire.AppendTag(nil, 99, protowire.VarintType) // another unknown field in front
		raw = protowire.AppendVarint(raw, 7)
		raw = protowire.AppendTag(raw, number, protowire.BytesType)
		raw = protowire.AppendString(raw, reason)
		message.ProtoReflect().SetUnknown(raw)
		return message
	}
	persistence := withReason(&labpb.StatePersistenceFeature{}, 8, "NODE_UNCONFIRMED:2/3")
	assert.Equal(t, &infraModel.UnavailableReason{Code: "NODE_UNCONFIRMED", NodesFailed: 2, NodesAsked: 3}, unavailableReasonOf(persistence, persistenceUnavailableReasonField))
	lifecycle := withReason(&labpb.LifecycleFeature{}, 6, "CGROUP_V2_MISSING:1/4")
	assert.Equal(t, &infraModel.UnavailableReason{Code: "CGROUP_V2_MISSING", NodesFailed: 1, NodesAsked: 4}, unavailableReasonOf(lifecycle, lifecycleUnavailableReasonField))
	assert.Nil(t, unavailableReasonOf(withReason(&labpb.StatePersistenceFeature{}, 8, "free text"), persistenceUnavailableReasonField))
	assert.Nil(t, unavailableReasonOf(withReason(&labpb.StatePersistenceFeature{}, 9, "NODE_UNCONFIRMED:2/3"), persistenceUnavailableReasonField), "another field number")
	assert.Nil(t, unavailableReasonOf(&labpb.StatePersistenceFeature{}, persistenceUnavailableReasonField))

	// whole report: FeaturesOf carries both reasons
	report := &labpb.FeaturesResponse{StatePersistence: persistence.(*labpb.StatePersistenceFeature), Lifecycle: lifecycle.(*labpb.LifecycleFeature)}
	features := FeaturesOf(report)
	require.NotNil(t, features.PersistenceUnavailable)
	require.NotNil(t, features.LifecycleUnavailable)
	assert.Equal(t, "NODE_UNCONFIRMED", features.PersistenceUnavailable.Code)
}
