package tools

import "testing"

func r(code rune) string { return string(code) }

func TestUnsafeDisplayTextCoversControlBidiAndZeroWidth(t *testing.T) {
	for name, s := range map[string]string{
		"control": "a" + r(1) + "b", "newline": "a" + r(10) + "b", "tab": "a" + r(9) + "b",
		"bidi override": "ad" + r(0x202e) + "min", "bidi isolate": "a" + r(0x2066) + "b", "bidi mark": "a" + r(0x200f) + "b",
		"zero width space": "ad" + r(0x200b) + "min", "zero width joiner": "a" + r(0x200d) + "b", "word joiner": "a" + r(0x2060) + "b",
		"bom": r(0xfeff) + "a", "soft hyphen": "a" + r(0xad) + "b", "line separator": "a" + r(0x2028) + "b", "arabic letter mark": "a" + r(0x61c) + "b",
	} {
		if !HasUnsafeDisplayText(s) {
			t.Errorf("%s: want unsafe", name)
		}
	}
	for _, s := range []string{"Олена Коваль", "O'Brien-Smith", "李雷", "Zoë", "Team 42", "Іван Франко", "ice_box.7"} {
		if HasUnsafeDisplayText(s) {
			t.Errorf("%q must be accepted", s)
		}
	}
	if got := StripUnsafeDisplayText("ad" + r(0x200b) + r(0x202e) + "min" + r(10)); got != "admin" {
		t.Errorf("strip = %q", got)
	}
}
