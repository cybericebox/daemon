package userModel

import (
	"errors"
	"testing"
	"time"
)

func TestANameWithBidiOrInvisibleCharactersIsRefused(t *testing.T) {
	for name, bad := range map[string]string{
		"bidi override": "ad" + string(rune(0x202e)) + "min", "zero width": "Ann" + string(rune(0x200b)), "control": "A" + string(rune(7)),
	} {
		if err := ValidName(bad, "Doe"); !errors.Is(err, ErrUserNameInvalid.Err()) {
			t.Errorf("%s as first name: %v", name, err)
		}
		if err := ValidName("Ann", bad); !errors.Is(err, ErrUserNameInvalid.Err()) {
			t.Errorf("%s as last name: %v", name, err)
		}
	}
	if err := ValidName("Олена", "Коваль-Іваненко"); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	u := User{FirstName: "Ann", LastName: "Doe"}
	if err := u.UpdateProfile("ad"+string(rune(0x200b))+"min", "Doe", now); err == nil || u.FirstName != "Ann" {
		t.Fatalf("a refused profile must change nothing: %v %q", err, u.FirstName)
	}
	if err := u.UpdateProfile("Bob", "Roe", now); err != nil || u.FirstName != "Bob" {
		t.Fatalf("a valid profile saves: %v", err)
	}
}
