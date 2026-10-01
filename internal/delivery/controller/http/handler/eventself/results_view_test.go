package eventself

import "testing"

func TestResultsViewAcceptsOnlyPageAndLive(t *testing.T) {
	if live, err := resultsView(""); err != nil || live {
		t.Fatalf("empty view = %v, %v", live, err)
	}
	if live, err := resultsView("live"); err != nil || !live {
		t.Fatalf("live view = %v, %v", live, err)
	}
	if _, err := resultsView("admin"); err == nil {
		t.Fatal("unknown view must be rejected")
	}
}
