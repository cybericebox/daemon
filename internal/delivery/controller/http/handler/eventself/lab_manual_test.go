package eventself

import (
	"encoding/json"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestManualLabBodyRequiresDecimalRevisionAndIdempotencyKey(t *testing.T) {
	var input manualLabRequest
	require.NoError(t, json.Unmarshal([]byte(`{"Revision":"7","IdempotencyKey":"00000000-0000-0000-0000-000000000001"}`), &input))
	require.Equal(t, "7", input.Revision)
	require.Error(t, json.Unmarshal([]byte(`{"Revision":7,"IdempotencyKey":"bad"}`), &input))
}
