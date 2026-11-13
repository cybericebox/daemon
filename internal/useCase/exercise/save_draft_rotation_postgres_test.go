package exercise_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/keyRotationRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	exercise "github.com/cybericebox/daemon/internal/useCase/exercise"
	"github.com/cybericebox/daemon/internal/useCase/keyrotation"
	"github.com/cybericebox/daemon/pkg/secret"
)

// hookWorker runs a function once, just before the first catalog transaction of a request opens.
type hookWorker struct {
	postgres.IUnitOfWorker[exercise.CatalogRepository]
	once   sync.Once
	before func()
}

func (w *hookWorker) UnitOfWork(ctx context.Context) (context.Context, exercise.CatalogRepository, postgres.UoW, error) {
	w.once.Do(w.before)
	return w.IUnitOfWorker.UnitOfWork(ctx)
}

// A draft save whose merge source (the stored draft, with its secrets) was read before the re-encryption pass rewrote the
// row must not write what it read: it gets ErrExerciseModified and writes nothing. After a re-read the save succeeds
// and every secret of the row is on the current key. This pins finding 7: there is no cache that could bring an
// old-key ciphertext back, only this window, and it is closed by the snapshot comparison under the lock.
func TestSaveDraft_StaleMergeSourceAfterReencryptionIsRefused(t *testing.T) {
	db, ctx, user, e := correctionTarget(t)
	repo := exerciseRepo.New(db.Queries)

	oldKey, newKey := strings.Repeat("11", 32), strings.Repeat("22", 32)
	oldRing, err := secret.New("k1:" + oldKey)
	require.NoError(t, err)
	ring, err := secret.New("k2:" + newKey + ",k1:" + oldKey)
	require.NoError(t, err)

	variantID := uuid.Must(uuid.NewV7())
	sealed, err := oldRing.EncryptWithContext([]byte("db-password"), exercise.EnvSecretContext(variantID, "web", "DB_PASS"))
	require.NoError(t, err)
	stored := draftVariantsWithID(variantID, sealed, true)
	_, err = repo.UpsertDraft(ctx, e.ID, uuid.Must(uuid.NewV7()), exerciseModel.ExerciseVersion{Variants: stored}, importJobT0, uuid.NullUUID{})
	require.NoError(t, err)

	rotate := func() {
		rep, runErr := keyrotation.New(keyRotationRepo.New(db.Pool, keyrotation.Contexts()),
			keyrotation.Rings{keyRotationRepo.FamilyExercise: ring}).Run(ctx, keyRotationRepo.FamilyExercise, keyrotation.Options{})
		require.NoError(t, runErr)
		require.Zero(t, rep.Failed())
	}
	base := postgres.NewUnitOfWorker[exercise.CatalogRepository](postgres.NewUoWFactory(db.Pool))
	worker := &hookWorker{IUnitOfWorker: base, before: rotate}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: db.Queries, Cipher: ring, Media: newFakeMedia(), CatalogUoW: worker})

	draftSecret := func() string {
		d, getErr := repo.GetDraft(ctx, e.ID)
		require.NoError(t, getErr)
		return d.Variants[0].Topology.Devices[0].Bootstrap.Env[0].Value
	}
	incoming := func() []exerciseModel.Variant { return draftVariantsWithID(variantID, "", true) } // blank secret = keep

	// The first save read the draft with the old-key value, the pass rewrites it, the save then asks for the lock.
	_, err = uc.SaveDraft(ctx, e.ID, exercise.SaveDraftInput{Variants: incoming(), SavedBy: user.ID})
	require.ErrorIs(t, err, exerciseModel.ErrExerciseModified.Err(), "a save built on a read from before the pass is refused")
	afterPass := draftSecret()
	require.Equal(t, "k2", keyOfCiphertext(afterPass), "the pass moved the stored value to the current key")
	require.NotContains(t, afterPass, sealed)

	// Nothing was written by the refused save: the stored value is exactly what the pass wrote.
	plain, err := ring.DecryptWithContext(afterPass, exercise.EnvSecretContext(variantID, "web", "DB_PASS"))
	require.NoError(t, err)
	require.Equal(t, "db-password", string(plain))

	// After a re-read the same save goes through and the row holds only current-key secrets.
	_, err = uc.SaveDraft(ctx, e.ID, exercise.SaveDraftInput{Variants: incoming(), SavedBy: user.ID})
	require.NoError(t, err)
	saved := draftSecret()
	require.Equal(t, "k2", keyOfCiphertext(saved))
	plain, err = ring.DecryptWithContext(saved, exercise.EnvSecretContext(variantID, "web", "DB_PASS"))
	require.NoError(t, err)
	require.Equal(t, "db-password", string(plain))
}

func keyOfCiphertext(ct string) string { id, _ := secret.KeyID(ct); return id }
