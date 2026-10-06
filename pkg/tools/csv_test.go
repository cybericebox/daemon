package tools

import "testing"

func TestCSVTextNeutralizesEveryFormulaStart(t *testing.T) {
	for _, in := range []string{
		"=SUM(A1)", "+1", "-1", "@cmd", "\t=1", "\r=1", "\n=1",
		" =1", "  \t=1", string(rune(0xa0)) + "=1", string(rune(0x200b)) + "=1", string(rune(0xfeff)) + "@x",
		string(rune(0xff1d)) + "1+1", string(rune(0xff0b)) + "1", string(rune(0xff0d)) + "1", string(rune(0xff20)) + "x",
		"\n@x",
	} {
		if got := CSVText(in); got != "'"+in {
			t.Errorf("%q = %q, want a leading apostrophe", in, got)
		}
	}
	for _, in := range []string{"", "plain", "a=b", "1+1", "Олена", "x-y", "'quoted", "email@example.test", "a\n=1"} {
		if got := CSVText(in); got != in {
			t.Errorf("%q changed to %q", in, got)
		}
	}
}
