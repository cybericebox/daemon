package imageRegistryUseCase

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	imageRegistryModel "github.com/cybericebox/daemon/internal/model/imageRegistry"
	"github.com/cybericebox/daemon/pkg/ociregistry"
	"github.com/cybericebox/daemon/pkg/ociregistry/provider"
)

type deleteCall struct{ account, token, repository, tag string }

type gcAdapter struct {
	fakeAdapter
	calls []deleteCall
	errs  map[string]error
}

func (a *gcAdapter) DeleteTag(_ context.Context, account, token, repository, tag string) error {
	a.calls = append(a.calls, deleteCall{account, token, repository, tag})
	return a.errs[tag]
}

func gcFixture(t *testing.T) (*fixture, *gcAdapter) {
	f := newFixture(t, true)
	a := &gcAdapter{errs: map[string]error{}}
	f.uc.adapter = func(imageRegistryModel.Provider) provider.Adapter { return a }
	f.stored(postgres.ImageRegistrySetting{
		ID: true, Provider: "dockerhub", Account: "acme", Repository: "acme/platform",
		PushTokenCiphertext: f.seal(t, pushToken, "push"), UpdatedAt: t0,
	})
	return f, a
}

func img(digest, tag string) postgres.PlatformImage {
	return postgres.PlatformImage{Digest: digest, Tag: tag, Name: "web", UploadedAt: t0.Add(-48 * time.Hour)}
}

// expectSweep: the blob push sweep always runs, with the 7 day cutoff.
func (f *fixture) expectSweep() {
	f.q.EXPECT().DeleteStaleImageBlobPushes(gomock.Any(), t0.Add(-7*24*time.Hour)).Return(int64(0), nil)
}

func (f *fixture) unreferenced(rows ...postgres.PlatformImage) {
	f.q.EXPECT().ListUnreferencedPlatformImages(gomock.Any(), t0.Add(-24*time.Hour)).Return(rows, nil)
}

var gcBefore = t0.Add(-24 * time.Hour)

// expectCollectable: the per-digest re-check right before the digest's first tag is removed.
func (f *fixture) expectCollectable(digest string, ok bool) {
	f.q.EXPECT().IsDigestCollectable(gomock.Any(), postgres.IsDigestCollectableParams{TargetDigest: digest, Before: gcBefore}).
		Return(pgtype.Bool{Bool: ok, Valid: true}, nil)
}

func (f *fixture) expectRowDeleted(digest, tag string) *gomock.Call {
	return f.q.EXPECT().DeletePlatformImage(gomock.Any(), postgres.DeletePlatformImageParams{Digest: digest, Tag: tag, Before: gcBefore}).Return(int64(1), nil)
}

func TestCollectDeletesEveryTagOfAnUnreferencedDigest(t *testing.T) {
	f, a := gcFixture(t)
	f.expectSweep()
	f.unreferenced(img("sha256:aa", "web-aa"), img("sha256:aa", "old-aa"))
	f.expectCollectable("sha256:aa", true)
	f.expectRowDeleted("sha256:aa", "web-aa")
	f.expectRowDeleted("sha256:aa", "old-aa")

	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err != nil || n != 2 {
		t.Fatalf("n=%d err=%v", n, err)
	}
	want := []deleteCall{{"acme", pushToken, "acme/platform", "web-aa"}, {"acme", pushToken, "acme/platform", "old-aa"}}
	if fmt.Sprint(a.calls) != fmt.Sprint(want) {
		t.Fatalf("calls %v want %v", a.calls, want)
	}
}

func TestCollectNothingUnreferencedDeletesNothing(t *testing.T) {
	f, a := gcFixture(t)
	f.expectSweep()
	f.unreferenced()
	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err != nil || n != 0 || len(a.calls) != 0 {
		t.Fatalf("n=%d err=%v calls=%v", n, err, a.calls)
	}
}

func TestCollectFailureKeepsTheRowAndContinues(t *testing.T) {
	f, a := gcFixture(t)
	a.errs["bad"] = ociregistry.ErrUnauthorized
	a.errs["shared"] = provider.ErrTagShared
	a.errs["gone"] = ociregistry.ErrNotFound
	f.expectSweep()
	f.unreferenced(img("sha256:aa", "bad"), img("sha256:bb", "shared"), img("sha256:cc", "gone"), img("sha256:dd", "ok"))
	for _, d := range []string{"aa", "bb", "cc", "dd"} {
		f.expectCollectable("sha256:"+d, true)
	}
	f.expectRowDeleted("sha256:cc", "gone")
	f.expectRowDeleted("sha256:dd", "ok")

	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err != nil || n != 2 || len(a.calls) != 4 {
		t.Fatalf("n=%d err=%v calls=%d", n, err, len(a.calls))
	}
}

func TestCollectRowDeleteFailureIsReturned(t *testing.T) {
	f, _ := gcFixture(t)
	f.expectSweep()
	f.unreferenced(img("sha256:aa", "one"), img("sha256:bb", "two"))
	f.expectCollectable("sha256:aa", true)
	f.expectCollectable("sha256:bb", true)
	f.q.EXPECT().DeletePlatformImage(gomock.Any(), gomock.Any()).Return(int64(0), errors.New("db down")).Times(1)
	f.expectRowDeleted("sha256:bb", "two")
	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err == nil || n != 1 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestCollectIsANoOpWhenNotConfigured(t *testing.T) {
	f := newFixture(t, true)
	f.empty()
	f.expectSweep()
	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestCollectIsANoOpWithoutACipher(t *testing.T) {
	f := newFixture(t, false)
	f.expectSweep()
	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

func TestCollectSweepsStaleBlobPushes(t *testing.T) {
	f := newFixture(t, true)
	f.empty()
	f.q.EXPECT().DeleteStaleImageBlobPushes(gomock.Any(), t0.Add(-7*24*time.Hour)).Return(int64(3), nil)
	if n, err := f.uc.CollectUnusedTags(context.Background(), t0); err != nil || n != 0 {
		t.Fatalf("n=%d err=%v", n, err)
	}
}

// A digest a version references again between the list and the removal is left alone: no registry call.
func TestCollectSkipsADigestReferencedAgain(t *testing.T) {
	f, a := gcFixture(t)
	f.expectSweep()
	f.unreferenced(img("sha256:aa", "one"), img("sha256:aa", "two"), img("sha256:bb", "three"))
	f.expectCollectable("sha256:aa", false)
	f.expectCollectable("sha256:bb", true)
	f.expectRowDeleted("sha256:bb", "three")
	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err != nil || n != 1 || len(a.calls) != 1 || a.calls[0].tag != "three" {
		t.Fatalf("n=%d err=%v calls=%v", n, err, a.calls)
	}
}

// The row delete is conditional too: refused (a fresh upload in between) means the row is kept and not counted.
func TestCollectKeepsARowTheConditionalDeleteRefused(t *testing.T) {
	f, a := gcFixture(t)
	f.expectSweep()
	f.unreferenced(img("sha256:aa", "one"))
	f.expectCollectable("sha256:aa", true)
	f.q.EXPECT().DeletePlatformImage(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	n, err := f.uc.CollectUnusedTags(context.Background(), t0)
	if err != nil || n != 0 || len(a.calls) != 1 {
		t.Fatalf("n=%d err=%v calls=%v", n, err, a.calls)
	}
}
