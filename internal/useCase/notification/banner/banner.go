// Package bannerUseCase manages site banners and answers which of them a
// viewer sees.
package bannerUseCase

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/siteBannerRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	notificationModel "github.com/cybericebox/daemon/internal/model/notification"
	siteBannerModel "github.com/cybericebox/daemon/internal/model/siteBanner"
)

type Dependencies struct {
	Repo siteBannerRepo.Queries
	// IsEventMember says whether the user is an approved participant or a manager of the event: the
	// audience of participants banners. Nil: nobody is.
	IsEventMember func(ctx context.Context, eventID, userID uuid.UUID) (bool, error)
}

type SiteBannerUseCase struct {
	banners  *siteBannerRepo.Repository
	now      func() time.Time
	isMember func(ctx context.Context, eventID, userID uuid.UUID) (bool, error)
	snapshot bannerSnapshot
}

func NewSiteBannerUseCase(deps Dependencies) *SiteBannerUseCase {
	return &SiteBannerUseCase{banners: siteBannerRepo.New(deps.Repo), now: time.Now, isMember: deps.IsEventMember}
}

// CreateSiteBanner adds a banner to a scope (nil = platform).
func (u *SiteBannerUseCase) CreateSiteBanner(ctx context.Context, scope *uuid.UUID, actorID uuid.UUID, in siteBannerModel.Input) (siteBannerModel.Banner, error) {
	if err := in.Validate(scope != nil); err != nil {
		return siteBannerModel.Banner{}, err
	}
	b, err := u.banners.Create(ctx, siteBannerModel.New(scope, &actorID, in, u.now()))
	if err != nil {
		return siteBannerModel.Banner{}, model.ErrPlatform.WithError(err).WithMessage("Failed to create the banner").Err()
	}
	u.snapshot.invalidate()
	return b, nil
}

// UpdateSiteBanner edits a banner of the given scope.
func (u *SiteBannerUseCase) UpdateSiteBanner(ctx context.Context, scope *uuid.UUID, id uuid.UUID, in siteBannerModel.Input) (siteBannerModel.Banner, error) {
	if err := in.Validate(scope != nil); err != nil {
		return siteBannerModel.Banner{}, err
	}
	if _, err := u.GetSiteBanner(ctx, scope, id); err != nil {
		return siteBannerModel.Banner{}, err
	}
	b, err := u.banners.Update(ctx, id, in)
	if err != nil {
		return siteBannerModel.Banner{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update the banner").Err()
	}
	u.snapshot.invalidate()
	return b, nil
}

// GetSiteBanner loads a banner of the given scope; another scope's banner is a
// plain not-found.
func (u *SiteBannerUseCase) GetSiteBanner(ctx context.Context, scope *uuid.UUID, id uuid.UUID) (siteBannerModel.Banner, error) {
	b, err := u.banners.Get(ctx, id)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return siteBannerModel.Banner{}, notificationModel.ErrBannerNotFound.Err()
		}
		return siteBannerModel.Banner{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load the banner").Err()
	}
	if !sameScope(b.ScopeEventID, scope) {
		return siteBannerModel.Banner{}, notificationModel.ErrBannerNotFound.Err()
	}
	return b, nil
}

// DeleteSiteBanner removes a banner of the given scope.
func (u *SiteBannerUseCase) DeleteSiteBanner(ctx context.Context, scope *uuid.UUID, id uuid.UUID) error {
	if _, err := u.GetSiteBanner(ctx, scope, id); err != nil {
		return err
	}
	if _, err := u.banners.Delete(ctx, id); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete the banner").Err()
	}
	u.snapshot.invalidate()
	return nil
}

// ListSiteBanners lists the banners of a scope for management.
func (u *SiteBannerUseCase) ListSiteBanners(ctx context.Context, scope *uuid.UUID) ([]siteBannerModel.Banner, error) {
	key := "platform"
	if scope != nil {
		key = scope.String()
	}
	items, err := u.banners.List(ctx, key)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list banners").Err()
	}
	return items, nil
}

func sameScope(a, b *uuid.UUID) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}
