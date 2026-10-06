package config

import (
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

func TestLogSetup_FollowsEnvironment(t *testing.T) {
	cases := []struct {
		environment string
		console     bool
		mode        string
		level       zerolog.Level
	}{
		{Development, true, gin.DebugMode, zerolog.DebugLevel},
		{Stage, false, gin.ReleaseMode, zerolog.DebugLevel},
		{Production, false, gin.ReleaseMode, zerolog.InfoLevel},
		{"", false, gin.ReleaseMode, zerolog.DebugLevel},
		{"qa", false, gin.ReleaseMode, zerolog.DebugLevel},
	}
	for _, tc := range cases {
		if got := ConsoleLogs(tc.environment); got != tc.console {
			t.Errorf("ConsoleLogs(%q): got %v want %v", tc.environment, got, tc.console)
		}
		if got := GinMode(tc.environment); got != tc.mode {
			t.Errorf("GinMode(%q): got %q want %q", tc.environment, got, tc.mode)
		}
		if got := LogLevel(tc.environment); got != tc.level {
			t.Errorf("LogLevel(%q): got %v want %v", tc.environment, got, tc.level)
		}
	}
}
