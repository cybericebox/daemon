package postgres

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeTx struct {
	committed  int
	rolledBack int
	commitErr  error
}

func (f *fakeTx) Commit(context.Context) error   { f.committed++; return f.commitErr }
func (f *fakeTx) Rollback(context.Context) error { f.rolledBack++; return nil }

func TestUoW_Save_Commits(t *testing.T) {
	tx := &fakeTx{}
	u := newUoW(context.Background(), tx)
	require.NoError(t, u.Save())
	assert.Equal(t, 1, tx.committed)
	assert.Equal(t, 0, tx.rolledBack)
}

func TestUoW_RestoreAfterSave_IsNoOp(t *testing.T) {
	tx := &fakeTx{}
	u := newUoW(context.Background(), tx)
	require.NoError(t, u.Save())
	u.Restore()
	assert.Equal(t, 1, tx.committed)
	assert.Equal(t, 0, tx.rolledBack)
}

func TestUoW_RestoreWithoutSave_RollsBack(t *testing.T) {
	tx := &fakeTx{}
	u := newUoW(context.Background(), tx)
	u.Restore()
	assert.Equal(t, 0, tx.committed)
	assert.Equal(t, 1, tx.rolledBack)
}

func TestUoW_DoubleSave_CommitsOnce(t *testing.T) {
	tx := &fakeTx{}
	u := newUoW(context.Background(), tx)
	require.NoError(t, u.Save())
	require.NoError(t, u.Save())
	assert.Equal(t, 1, tx.committed)
}

func TestUoW_SaveError_Propagates(t *testing.T) {
	tx := &fakeTx{commitErr: errors.New("boom")}
	u := newUoW(context.Background(), tx)
	assert.Error(t, u.Save())
	u.Restore()
	assert.Equal(t, 1, tx.rolledBack, "rollback expected after failed commit")
}
