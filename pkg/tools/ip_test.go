package tools

import "testing"

func TestFromStringToNetPrefixSlice(t *testing.T) {
	got, err := FromStringToNetPrefixSlice([]string{"10.0.0.0/24", "192.168.1.0/30"})
	if err != nil || len(got) != 2 || got[0].String() != "10.0.0.0/24" {
		t.Fatalf("parse: %v %v", got, err)
	}
	if _, err := FromStringToNetPrefixSlice([]string{"not-a-cidr"}); err == nil {
		t.Fatal("invalid CIDR must error")
	}
	back := FromNetPrefixToStringSlice(got)
	if len(back) != 2 || back[1] != "192.168.1.0/30" {
		t.Fatalf("round-trip: %v", back)
	}
}
