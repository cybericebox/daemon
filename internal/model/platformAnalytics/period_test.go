package platformAnalyticsModel_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	platformAnalyticsModel "github.com/cybericebox/daemon/internal/model/platformAnalytics"
)

var now = time.Date(2026, 9, 30, 13, 20, 0, 0, time.UTC)

func ptr(t time.Time) *time.Time { return &t }

func TestNewPeriodAlignsToWholeDaysAndIncludesToday(t *testing.T) {
	p, err := platformAnalyticsModel.NewPeriod(ptr(now.AddDate(0, 0, -7)), nil, now)
	require.NoError(t, err)
	assert.Equal(t, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC), p.From)
	assert.Equal(t, time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC), p.To)
	assert.Equal(t, 8, p.Days())
	assert.False(t, p.All)
}

func TestNewPeriodWithoutFromIsAllTime(t *testing.T) {
	p, err := platformAnalyticsModel.NewPeriod(nil, nil, now)
	require.NoError(t, err)
	assert.True(t, p.All)
	assert.Equal(t, platformAnalyticsModel.Epoch, p.From)
	_, ok := p.Previous()
	assert.False(t, ok)
}

func TestNewPeriodRejectsInvertedAndHugeWindows(t *testing.T) {
	_, err := platformAnalyticsModel.NewPeriod(ptr(now), ptr(now.Add(-time.Hour)), now)
	assert.Error(t, err)
	_, err = platformAnalyticsModel.NewPeriod(ptr(now.AddDate(-20, 0, 0)), nil, now)
	assert.NoError(t, err, "a bound before the epoch is clamped, not rejected")
	_, err = platformAnalyticsModel.NewPeriod(ptr(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)), ptr(time.Date(2031, 1, 1, 0, 0, 0, 0, time.UTC)), now)
	assert.Error(t, err)
}

func TestPreviousIsTheSameLengthBefore(t *testing.T) {
	p, _ := platformAnalyticsModel.NewPeriod(ptr(now.AddDate(0, 0, -7)), nil, now)
	prev, ok := p.Previous()
	require.True(t, ok)
	assert.Equal(t, p.From, prev.To)
	assert.Equal(t, p.Days(), prev.Days())
}
