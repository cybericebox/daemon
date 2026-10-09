package event

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSizingFallsBackToFiveOnlyWhenUsersUnknown(t *testing.T) {
	require.Equal(t, 3, plannedUsers(8, 3, true, true))
	require.Equal(t, 8, plannedUsers(8, 3, true, false))
	require.Equal(t, 5, plannedUsers(0, 0, false, false))
	require.Equal(t, 1, plannedUsers(0, 0, true, true))
}
