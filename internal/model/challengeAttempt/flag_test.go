package challengeAttempt

import "testing"

func TestFlagMatches(t *testing.T) {
	cases := []struct {
		name, expected, submitted string
		want                      bool
	}{
		{"exact", "CTF{abc}", "CTF{abc}", true},
		{"trailing spaces", "CTF{abc}", "CTF{abc}  ", true},
		{"leading spaces", "CTF{abc}", "  CTF{abc}", true},
		{"newline", "CTF{abc}", "\nCTF{abc}\r\n", true},
		{"inner spaces kept", "CTF{a b}", "CTF{a  b}", false},
		{"inner spaces equal", "CTF{a b}", "CTF{a b}", true},
		{"case mismatch", "CTF{abc}", "ctf{abc}", false},
		{"different length", "CTF{abc}", "CTF{abcd}", false},
		{"prefix", "CTF{abc}", "CTF{", false},
		{"empty submitted", "CTF{abc}", "   ", false},
		{"empty both", "", "", false},
		{"empty expected", "", "x", false},
	}
	for _, c := range cases {
		if got := FlagMatches(c.expected, c.submitted); got != c.want {
			t.Errorf("%s: FlagMatches(%q, %q) = %v, want %v", c.name, c.expected, c.submitted, got, c.want)
		}
	}
}
