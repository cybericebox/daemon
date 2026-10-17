package exercise

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
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

// errReadCloser fails every read with err.
type errReadCloser struct{ err error }

func (e errReadCloser) Read([]byte) (int, error) { return 0, e.err }
func (e errReadCloser) Close() error             { return nil }

// A typed failure (the platform's storage error, here a truncated range read) passes through unchanged: it stays a
// retryable platform error, never the author's corrupt archive.
func TestSizedEntryReaderKeepsTypedErrors(t *testing.T) {
	storage := model.ErrPlatform.WithError(io.ErrUnexpectedEOF).WithMessage("Failed to read a stored object").Err()
	_, err := io.ReadAll(&sizedEntryReader{r: errReadCloser{storage}, size: 5})
	if errors.Is(err, ociregistry.ErrSourceCorrupt) || !isTyped(err) || !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Fatalf("err %v", err)
	}
	if IsArchiveFormatError(storage) {
		t.Fatal("a typed platform error is not the archive's")
	}
	// The open wrapper too.
	image := imageFileOf("images/web.tar", 5, func(bool) (io.ReadCloser, error) { return nil, storage })
	if _, err = image.open(); errors.Is(err, ociregistry.ErrSourceCorrupt) || !isTyped(err) {
		t.Fatalf("open err %v", err)
	}
}

// An attachment the parse knows but the archive does not hold is the author's archive error, not an internal one
// (the job tells it with its code and reason).
func TestCopyImportedAttachmentsMissingIsArchiveInvalid(t *testing.T) {
	id := freshID()
	versions := []exerciseModel.ExerciseVersion{{Variants: []exerciseModel.Variant{{Tasks: []exerciseModel.Task{{Attachments: []exerciseModel.AttachmentRef{{FileID: id}}}}}}}}
	u := &ExerciseUseCase{media: uploadOnlyMedia{}}
	for name, parsed := range map[string]ParsedExerciseArchive{
		"no metadata": {Versions: versions},
		"no bytes":    {Versions: versions, FileMeta: map[uuid.UUID]exportedFile{id: {Name: "a.txt"}}},
	} {
		err := u.copyImportedAttachments(context.Background(), &parsed, uuid.Nil)
		if !errors.Is(err, exerciseModel.ErrArchiveInvalid.Err()) {
			t.Fatalf("%s: %v", name, err)
		}
		failure := importJobFailure(err)
		if failure.Code != exerciseModel.ErrArchiveInvalid.Err().StatusCode().FullCode() || failure.Context["reason"] == "" {
			t.Fatalf("%s: job failure %+v", name, failure)
		}
	}
}

// uploadOnlyMedia accepts uploads and references (nothing is expected to reach it here).
type uploadOnlyMedia struct{}

func (uploadOnlyMedia) ReplaceReferences(context.Context, string, uuid.UUID, []uuid.UUID) error {
	return nil
}
func (uploadOnlyMedia) RemoveReferences(context.Context, string, uuid.UUID) error        { return nil }
func (uploadOnlyMedia) RemoveReferencesBatch(context.Context, string, []uuid.UUID) error { return nil }
func (uploadOnlyMedia) UploadFile(context.Context, string, string, io.Reader, uuid.UUID) (mediaModel.File, error) {
	return mediaModel.File{}, errors.New("nothing should be uploaded")
}
