package exercise

import (
	"bytes"
	"errors"
	"testing"

	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
)

// A protected archive says what the author has to do: give a password, or another one.
func TestArchivePasswordErrorsAreTyped(t *testing.T) {
	protected, err := BuildProtectedArchiveV1(map[string][]byte{"exercise.yaml": []byte(layoutDoc)}, "right")
	if err != nil {
		t.Fatal(err)
	}
	lim := DefaultLimits()
	if _, _, err := openZipAt(bytes.NewReader(protected), int64(len(protected)), "", lim); !errors.Is(err, exerciseModel.ErrArchivePasswordRequired.Err()) {
		t.Fatalf("no password: %v", err)
	}
	entries, isProtected, err := openZipAt(bytes.NewReader(protected), int64(len(protected)), "wrong", lim)
	if err != nil {
		t.Fatal(err)
	}
	_, err = readArchiveEntries(entries, isProtected, lim)
	if err == nil {
		t.Fatal("a wrong password must not read")
	}
	if typed := ArchivePasswordError(err); !errors.Is(typed, exerciseModel.ErrArchivePasswordWrong.Err()) {
		t.Fatalf("wrong password: %v → %v", err, typed)
	}
	if _, _, err := openZipAt(bytes.NewReader(protected), int64(len(protected)), "right", lim); err != nil {
		t.Fatalf("right password: %v", err)
	}
	other := errors.New("storage down")
	if ArchivePasswordError(other) != other || ArchivePasswordError(nil) != nil {
		t.Fatal("other errors pass through")
	}
}
