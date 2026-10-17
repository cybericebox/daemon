package exercise

import (
	"bytes"
	"errors"
	"io"
	"testing"

	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// An image entry reader gives exactly the declared size: more or fewer bytes are a corrupt archive.
func TestSizedEntryReader(t *testing.T) {
	read := func(body string, size int64) error {
		_, err := io.ReadAll(&sizedEntryReader{r: io.NopCloser(bytes.NewReader([]byte(body))), size: size})
		return err
	}
	if err := read("12345", 5); err != nil {
		t.Fatalf("exact: %v", err)
	}
	for _, size := range []int64{4, 6} {
		if err := read("12345", size); !errors.Is(err, ociregistry.ErrSourceCorrupt) {
			t.Fatalf("size %d: %v", size, err)
		}
	}
}
