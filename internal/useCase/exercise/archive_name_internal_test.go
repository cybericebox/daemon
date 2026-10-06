package exercise

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// A duplicate import of a long Cyrillic name gets a suffix; the cut must not split a letter.
func TestImportedName_CutsCharactersNotBytes(t *testing.T) {
	base := "Тест доступу: VPN і проксі " + strings.Repeat("ш", 30)
	got := importedName(base, 1)
	if !utf8.ValidString(got) {
		t.Fatalf("invalid UTF-8: %q", got)
	}
	if n := utf8.RuneCountInString(got); n > 50 {
		t.Fatalf("name has %d characters, want at most 50", n)
	}
	if !strings.HasSuffix(got, " (імпорт 1)") {
		t.Fatalf("suffix missing: %q", got)
	}
	if importedName("Короткий", 0) != "Короткий" {
		t.Fatal("the first attempt keeps the name")
	}
}
