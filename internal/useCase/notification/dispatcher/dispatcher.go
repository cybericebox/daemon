package dispatcherUseCase

//go:generate go run go.uber.org/mock/mockgen -destination mocks/mock_deps.go -package mocks github.com/cybericebox/daemon/internal/useCase/notification/dispatcher Handler

import (
	"context"
	"time"

	"github.com/gofrs/uuid"

	"github.com/cybericebox/daemon/internal/delivery/repository/broadcastRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/dispatchRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/temporalCodeRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model/notification/types"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/secret"
	"github.com/cybericebox/daemon/pkg/worker"

	// Driver-registration: payload init() self-registers into the type registry,
	// so notificationTypes.Supports/Variables work in the dispatch path.
	_ "github.com/cybericebox/daemon/internal/model/notification/types/payloads"
)

const (
	defaultRetryDelay = 5 * time.Second
	maxDispatchRounds = 3
)

type (
	// repoPort composes the aggregate-repository query slices the dispatcher
	// needs (dispatch rows + recipient lookups); *postgres.Queries satisfies it
	// structurally.
	repoPort interface {
		dispatchRepo.Queries
		userRepo.Queries
		broadcastRepo.Queries
		temporalCodeRepo.Queries
	}

	// Handler is the uniform channel interface. Channel handlers satisfy it structurally.
	Handler interface {
		Channel() notificationTypes.NotificationChannel
		Handle(
			ctx context.Context,
			user userModel.User,
			t notificationTypes.NotificationType,
			vars map[string]any,
			templateID *uuid.UUID,
			scopeEventID ...*uuid.UUID,
		) error
	}

	Dependencies struct {
		Repo     repoPort
		Enqueuer worker.IEnqueuer
		Handlers []Handler
		// Cipher seals the payload of a queued notification (names, addresses, links) so it stays out of
		// the job arguments; the platform secrets cipher. Nil: notifications cannot be queued.
		Cipher     *secret.Cipher
		RetryDelay time.Duration // 0 → defaultRetryDelay (5s); set to time.Millisecond in tests
	}

	NotificationDispatcher struct {
		dispatches *dispatchRepo.Repository
		broadcasts *broadcastRepo.Repository
		users      *userRepo.Repository
		payloads   *payloadStore
		enqueuer   worker.IEnqueuer
		handlers   map[notificationTypes.NotificationChannel]Handler
		retryDelay time.Duration
	}
)

// EnqueueInput is the domain enqueue payload (no River, no JSON).

func NewNotificationDispatcher(deps Dependencies) *NotificationDispatcher {
	hs := make(map[notificationTypes.NotificationChannel]Handler, len(deps.Handlers))
	for _, h := range deps.Handlers {
		hs[h.Channel()] = h
	}
	retryDelay := deps.RetryDelay
	if retryDelay <= 0 {
		retryDelay = defaultRetryDelay
	}
	return &NotificationDispatcher{
		dispatches: dispatchRepo.New(deps.Repo),
		broadcasts: broadcastRepo.New(deps.Repo),
		users:      userRepo.New(deps.Repo),
		payloads:   &payloadStore{codes: temporalCodeRepo.New(deps.Repo), cipher: deps.Cipher},
		enqueuer:   deps.Enqueuer,
		handlers:   hs,
		retryDelay: retryDelay,
	}
}
