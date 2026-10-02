package auth_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	"github.com/cybericebox/daemon/internal/useCase/auth"
	"github.com/cybericebox/daemon/pkg/password"
	"github.com/cybericebox/daemon/pkg/token"
)

// fakeAvatarStorage is a hand-rolled stub for auth.IAvatarStorage — no gomock
// needed since the use case calls it directly (upload/stream/reference sync).
type fakeAvatarStorage struct {
	uploadedName        string
	uploadedContentType string
	uploadedBy          uuid.UUID
	uploadFile          mediaModel.File
	uploadErr           error

	streamedID uuid.UUID
	streamFile io.ReadCloser
	streamMeta mediaModel.File
	streamErr  error

	replaceCalls    int
	replacedRefType string
	replacedRefID   uuid.UUID
	replacedIDs     []uuid.UUID
	replaceErr      error

	refIDs []uuid.UUID
	refErr error
}

func (f *fakeAvatarStorage) UploadFile(_ context.Context, name, contentType string, _ io.Reader, createdBy uuid.UUID) (mediaModel.File, error) {
	f.uploadedName = name
	f.uploadedContentType = contentType
	f.uploadedBy = createdBy
	if f.uploadErr != nil {
		return mediaModel.File{}, f.uploadErr
	}
	return f.uploadFile, nil
}

func (f *fakeAvatarStorage) StreamFile(_ context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error) {
	f.streamedID = id
	if f.streamErr != nil {
		return nil, mediaModel.File{}, f.streamErr
	}
	return f.streamFile, f.streamMeta, nil
}

func (f *fakeAvatarStorage) ReplaceReferences(_ context.Context, refType string, refID uuid.UUID, fileIDs []uuid.UUID) error {
	f.replaceCalls++
	f.replacedRefType = refType
	f.replacedRefID = refID
	f.replacedIDs = fileIDs
	return f.replaceErr
}

func (f *fakeAvatarStorage) GetReferences(_ context.Context, _ string, _ uuid.UUID) ([]uuid.UUID, error) {
	return f.refIDs, f.refErr
}

// fakeLegacyStorage is a hand-rolled stub for auth.IStorageClient — the
// pre-migration direct-storage fallback path only (Get to read, Remove to
// best-effort clean up).
type fakeLegacyStorage struct {
	getKey    string
	getCalls  int
	getReader io.ReadCloser
	getType   string
	getErr    error

	removeKey   string
	removeCalls int
	removeErr   error
}

func (f *fakeLegacyStorage) Get(_ context.Context, key string) (io.ReadCloser, string, error) {
	f.getCalls++
	f.getKey = key
	return f.getReader, f.getType, f.getErr
}

func (f *fakeLegacyStorage) Remove(_ context.Context, key string) error {
	f.removeCalls++
	f.removeKey = key
	return f.removeErr
}

// pngBytes is a minimal valid PNG signature plus padding: enough for
// http.DetectContentType to sniff "image/png".
func pngBytes(n int) []byte {
	sig := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}
	return append(sig, strings.Repeat("x", n)...)
}

func newAvatarUC(t *testing.T, storage auth.IStorageClient, avatar auth.IAvatarStorage) (*auth.AuthUseCase, *postgresMocks.MockQuerier) {
	t.Helper()
	ctrl := gomock.NewController(t)
	repo := postgresMocks.NewMockQuerier(ctrl)
	uc := auth.NewAuthUseCase(auth.Dependencies{
		Repo:     repo,
		Token:    token.MustNew(token.Config{TokenSignature: "test-signing-key-that-is-long-enough"}),
		Password: password.New(password.Config{HashCost: 4}),
		Storage:  storage,
		Avatar:   avatar,
		Config:   config.AuthConfig{},
	})
	return uc, repo
}

