package event

import (
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestAppearanceTimestampMatchesPostgresPrecision(t *testing.T) {
	saved := time.Date(2026, 10, 16, 10, 0, 0, 123456789, time.UTC)
	persisted := saved.Truncate(time.Microsecond)
	require.True(t, appearanceTimestampMatches(persisted, saved), "PostgreSQL stores microseconds; this remains the same save")
	require.False(t, appearanceTimestampMatches(persisted.Add(time.Microsecond), saved), "a genuinely newer save must block rollback")
}
