package labagent

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"

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
	assert.Equal(t, &infraModel.UnavailableReason{Code: "CGROUP_V2_MISSING", NodesFailed: 1, NodesAsked: 4}, unavailableReasonOf(featureWith(t, "CGROUP_V2_MISSING:1/4")))
	assert.Nil(t, unavailableReasonOf(featureWith(t, "")), "empty: nothing is held back")
	assert.Nil(t, unavailableReasonOf(featureWith(t, "node kind-worker: no cgroup tree")), "free text is dropped")
	assert.Nil(t, unavailableReasonOf(nil))
}