func TestUploadAvatar_StoresThroughMediaAndSetsPicture(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	fileID := uuid.Must(uuid.NewV7())
	av := &fakeAvatarStorage{uploadFile: mediaModel.File{ID: fileID, ContentType: "image/png"}}
	st := &fakeLegacyStorage{}
	uc, repo := newAvatarUC(t, st, av)

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			want := "/api/auth/avatar/" + uid.String() + "?v="
			if !strings.HasPrefix(arg.Picture, want) {
				return 0, fmt.Errorf("unexpected picture URL: %q", arg.Picture)
			}
			return 1, nil
		},
	)

	body := pngBytes(100)
	if err := uc.UploadAvatar(context.Background(), uid, strings.NewReader(string(body)), int64(len(body)), "image/png"); err != nil {
		t.Fatalf("UploadAvatar: %v", err)
	}

	if av.uploadedName != "avatar" || av.uploadedContentType != "image/png" || av.uploadedBy != uid {
		t.Fatalf("unexpected UploadFile call: name=%q contentType=%q by=%v", av.uploadedName, av.uploadedContentType, av.uploadedBy)
	}
	if av.replaceCalls != 1 || av.replacedRefType != mediaModel.RefTypeUserAvatar || av.replacedRefID != uid {
		t.Fatalf("unexpected ReplaceReferences call: calls=%d refType=%q refID=%v", av.replaceCalls, av.replacedRefType, av.replacedRefID)
	}
	if len(av.replacedIDs) != 1 || av.replacedIDs[0] != fileID {
		t.Fatalf("want replaced fileIDs=[%v], got %v", fileID, av.replacedIDs)
	}
}

func TestUploadAvatar_RejectsDisallowedType(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	av := &fakeAvatarStorage{}
	st := &fakeLegacyStorage{}
	uc, _ := newAvatarUC(t, st, av)

	body := []byte(strings.Repeat("not an image, plain text padding ", 20))
	err := uc.UploadAvatar(context.Background(), uid, strings.NewReader(string(body)), int64(len(body)), "text/plain")
	if !errors.Is(err, authModel.ErrInvalidAvatarType.Err()) {
		t.Fatalf("want ErrInvalidAvatarType, got %v", err)
	}
	if av.uploadedName != "" || av.replaceCalls != 0 {
		t.Fatalf("media service must not be touched on a rejected type: %+v", av)
	}
}

func TestUploadAvatar_TooLarge(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	av := &fakeAvatarStorage{}
	st := &fakeLegacyStorage{}
	uc, _ := newAvatarUC(t, st, av)

	err := uc.UploadAvatar(context.Background(), uid, strings.NewReader(""), auth.MaxAvatarBytes+1, "image/png")
	if !errors.Is(err, authModel.ErrAvatarTooLarge.Err()) {
		t.Fatalf("want ErrAvatarTooLarge, got %v", err)
	}
	if av.uploadedName != "" || av.replaceCalls != 0 {
		t.Fatalf("media service must not be touched on an oversized upload: %+v", av)
	}
}

func TestUploadAvatar_StorageUnconfigured(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	uc, _ := newAvatarUC(t, nil, nil)

	body := pngBytes(10)
	err := uc.UploadAvatar(context.Background(), uid, strings.NewReader(string(body)), int64(len(body)), "image/png")
	if !errors.Is(err, authModel.ErrStorageUnavailable.Err()) {
		t.Fatalf("want ErrStorageUnavailable, got %v", err)
	}
}

func TestRemoveAvatar_ClearsMediaReferenceRemovesLegacyAndClearsPicture(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	av := &fakeAvatarStorage{}
	st := &fakeLegacyStorage{}
	uc, repo := newAvatarUC(t, st, av)

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid, Picture: "/api/auth/avatar/" + uid.String() + "?v=1"}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, arg postgres.UpdateUserParams) (int64, error) {
			if arg.Picture != "" {
				return 0, fmt.Errorf("want cleared picture, got %q", arg.Picture)
			}
			return 1, nil
		},
	)

	if err := uc.RemoveAvatar(context.Background(), uid); err != nil {
		t.Fatalf("RemoveAvatar: %v", err)
	}
	if av.replaceCalls != 1 || av.replacedRefType != mediaModel.RefTypeUserAvatar || av.replacedRefID != uid || len(av.replacedIDs) != 0 {
		t.Fatalf("want ReplaceReferences cleared to empty set, got %+v", av)
	}
	if st.removeCalls != 1 || st.removeKey != "avatars/"+uid.String() {
		t.Fatalf("want legacy object removed under avatars/<userID>, got calls=%d key=%q", st.removeCalls, st.removeKey)
	}
}

func TestRemoveAvatar_LegacyRemoveFailureIsBestEffort(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	av := &fakeAvatarStorage{}
	st := &fakeLegacyStorage{removeErr: errors.New("s3 unavailable")}
	uc, repo := newAvatarUC(t, st, av)

	repo.EXPECT().GetUserByID(gomock.Any(), uid).Return(postgres.User{ID: uid}, nil)
	repo.EXPECT().UpdateUser(gomock.Any(), gomock.Any()).Return(int64(1), nil)

	if err := uc.RemoveAvatar(context.Background(), uid); err != nil {
		t.Fatalf("RemoveAvatar must succeed despite a legacy-removal failure: %v", err)
	}
	if st.removeCalls != 1 {
		t.Fatalf("want legacy removal attempted once, got %d", st.removeCalls)
	}
}

