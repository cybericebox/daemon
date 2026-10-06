package signalModel_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	signalModel "github.com/cybericebox/daemon/internal/model/signal"
)

type namedHook string

func (h namedHook) Name() string                                     { return string(h) }
func (h namedHook) Handle(context.Context, signalModel.Signal) error { return nil }

func TestHookRegistryReturnsExactThenWildcardHooks(t *testing.T) {
	registry := signalModel.NewHookRegistry()
	registry.RegisterWildcard(namedHook("notification-planner"))
	registry.RegisterExact(testSignalType, namedHook("individual-team"))

	hooks := registry.HooksFor(testSignalType)
	require.Len(t, hooks, 2)
	require.Equal(t, "individual-team", hooks[0].Name())
	require.Equal(t, "notification-planner", hooks[1].Name())
}

func TestHookRegistryRejectsDuplicateNamesForOneSignal(t *testing.T) {
	registry := signalModel.NewHookRegistry()
	registry.RegisterExact(testSignalType, namedHook("individual-team"))
	require.Panics(t, func() {
		registry.RegisterExact(testSignalType, namedHook("individual-team"))
	})
}
