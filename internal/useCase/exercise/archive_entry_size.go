package exercise

import (
	"archive/zip"
	"compress/flate"
	"errors"
	"fmt"
	"io"
	"time"

	yzip "github.com/yeka/zip"

	"github.com/cybericebox/daemon/pkg/ociregistry"
)

// importStateRetryDelay is the wait between writes of an import's outcome.
var importStateRetryDelay = time.Second

// Failures of the archive itself that are not typed errors: the zip readers' own, and these.
var (
	errUnencryptedEntry = errors.New("protected archive contains an unencrypted entry")
	errPasswordRequired = errors.New("password is required for protected archive")
	errManifestInvalid  = errors.New("invalid manifest.json")
)

// IsArchiveFormatError tells an untyped failure of the archive itself (a broken zip, a wrong password, an invalid
// manifest.json): its text is the author's to read. Any other untyped failure (storage, database) is internal and
// its text never reaches a user.
func IsArchiveFormatError(err error) bool {
	for _, target := range []error{
		zip.ErrFormat, zip.ErrAlgorithm, zip.ErrChecksum, zip.ErrInsecurePath,
		yzip.ErrFormat, yzip.ErrAlgorithm, yzip.ErrChecksum, yzip.ErrPassword, yzip.ErrAuthentication, yzip.ErrDecryption,
		errUnencryptedEntry, errPasswordRequired, errManifestInvalid, ociregistry.ErrSourceCorrupt, io.ErrUnexpectedEOF,
	} {
		if errors.Is(err, target) {
			return true
		}
	}
	var corrupt flate.CorruptInputError
	return errors.As(err, &corrupt)
}

// sizedEntryReader reads an image entry of the zip and gives exactly its declared size: more bytes, fewer bytes,
// or a failure of the zip reader itself (checksum, authentication code, deflate data) is ociregistry.ErrSourceCorrupt,
// the author's archive. Any other read failure (storage) passes through unchanged.
type sizedEntryReader struct {
	r    io.ReadCloser
	size int64
	read int64
}

func (s *sizedEntryReader) Read(p []byte) (int, error) {
	n, err := s.r.Read(p)
	s.read += int64(n)
	switch {
	case s.read > s.size:
		return n, fmt.Errorf("%w: the entry is longer than its declared %d bytes", ociregistry.ErrSourceCorrupt, s.size)
	case errors.Is(err, io.EOF) && s.read != s.size:
		return n, fmt.Errorf("%w: the entry is shorter than its declared %d bytes", ociregistry.ErrSourceCorrupt, s.size)
	case err != nil && !errors.Is(err, io.EOF) && IsArchiveFormatError(err):
		return n, fmt.Errorf("%w: %w", ociregistry.ErrSourceCorrupt, err)
	}
	return n, err
}

func (s *sizedEntryReader) Close() error { return s.r.Close() }
