package dispatchModel_test

import (
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
)

func TestDispatchStatuses_StringValues(t *testing.T) {
	assert.Equal(t, "pending", string(dispatchModel.DispatchStatusPending))
	assert.Equal(t, "started", string(dispatchModel.DispatchStatusStarted))
	assert.Equal(t, "done", string(dispatchModel.DispatchStatusDone))
	assert.Equal(t, "done", string(dispatchModel.TargetStatusDone))
	assert.Equal(t, "error", string(dispatchModel.TargetStatusError))
}

func TestWithTemplateID_SetsOption(t *testing.T) {
	id := uuid.Must(uuid.NewV7())
	o := dispatchModel.ApplyNotifyOptions([]dispatchModel.NotifyOption{
		dispatchModel.WithTemplateID(id),
	})
	require.NotNil(t, o.TemplateID, "TemplateID must be set after WithTemplateID")
	assert.Equal(t, id, *o.TemplateID)
}

func TestApplyNotifyOptions_NoTemplateID_IsNil(t *testing.T) {
	o := dispatchModel.ApplyNotifyOptions(nil)
	assert.Nil(t, o.TemplateID, "TemplateID must be nil when WithTemplateID is not passed")
}
