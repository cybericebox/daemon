package media_test

import (
	"context"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	media "github.com/cybericebox/daemon/internal/useCase/media"
)

func TestGetReferences_ReturnsFileIDs(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := media.NewMediaUseCase(media.Dependencies{Repo: q, Storage: newFakeStorage(), Config: config.MediaConfig{MaxUploadBytes: 1024}})

	refID := uuid.Must(uuid.NewV7())
	fileID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetFileReferenceIDs(gomock.Any(), postgres.GetFileReferenceIDsParams{
		RefType: mediaModel.RefTypeUserAvatar, RefID: refID,
	}).Return([]uuid.UUID{fileID}, nil)

	got, err := uc.GetReferences(context.Background(), mediaModel.RefTypeUserAvatar, refID)
	if err != nil {
		t.Fatalf("GetReferences: %v", err)
	}
	if len(got) != 1 || got[0] != fileID {
		t.Fatalf("want [%v], got %v", fileID, got)
	}
}

func TestGetReferences_EmptyWhenUnreferenced(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := media.NewMediaUseCase(media.Dependencies{Repo: q, Storage: newFakeStorage(), Config: config.MediaConfig{MaxUploadBytes: 1024}})

	refID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetFileReferenceIDs(gomock.Any(), gomock.Any()).Return([]uuid.UUID{}, nil)

	got, err := uc.GetReferences(context.Background(), mediaModel.RefTypeUserAvatar, refID)
	if err != nil {
		t.Fatalf("GetReferences: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("want empty slice, got %v", got)
	}
}

func TestGetReferences_PropagatesRepoErrorAsPlatformError(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	uc := media.NewMediaUseCase(media.Dependencies{Repo: q, Storage: newFakeStorage(), Config: config.MediaConfig{MaxUploadBytes: 1024}})

	refID := uuid.Must(uuid.NewV7())
	q.EXPECT().GetFileReferenceIDs(gomock.Any(), gomock.Any()).Return(nil, errors.New("db down"))

	if _, err := uc.GetReferences(context.Background(), mediaModel.RefTypeUserAvatar, refID); err == nil {
		t.Fatal("want error propagated")
	}
}
