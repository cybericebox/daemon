package registrycheckJob

import (
	"context"
	"errors"
	"testing"

	"github.com/riverqueue/river"
	"github.com/stretchr/testify/require"

	jobsModel "github.com/cybericebox/daemon/internal/model/jobs"
)

type fakeUseCase struct {
	calls int
	err   error
}

func (f *fakeUseCase) RunRegistryChecks(context.Context) error { f.calls++; return f.err }

func TestWorkerRunsOnePass(t *testing.T) {
	uc := &fakeUseCase{}
	require.NoError(t, NewWorker(uc).Work(context.Background(), &river.Job[jobsModel.RegistryCheckArgs]{}))
	require.Equal(t, 1, uc.calls)

	uc.err = errors.New("listing failed")
	require.Error(t, NewWorker(uc).Work(context.Background(), &river.Job[jobsModel.RegistryCheckArgs]{}))
}

func TestKind(t *testing.T) {
	require.Equal(t, "registry_credentials_check", jobsModel.RegistryCheckArgs{}.Kind())
}