func TestGetAvatar_StreamsCurrentMediaReference(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	fileID := uuid.Must(uuid.NewV7())
	av := &fakeAvatarStorage{
		refIDs:     []uuid.UUID{fileID},
		streamFile: io.NopCloser(strings.NewReader("img-bytes")),
		streamMeta: mediaModel.File{ID: fileID, ContentType: "image/webp"},
	}
	st := &fakeLegacyStorage{}
	uc, _ := newAvatarUC(t, st, av)

	rc, ct, err := uc.GetAvatar(context.Background(), uid)
	if err != nil {
		t.Fatalf("GetAvatar: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if ct != "image/webp" {
		t.Fatalf("want content type image/webp, got %q", ct)
	}
	if av.streamedID != fileID {
		t.Fatalf("want StreamFile(%v), got %v", fileID, av.streamedID)
	}
	data, _ := io.ReadAll(rc)
	if string(data) != "img-bytes" {
		t.Fatalf("unexpected stream content: %q", data)
	}
	if st.getCalls != 0 {
		t.Fatalf("legacy storage must not be consulted when a media reference exists: calls=%d", st.getCalls)
	}
}

func TestGetAvatar_FallsBackToLegacyWhenNoMediaReference(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	av := &fakeAvatarStorage{}
	st := &fakeLegacyStorage{
		getReader: io.NopCloser(strings.NewReader("legacy-bytes")),
		getType:   "image/jpeg",
	}
	uc, _ := newAvatarUC(t, st, av)

	rc, ct, err := uc.GetAvatar(context.Background(), uid)
	if err != nil {
		t.Fatalf("GetAvatar: %v", err)
	}
	defer func() { _ = rc.Close() }()
	if ct != "image/jpeg" {
		t.Fatalf("want content type image/jpeg, got %q", ct)
	}
	if st.getKey != "avatars/"+uid.String() {
		t.Fatalf("want legacy key avatars/<userID>, got %q", st.getKey)
	}
	data, _ := io.ReadAll(rc)
	if string(data) != "legacy-bytes" {
		t.Fatalf("unexpected legacy stream content: %q", data)
	}
}

func TestGetAvatar_NoReferenceAndStorageUnconfigured(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	av := &fakeAvatarStorage{}
	uc, _ := newAvatarUC(t, nil, av)

	_, _, err := uc.GetAvatar(context.Background(), uid)
	if !errors.Is(err, authModel.ErrStorageUnavailable.Err()) {
		t.Fatalf("want ErrStorageUnavailable, got %v", err)
	}
}

func TestGetAvatar_ReferenceLookupErrorPropagates(t *testing.T) {
	uid := uuid.Must(uuid.NewV7())
	boom := errors.New("db down")
	av := &fakeAvatarStorage{refErr: boom}
	st := &fakeLegacyStorage{}
	uc, _ := newAvatarUC(t, st, av)

	_, _, err := uc.GetAvatar(context.Background(), uid)
	if !errors.Is(err, boom) {
		t.Fatalf("want reference lookup error propagated, got %v", err)
	}
	if st.getCalls != 0 {
		t.Fatalf("legacy fallback must not run when the reference lookup itself failed: calls=%d", st.getCalls)
	}
}

// L14: the Google "picture" is the one URL of the profile the daemon fetches; it may only
// point at Google's avatar hosts.
func TestIsProviderAvatarURL(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://lh3.googleusercontent.com/a/ACg8oc=s96-c": true,
		"https://googleusercontent.com/x":                  true,
		"https://LH3.GoogleUserContent.com./a":             true,
		"http://lh3.googleusercontent.com/a":               false, // not https
		"https://lh3.googleusercontent.com:8443/a":         false, // a port
		"https://user:pw@lh3.googleusercontent.com/a":      false, // credentials
		"https://evilgoogleusercontent.com/a":              false, // suffix without the dot
		"https://googleusercontent.com.evil.test/a":        false,
		"https://169.254.169.254/latest/meta-data":         false,
		"https://localhost/a":                              false,
		"file:///etc/passwd":                               false,
		"":                                                 false,
		"%zz":                                              false,
	} {
		if got := auth.ExportIsProviderAvatarURL(raw); got != want {
			t.Errorf("%q: got %v, want %v", raw, got, want)
		}
	}
}
