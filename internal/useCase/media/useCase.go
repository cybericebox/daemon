// Package media implements the media subsystem application layer: streamed
// uploads with sha256 dedup, downloads, ownership references and orphan GC.
package media

import (
	"context"
	"io"
	"reflect"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/mediaRepo"
)

// IRepository is the narrow data port: the whole mediaRepo query slice (file
// aggregate + references + GC shapes), so the use case holds no postgres.*
// types. The aggregate repository and the gomock Querier both satisfy it.
type IRepository interface {
	mediaRepo.Queries
}

// IStorage is the object-store port (satisfied by *storage.Client).
type IStorage interface {
	Put(ctx context.Context, key string, r io.Reader, size int64, contentType string) error
	Get(ctx context.Context, key string) (io.ReadCloser, string, error)
	Remove(ctx context.Context, key string) error
	Stat(ctx context.Context, key string) (bool, error)
	Copy(ctx context.Context, src, dst string) error
}

type MediaUseCase struct {
	files             *mediaRepo.Repository
	storage           IStorage
	storageConfigured bool
	cfg               config.MediaConfig
}

type Dependencies struct {
	Repo    IRepository
	Storage IStorage
	Config  config.MediaConfig
}

func NewMediaUseCase(deps Dependencies) *MediaUseCase {
	return &MediaUseCase{
		files:             mediaRepo.New(deps.Repo),
		storage:           deps.Storage,
		storageConfigured: isStorageConfigured(deps.Storage),
		cfg:               deps.Config,
	}
}

// isStorageConfigured mirrors auth's guard: true only for a non-nil interface
// holding a non-nil concrete value (typed-nil safe).
func isStorageConfigured(s IStorage) bool {
	if s == nil {
		return false
	}
	v := reflect.ValueOf(s)
	return v.Kind() != reflect.Ptr || !v.IsNil()
}

// MaxUploadBytes exposes the configured upload cap so delivery-layer callers
// (the multipart handler) can enforce the SAME limit at the HTTP boundary —
// before the request body is parsed into memory/temp files — rather than
// only after this use case has already read the whole payload.
func (u *MediaUseCase) MaxUploadBytes() int64 {
	return u.cfg.MaxUploadBytes
}
