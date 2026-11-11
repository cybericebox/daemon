package challengeAttempt_test

import (
	"testing"

	"github.com/cybericebox/daemon/internal/model/challengeAttempt"
)

func TestAnswerMatches(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		expected, typed        string
		ignoreCase, trim, want bool
	}{
		{"exact", "OSPF", "OSPF", false, true, true},
		{"case differs", "OSPF", "ospf", false, true, false},
		{"case ignored", "OSPF", "ospf", true, true, true},
		{"edge spaces dropped on both sides", " OSPF ", "\tOSPF\n", false, true, true},
		{"edge spaces kept", "OSPF", " OSPF", false, false, false},
		{"edge spaces kept and equal", " OSPF", " OSPF", false, false, true},
		{"inner spaces always count", "a b", "a  b", true, true, false},
		{"empty typed value never matches", "x", "  ", false, true, false},
		{"empty expected value never matches", "", "", false, false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := challengeAttempt.AnswerMatches(
				tc.expected,
				tc.typed,
				tc.ignoreCase,
				tc.trim,
			); got != tc.want {
				t.Fatalf(
					"AnswerMatches(%q, %q, ignoreCase=%v, trim=%v) = %v",
					tc.expected,
					tc.typed,
					tc.ignoreCase,
					tc.trim,
					got,
				)
			}
		})
	}
}
