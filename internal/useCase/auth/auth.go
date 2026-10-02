package auth

import (
	"context"
	"io"
	"reflect"
	"sync"
	"time"

	"github.com/gofrs/uuid"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	"github.com/cybericebox/daemon/internal/delivery/repository/sessionRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/temporalCodeRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/delivery/repository/userRepo"
	"github.com/cybericebox/daemon/internal/model"
	mediaModel "github.com/cybericebox/daemon/internal/model/media"
	dispatchModel "github.com/cybericebox/daemon/internal/model/notification/dispatch"
	notificationTypes "github.com/cybericebox/daemon/internal/model/notification/types"
	"github.com/cybericebox/daemon/internal/model/rbac"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/oauth"
	"github.com/cybericebox/daemon/pkg/password"
)

// IRepository is the narrow data port: the User, Session and TemporalCode repos'
// query slices, so the use case holds no postgres.* types. The gomock Querier and
// the real Queries both satisfy it structurally.
type IRepository interface {
	userRepo.Queries
	sessionRepo.Queries
	temporalCodeRepo.Queries
}

// ITokenClient is the crypto port (satisfied by *token.Client).
type ITokenClient interface {
	GenerateSessionCookie(sessionID uuid.UUID, expiresAt time.Time) (string, error)
	ParseSessionCookie(tokenStr string) (uuid.UUID, error)
	GenerateSetupToken(userID uuid.UUID) (string, error)
	GenerateSetupTokenFor(userID uuid.UUID, ttl time.Duration) (string, error)
	ParseSetupToken(tokenStr string) (uuid.UUID, error)
}

// IPasswordClient is the password port (satisfied by *password.Client).
type IPasswordClient interface {
	Matches(plaintext, hashed string) (bool, error)
	Hash(plaintext string) (string, error)
	CheckPasswordComplexity(password string) error
	Complexity() password.ComplexityConfig
}

// INotifier is the dispatch port (satisfied by the aggregate *NotificationDispatcher).
type INotifier interface {
	Notify(ctx context.Context, userID uuid.UUID, n notificationTypes.NotificationPayload, opts ...dispatchModel.NotifyOption) error
}

// IOAuthClient is the OAuth port (satisfied by *oauth.Client).
type IOAuthClient interface {
	GetGoogleLoginURL(redirect string) (loginURL, state string, err error)
	GetGoogleUser(ctx context.Context, code, state string) (*oauth.GoogleUser, string, error)
}

// IStorageClient is the object-store port used ONLY for the legacy avatar
// path: reading (and best-effort cleaning up) pre-migration avatars that were
// written directly to the "avatars/<userID>" key, before avatars moved onto
// the shared media service. New avatar writes never use this port.
type IStorageClient interface {
	Get(ctx context.Context, key string) (io.ReadCloser, string, error)
	Remove(ctx context.Context, key string) error
}

// IAvatarStorage is the narrow media-service port avatar operations need:
// store the sniffed image as a media file, stream it back by ID, and manage
// the "current avatar" reference for a user. Declared here (rather than
// importing the media use case package) to keep useCase/auth free of a
// useCase/media import; satisfied structurally by *mediaUseCase.MediaUseCase,
// wired together in useCase/useCase.go where both use cases are constructed.
type IAvatarStorage interface {
	UploadFile(ctx context.Context, name, contentType string, r io.Reader, createdBy uuid.UUID) (mediaModel.File, error)
	StreamFile(ctx context.Context, id uuid.UUID) (io.ReadCloser, mediaModel.File, error)
	ReplaceReferences(ctx context.Context, refType string, refID uuid.UUID, fileIDs []uuid.UUID) error
	GetReferences(ctx context.Context, refType string, refID uuid.UUID) ([]uuid.UUID, error)
}

type AuthUseCase struct {
	sessions          *sessionRepo.Repository
	users             *userRepo.Repository
	codes             *temporalCodeRepo.Repository
	token             ITokenClient
	password          IPasswordClient
	notifier          INotifier
	oauth             IOAuthClient
	oauthConfigured   bool
	storage           IStorageClient
	storageConfigured bool
	avatar            IAvatarStorage
	cfg               config.AuthConfig
	inboxRequests     IInboxRequests // nil until wired
	limits            *authLimits
	// superAdminMu serializes the operations that can take a super_admin out of
	// service (demote, block, delete): the "last one" check and its write are one decision.
	// In process: with several replicas the window is the length of one request.
	superAdminMu sync.Mutex
}

// IInboxRequests withdraws a removed account's undecided applications for
// every Event manager (satisfied by inbox.RequestRouter).
type IInboxRequests interface {
	UserDeleted(ctx context.Context, userID uuid.UUID) error
}

// SetInboxRequests wires the inbox request router after the dispatcher exists.
func (u *AuthUseCase) SetInboxRequests(requests IInboxRequests) {
	u.inboxRequests = requests
}

type Dependencies struct {
	Repo     IRepository
	Token    ITokenClient
	Password IPasswordClient
	Notifier INotifier
	OAuth    IOAuthClient
	Storage  IStorageClient
	Avatar   IAvatarStorage
	Config   config.AuthConfig
}

