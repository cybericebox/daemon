package event

import "testing"

func TestCSVTextNeutralizesFormulas(t *testing.T) {
	for in, want := range map[string]string{"=SUM(A1)": "'=SUM(A1)", "+1": "'+1", "-cmd": "'-cmd", "@x": "'@x", "ICE{flag}": "ICE{flag}", "": ""} {
		if got := csvText(in); got != want {
			t.Fatalf("csvText(%q) = %q, want %q", in, got, want)
		}
	}
}
