package storage

import (
	"context"
	"github.com/cybericebox/daemon/internal/appError"
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
		return "", appError.NewError().WithError(err).WithMessage("failed to get user role from context")
	}
	// only admin can upload banners and tasks
	if useRole != model.AdministratorRole && (storageType == model.BannerStorageType || storageType == model.TaskStorageType) {
		return "", model.ErrForbidden
	}

	link, err := u.service.GetUploadFileLink(ctx, storageType, fileID.String())
	if err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to get upload file link")
	}
	return link, nil
}

func (u *StorageUseCase) GetDownloadFileLink(ctx context.Context, storageType string, fileID uuid.UUID) (string, error) {
	link, err := u.service.GetDownloadFileLink(ctx, storageType, fileID.String())
	if err != nil {
		return "", appError.NewError().WithError(err).WithMessage("failed to get download file link")
	}
	return link, nil
}