// isOAuthConfigured returns true only when deps.OAuth is a non-nil interface
// holding a non-nil concrete value. A plain `deps.OAuth != nil` check is
// insufficient because assigning a typed-nil pointer (e.g. (*oauth.Client)(nil))
// to the IOAuthClient interface yields a non-nil interface that wraps nil —
// calling any method on it would panic. reflect.ValueOf(...).IsNil() detects
// that case so both a true-nil interface and a typed-nil-wrapping interface are
// treated as "not configured".
func isOAuthConfigured(o IOAuthClient) bool {
	if o == nil {
		return false
	}
	v := reflect.ValueOf(o)
	return v.Kind() != reflect.Ptr || !v.IsNil()
}

func NewAuthUseCase(deps Dependencies) *AuthUseCase {
	return &AuthUseCase{
		sessions:          sessionRepo.New(deps.Repo),
		users:             userRepo.New(deps.Repo),
		codes:             temporalCodeRepo.New(deps.Repo),
		token:             deps.Token,
		password:          deps.Password,
		notifier:          deps.Notifier,
		oauth:             deps.OAuth,
		oauthConfigured:   isOAuthConfigured(deps.OAuth),
		storage:           deps.Storage,
		storageConfigured: isStorageConfigured(deps.Storage),
		avatar:            deps.Avatar,
		cfg:               deps.Config,
		limits:            newAuthLimits(),
	}
}

// isStorageConfigured mirrors isOAuthConfigured: true only for a non-nil
// interface holding a non-nil concrete value (guards against a typed-nil).
func isStorageConfigured(s IStorageClient) bool {
	if s == nil {
		return false
	}
	v := reflect.ValueOf(s)
	return v.Kind() != reflect.Ptr || !v.IsNil()
}

// mutateUser loads the User aggregate, applies a domain mutation, and writes
// the whole entity back in one statement. A read miss and a write miss (the
// row vanished in between) both surface ErrUserNotFound.
func (u *AuthUseCase) mutateUser(ctx context.Context, userID uuid.UUID, mutate func(*userModel.User) error) error {
	_, err := u.mutateUserReturning(ctx, userID, userModel.ErrUserNotFound.Err(), mutate)
	return err
}

// mutateUserReturning is the shared fetch → snapshot → mutate → whole-aggregate
// write path behind the optimistic lock, returning the mutated entity for callers
// that need it afterwards (e.g. to create a session). The snapshot is taken BEFORE
// mutate, so the lock always compares the LOADED updated_at, never the value a
// domain touch just wrote — capturing it after the mutation would never match the
// stored row (zero rows every time). notFound is returned on both the initial read
// miss and the post-write re-read miss, so callers pick ErrUserNotFound (404) or
// the anti-enumeration ErrInvalidToken; a row still present on the re-read is a
// concurrent write → ErrUserModified (409).
func (u *AuthUseCase) mutateUserReturning(
	ctx context.Context,
	userID uuid.UUID,
	notFound error,
	mutate func(*userModel.User) error,
) (userModel.User, error) {
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return userModel.User{}, notFound
		}
		return userModel.User{}, model.ErrPlatform.WithError(err).WithMessage("Failed to get user").Err()
	}
	expectedUpdatedAt := user.UpdatedAt // optimistic-lock snapshot, before the mutation touches it
	if err = mutate(&user); err != nil {
		return userModel.User{}, err
	}
	if actor, ok := rbac.CurrentUserSessionFromContext(ctx); ok {
		user.UpdatedBy = uuid.NullUUID{UUID: actor.UserID, Valid: true}
	}
	affected, err := u.users.Update(ctx, user, expectedUpdatedAt)
	if err != nil {
		return userModel.User{}, model.ErrPlatform.WithError(err).WithMessage("Failed to update user").Err()
	}
	if affected == 0 {
		if _, err := u.users.GetByID(ctx, userID); err != nil {
			return userModel.User{}, notFound
		}
		return userModel.User{}, userModel.ErrUserModified.Err()
	}
	return user, nil
}

// deleteUserCascade is the single removal path shared by self-service
// DeleteAccount and admin DeleteUser: soft-delete the aggregate (PII scrub),
// then cut sessions and provider links so the freed email and identities can
// be reused. The last-super-admin lockout guard lives HERE — it is an
// invariant of account removal itself, enforced no matter who calls.
// Permission checks stay with the callers.
func (u *AuthUseCase) deleteUserCascade(ctx context.Context, userID uuid.UUID, authorize func(*userModel.User) error) error {
	u.superAdminMu.Lock()
	defer u.superAdminMu.Unlock()
	if err := u.mutateUser(ctx, userID, func(user *userModel.User) error {
		if authorize != nil {
			if err := authorize(user); err != nil {
				return err
			}
		}
		if user.Role == rbac.RoleSuperAdmin {
			if err := u.guardLastSuperAdmin(ctx, user); err != nil {
				return err
			}
		}
		return user.SoftDelete(time.Now())
	}); err != nil {
		return err
	}
	if _, err := u.sessions.DeleteAllForUser(ctx, userID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete user sessions").Err()
	}
	if _, err := u.users.DeleteProviders(ctx, userID); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to delete user providers").Err()
	}
	if u.inboxRequests != nil {
		// Best-effort: the account is already removed.
		if err := u.inboxRequests.UserDeleted(ctx, userID); err != nil {
			log.Error().Err(err).Str("user_id", userID.String()).Msg("Failed to withdraw deleted user's applications")
		}
	}
	return nil
}
