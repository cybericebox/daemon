package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	tablePrefsModel "github.com/cybericebox/daemon/internal/model/tablePrefs"
)

func TestTablePrefs_SaveGetResetAreKeyedOnTheCaller(t *testing.T) {
	uc, repo := newExportUC(t)
	uid := uuid.Must(uuid.NewV7())
	var saved []byte
	repo.EXPECT().UpsertUserTablePrefs(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, a postgres.UpsertUserTablePrefsParams) (int64, error) {
			if a.UserID != uid || a.TableKey != "admin.users" || a.MaxTables != tablePrefsModel.MaxTablesPerUser {
				t.Errorf("wrong save arguments: %+v", a)
			}
			saved = a.Prefs
			return 1, nil
		})
	repo.EXPECT().GetUserTablePrefs(gomock.Any(), postgres.GetUserTablePrefsParams{UserID: uid, TableKey: "admin.users"}).
		DoAndReturn(func(context.Context, postgres.GetUserTablePrefsParams) (postgres.GetUserTablePrefsRow, error) {
			return postgres.GetUserTablePrefsRow{Prefs: saved, UpdatedAt: time.Now()}, nil
		})
	repo.EXPECT().DeleteUserTablePrefs(gomock.Any(), postgres.DeleteUserTablePrefsParams{UserID: uid, TableKey: "admin.users"}).Return(int64(1), nil)

	in := tablePrefsModel.Prefs{ColumnOrder: []string{"name", "email"}, PageSize: 25, Sort: &tablePrefsModel.Sort{Key: "name", Dir: "asc"}}
	if _, err := uc.SaveTablePrefs(context.Background(), uid, "admin.users", in); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := uc.GetTablePrefs(context.Background(), uid, "admin.users")
	if err != nil || got == nil || got.PageSize != 25 || got.Sort.Key != "name" || len(got.ColumnOrder) != 2 {
		t.Fatalf("get: %+v %v", got, err)
	}
	if err = uc.ResetTablePrefs(context.Background(), uid, "admin.users"); err != nil {
		t.Fatalf("reset: %v", err)
	}
}

func TestTablePrefs_BadInputNeverReachesTheDatabase(t *testing.T) {
	uc, _ := newExportUC(t) // no repository call is expected
	uid := uuid.Must(uuid.NewV7())
	wantErr(t, ignoreValue(uc.SaveTablePrefs(context.Background(), uid, "Bad Key", tablePrefsModel.Prefs{})), tablePrefsModel.ErrTablePrefsInvalid)
	wantErr(t, ignoreValue(uc.SaveTablePrefs(context.Background(), uid, "ok", tablePrefsModel.Prefs{PageSize: -5})), tablePrefsModel.ErrTablePrefsInvalid)
	_, err := uc.GetTablePrefs(context.Background(), uid, "../x")
	wantErr(t, err, tablePrefsModel.ErrTablePrefsInvalid)
	wantErr(t, uc.ResetTablePrefs(context.Background(), uid, ""), tablePrefsModel.ErrTablePrefsInvalid)
}

func TestTablePrefs_NewTableOverTheCapIsRefused(t *testing.T) {
	uc, repo := newExportUC(t)
	repo.EXPECT().UpsertUserTablePrefs(gomock.Any(), gomock.Any()).Return(int64(0), nil)
	_, err := uc.SaveTablePrefs(context.Background(), uuid.Must(uuid.NewV7()), "one-more", tablePrefsModel.Prefs{})
	wantErr(t, err, tablePrefsModel.ErrTablePrefsLimit)
}

func ignoreValue[T any](_ T, err error) error { return err }
