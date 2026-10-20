package infrastructure

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/util/validation"
)

func TestLabelKeysAreValidKubernetesLabelKeys(t *testing.T) {
	keys := []string{LabelInstance, LabelEvent, LabelTeam, LabelTask, LabelVersion, LabelKind}
	assert.Equal(t, []string{"platform/instance", "platform/event", "platform/team", "platform/task", "platform/version", "platform/kind"}, keys)
	for _, k := range keys {
		assert.Empty(t, validation.IsQualifiedName(k), k)
	}
}
