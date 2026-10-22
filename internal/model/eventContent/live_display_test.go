package eventContentModel

import (
	"errors"
	"strings"
	"testing"
)

func TestNormalizeLiveDisplayName(t *testing.T) {
	for in, want := range map[string]string{"  Головний зал ": "Головний зал", "Екран 2": "Екран 2", strings.Repeat("я", 40): strings.Repeat("я", 40)} {
		got, err := NormalizeLiveDisplayName(in)
		if err != nil || got != want {
			t.Fatalf("%q = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "   ", strings.Repeat("я", 41), "a\nb", "a\x00b", string([]byte{0xff})} {
		if _, err := NormalizeLiveDisplayName(bad); !errors.Is(err, ErrLiveScreenNameInvalid.Err()) {
			t.Fatalf("%q: %v", bad, err)
		}
	}
}

func TestDefaultLiveDisplayNameTakesTheNextFreeNumber(t *testing.T) {
	if got := DefaultLiveDisplayName([]LiveDisplay{{Name: "Екран 1"}}); got != "Екран 2" {
		t.Fatalf("got %q", got)
	}
	// «Екран 2» was deleted, «Екран 3» stays: the new one is not a second «Екран 3».
	if got := DefaultLiveDisplayName([]LiveDisplay{{Name: "Екран 1"}, {Name: "Екран 3"}}); got != "Екран 4" {
		t.Fatalf("got %q", got)
	}
	if got := DefaultLiveDisplayName([]LiveDisplay{{Name: "Зал"}, {Name: "Фойє"}}); got != "Екран 3" {
		t.Fatalf("got %q", got)
	}
}
