package auth

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/model"
	tablePrefsModel "github.com/cybericebox/daemon/internal/model/tablePrefs"
)

// GetTablePrefs returns the person's saved view of a table, nil when there is none.
func (u *AuthUseCase) GetTablePrefs(ctx context.Context, userID uuid.UUID, key string) (*tablePrefsModel.Prefs, error) {
	if !tablePrefsModel.ValidKey(key) {
		return nil, tablePrefsModel.ErrTablePrefsInvalid.Err()
	}
	prefs, err := u.tablePrefs.Get(ctx, userID, key)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to get table view").Err()
	}
	return prefs, nil
}

// SaveTablePrefs stores the person's view of a table and returns it as stored.
func (u *AuthUseCase) SaveTablePrefs(ctx context.Context, userID uuid.UUID, key string, prefs tablePrefsModel.Prefs) (tablePrefsModel.Prefs, error) {
	if !tablePrefsModel.ValidKey(key) {
		return tablePrefsModel.Prefs{}, tablePrefsModel.ErrTablePrefsInvalid.Err()
	}
	normalized, raw, ok := tablePrefsModel.Normalize(prefs)
	if !ok {
		return tablePrefsModel.Prefs{}, tablePrefsModel.ErrTablePrefsInvalid.Err()
	}
	stored, err := u.tablePrefs.Save(ctx, userID, key, raw, time.Now(), tablePrefsModel.MaxTablesPerUser)
	if err != nil {
		return tablePrefsModel.Prefs{}, model.ErrPlatform.WithError(err).WithMessage("Failed to save table view").Err()
	}
	if !stored {
		return tablePrefsModel.Prefs{}, tablePrefsModel.ErrTablePrefsLimit.Err()
	}
	return normalized, nil
}

// ResetTablePrefs forgets the person's view of a table (back to the default).
func (u *AuthUseCase) ResetTablePrefs(ctx context.Context, userID uuid.UUID, key string) error {
	if !tablePrefsModel.ValidKey(key) {
		return tablePrefsModel.ErrTablePrefsInvalid.Err()
	}
	if err := u.tablePrefs.Delete(ctx, userID, key); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to reset table view").Err()
	}
	return nil
}
