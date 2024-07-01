package storage

import (
	"context"
	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/internal/tools"
	"github.com/gofrs/uuid"
	"time"
)

type (
	StorageUseCase struct {
		service IStorageService
	}

	IStorageService interface {
		GetUploadFileLink(ctx context.Context, storageType, fileID string, expires ...time.Duration) (string, error)
		GetDownloadFileLink(ctx context.Context, storageType, fileID string, expires ...time.Duration) (string, error)
	}

	Dependencies struct {
		Service IStorageService
	}
)

func NewUseCase(deps Dependencies) *StorageUseCase {
	return &StorageUseCase{
		service: deps.Service,
	}

}
func (u *StorageUseCase) GetUploadFileLink(ctx context.Context, storageType string, fileID uuid.UUID) (string, error) {
	// check user permission
	useRole, err := tools.GetCurrentUserRoleFromContext(ctx)
	if err != nil {
		return "", err
	}
	// only admin can upload banners and tasks
	if useRole != model.AdministratorRole && (storageType == model.BannerStorageType || storageType == model.TaskStorageType) {
		return "", tools.NewError("permission denied", 403)
	}

	return u.service.GetUploadFileLink(ctx, storageType, fileID.String())
}

func (u *StorageUseCase) GetDownloadFileLink(ctx context.Context, storageType string, fileID uuid.UUID) (string, error) {
	return u.service.GetDownloadFileLink(ctx, storageType, fileID.String())
}
