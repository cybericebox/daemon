package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/gofrs/uuid"
	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/rs/zerolog/log"

	"github.com/cybericebox/daemon/internal/config"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	authModel "github.com/cybericebox/daemon/internal/model/auth"
	"github.com/cybericebox/daemon/internal/model/errorJournal"
	passkeyModel "github.com/cybericebox/daemon/internal/model/passkey"
	"github.com/cybericebox/daemon/internal/model/rbac"
	temporalCodeModel "github.com/cybericebox/daemon/internal/model/temporalCode"
	userModel "github.com/cybericebox/daemon/internal/model/user"
	"github.com/cybericebox/daemon/pkg/tools"
)

// passkeyCeremonyTTL is how long the server keeps the challenge of a passkey ceremony: the person has this
// long to answer the authenticator prompt.
const passkeyCeremonyTTL = 5 * time.Minute

// relyingPartyName is the name authenticators show for the platform (the brand never wraps: non-breaking spaces).
const relyingPartyName = "Cyber ICE Box"

// PasskeyCeremony is what the browser needs to start a ceremony: the one-time id of the server half and the
// WebAuthn options (the JSON of PublicKeyCredentialCreationOptions / RequestOptions, wrapped as {"publicKey":...}).
type PasskeyCeremony struct {
	CeremonyID string
	Options    json.RawMessage
}

// newWebAuthn builds the relying party from the single DOMAIN (hosts.Main is DOMAIN): the RP ID is the platform domain itself (not a
// wider registrable domain, so no sibling site can use the keys) and the only origin that may ask for a ceremony
// is the sign-in site. Registration needs a discoverable key with user verification, no attestation.
func newWebAuthn(hosts config.HostsConfig) (*webauthn.WebAuthn, error) {
	if hosts.Main == "" || hosts.ID == "" {
		return nil, errors.New("hosts are not resolved")
	}
	required := true
	return webauthn.New(&webauthn.Config{
		RPID:                  hosts.Main,
		RPDisplayName:         relyingPartyName,
		RPOrigins:             []string{hosts.IDURL("")},
		AttestationPreference: protocol.PreferNoAttestation,
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			RequireResidentKey: &required,
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		},
		Timeouts: webauthn.TimeoutsConfig{
			Login: webauthn.TimeoutConfig{
				Enforce:    true,
				Timeout:    passkeyCeremonyTTL,
				TimeoutUVD: passkeyCeremonyTTL,
			},
			Registration: webauthn.TimeoutConfig{
				Enforce:    true,
				Timeout:    passkeyCeremonyTTL,
				TimeoutUVD: passkeyCeremonyTTL,
			},
		},
	})
}

// passkeyUser adapts an account to the WebAuthn library. The user handle is the account id: an opaque random
// UUID that carries nothing about the person.
type passkeyUser struct {
	user  userModel.User
	creds []webauthn.Credential
}

func (p passkeyUser) WebAuthnID() []byte                         { return p.user.ID.Bytes() }
func (p passkeyUser) WebAuthnName() string                       { return p.user.Email }
func (p passkeyUser) WebAuthnCredentials() []webauthn.Credential { return p.creds }
func (p passkeyUser) WebAuthnDisplayName() string {
	if name := p.user.FirstName + " " + p.user.LastName; len(name) > 1 {
		return name
	}
	return p.user.Email
}

func toCredential(p passkeyModel.Passkey) webauthn.Credential {
	flags := protocol.FlagUserPresent
	if p.UserVerified {
		flags |= protocol.FlagUserVerified
	}
	if p.BackupEligible {
		flags |= protocol.FlagBackupEligible
	}
	if p.BackupState {
		flags |= protocol.FlagBackupState
	}
	transports := make([]protocol.AuthenticatorTransport, 0, len(p.Transports))
	for _, t := range p.Transports {
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}
	return webauthn.Credential{
		ID:        p.CredentialID,
		PublicKey: p.PublicKey,
		Transport: transports,
		Flags:     webauthn.NewCredentialFlags(flags),
		Authenticator: webauthn.Authenticator{
			AAGUID:    p.AAGUID,
			SignCount: p.SignCount,
		},
	}
}

func descriptors(passkeys []passkeyModel.Passkey) []protocol.CredentialDescriptor {
	out := make([]protocol.CredentialDescriptor, 0, len(passkeys))
	for _, p := range passkeys {
		cred := toCredential(p)
		out = append(out, cred.Descriptor())
	}
	return out
}

// passkeyRegistrationCeremony is the stored server half of a registration: it belongs to one person and one
// session, so a ceremony cannot be finished by someone else or from another session.
type passkeyRegistrationCeremony struct {
	UserID    uuid.UUID
	SessionID uuid.UUID
	Session   webauthn.SessionData
}

// passkeyLoginCeremony is the stored server half of a sign-in: only the challenge, the person is not known yet.
type passkeyLoginCeremony struct {
	Session webauthn.SessionData
}

func (u *AuthUseCase) requirePasskeys() error {
	if u.webauthn == nil {
		return model.ErrPlatform.WithMessage("Passkeys are not configured").Err()
	}
	return nil
}

// createPasskeyCeremony stores the server half of a ceremony for passkeyCeremonyTTL and returns its one-time id.
func (u *AuthUseCase) createPasskeyCeremony(
	ctx context.Context,
	codeType int32,
	data any,
) (string, error) {
	return u.createTemporalCodeTTL(ctx, codeType, data, passkeyCeremonyTTL)
}

// consumePasskeyCeremony claims the stored server half once. Unknown, used, expired and mistyped ids look the
// same to the caller; a database failure stays a platform error.
func (u *AuthUseCase) consumePasskeyCeremony(
	ctx context.Context,
	id string,
	codeType int32,
	into any,
) error {
	data, err := u.consumeTemporalCode(ctx, id, codeType)
	if err != nil {
		if errors.Is(err, temporalCodeModel.ErrTemporalCodeInvalidCode.Err()) ||
			errors.Is(err, temporalCodeModel.ErrTemporalCodeExpired.Err()) {
			return passkeyModel.ErrPasskeyCeremonyInvalid.WithError(err).Err()
		}
		return err
	}
	if err = json.Unmarshal(data, into); err != nil {
		return passkeyModel.ErrPasskeyCeremonyInvalid.WithError(err).Err()
	}
	return nil
}

// BeginPasskeyRegistration starts adding a passkey to the signed-in account. The owner is re-confirmed first
// (the password, or a sign-in from the last minutes for an account without one): a stolen session alone must not
// be able to plant its own key.
func (u *AuthUseCase) BeginPasskeyRegistration(
	ctx context.Context,
	userID uuid.UUID,
	currentPassword string,
) (PasskeyCeremony, error) {
	if err := u.requirePasskeys(); err != nil {
		return PasskeyCeremony{}, err
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok || claims.SessionID == uuid.Nil {
		return PasskeyCeremony{}, authModel.ErrAuthReauthRequired.Err()
	}
	if err := u.reauthenticate(ctx, userID, currentPassword); err != nil {
		return PasskeyCeremony{}, err
	}
	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return PasskeyCeremony{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to get user").
			Err()
	}
	existing, err := u.passkeys.ListByUser(ctx, userID)
	if err != nil {
		return PasskeyCeremony{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to list passkeys").
			Err()
	}
	if len(existing) >= passkeyModel.MaxPerUser {
		return PasskeyCeremony{}, passkeyModel.ErrPasskeyLimit.Err()
	}

	required := true
	creation, session, err := u.webauthn.BeginRegistration(
		passkeyUser{user: user},
		webauthn.WithAuthenticatorSelection(protocol.AuthenticatorSelection{
			RequireResidentKey: &required,
			ResidentKey:        protocol.ResidentKeyRequirementRequired,
			UserVerification:   protocol.VerificationRequired,
		}),
		webauthn.WithConveyancePreference(protocol.PreferNoAttestation),
		webauthn.WithExclusions(descriptors(existing)),
	)
	if err != nil {
		return PasskeyCeremony{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to begin passkey registration").
			Err()
	}
	return u.passkeyCeremonyOf(
		ctx,
		temporalCodeModel.PasskeyRegistrationCodeType,
		passkeyRegistrationCeremony{
			UserID:    userID,
			SessionID: claims.SessionID,
			Session:   *session,
		},
		creation,
	)
}

func (u *AuthUseCase) passkeyCeremonyOf(
	ctx context.Context,
	codeType int32,
	stored, options any,
) (PasskeyCeremony, error) {
	raw, err := json.Marshal(options)
	if err != nil {
		return PasskeyCeremony{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to encode passkey options").
			Err()
	}
	id, err := u.createPasskeyCeremony(ctx, codeType, stored)
	if err != nil {
		return PasskeyCeremony{}, err
	}
	return PasskeyCeremony{CeremonyID: id, Options: raw}, nil
}

// FinishPasskeyRegistration verifies the authenticator's answer (origin, RP ID hash, challenge, user
// verification) and stores the new passkey under the person-given name.
func (u *AuthUseCase) FinishPasskeyRegistration(
	ctx context.Context,
	userID uuid.UUID,
	ceremonyID, name string,
	credential []byte,
) (passkeyModel.Passkey, error) {
	if err := u.requirePasskeys(); err != nil {
		return passkeyModel.Passkey{}, err
	}
	name, ok := passkeyModel.NormalizeName(name)
	if !ok {
		return passkeyModel.Passkey{}, passkeyModel.ErrPasskeyInvalidName.Err()
	}
	claims, ok := rbac.CurrentUserSessionFromContext(ctx)
	if !ok || claims.SessionID == uuid.Nil {
		return passkeyModel.Passkey{}, authModel.ErrAuthReauthRequired.Err()
	}

	var ceremony passkeyRegistrationCeremony
	if err := u.consumePasskeyCeremony(
		ctx,
		ceremonyID,
		temporalCodeModel.PasskeyRegistrationCodeType,
		&ceremony,
	); err != nil {
		return passkeyModel.Passkey{}, err
	}
	if ceremony.UserID != userID || ceremony.SessionID != claims.SessionID {
		return passkeyModel.Passkey{}, passkeyModel.ErrPasskeyCeremonyInvalid.
			WithError(
				errors.New("passkey registration: ceremony belongs to another person or session"),
			).Err()
	}

	user, err := u.users.GetByID(ctx, userID)
	if err != nil {
		return passkeyModel.Passkey{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to get user").
			Err()
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(credential)
	if err != nil {
		return passkeyModel.Passkey{}, passkeyModel.ErrPasskeyRegistrationFailed.WithError(err).
			Err()
	}
	created, err := u.webauthn.CreateCredential(passkeyUser{user: user}, ceremony.Session, parsed)
	if err != nil {
		return passkeyModel.Passkey{}, passkeyModel.ErrPasskeyRegistrationFailed.WithError(err).
			Err()
	}
	// A key that did not prove user verification is no sign-in method of its own.
	if !created.Flags.UserVerified {
		return passkeyModel.Passkey{}, passkeyModel.ErrPasskeyRegistrationFailed.
			WithError(errors.New("passkey registration: user verification missing")).Err()
	}

	transports := make([]string, 0, len(created.Transport))
	for _, t := range created.Transport {
		transports = append(transports, string(t))
	}
	stored, within, err := u.passkeys.Create(ctx, passkeyModel.Passkey{
		ID:             tools.NewUUIDv7(),
		UserID:         userID,
		CredentialID:   created.ID,
		PublicKey:      created.PublicKey,
		SignCount:      created.Authenticator.SignCount,
		AAGUID:         created.Authenticator.AAGUID,
		Transports:     transports,
		UserVerified:   true,
		BackupEligible: created.Flags.BackupEligible,
		BackupState:    created.Flags.BackupState,
		Name:           name,
		CreatedAt:      time.Now(),
	}, passkeyModel.MaxPerUser)
	if err != nil {
		if pgErr, isPg := errors.AsType[*pgconn.PgError](
			err,
		); isPg &&
			pgErr.Code == pgerrcode.UniqueViolation {
			return passkeyModel.Passkey{}, passkeyModel.ErrPasskeyRegistrationFailed.
				WithError(
					errors.New("passkey registration: credential is already registered"),
				).Err()
		}
		return passkeyModel.Passkey{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to store passkey").
			Err()
	}
	if !within {
		return passkeyModel.Passkey{}, passkeyModel.ErrPasskeyLimit.Err()
	}
	return stored, nil
}

// BeginPasskeySignIn starts a usernameless sign-in: the browser asks the authenticator for any passkey of this
// site, with user verification required.
func (u *AuthUseCase) BeginPasskeySignIn(ctx context.Context) (PasskeyCeremony, error) {
	if err := u.requirePasskeys(); err != nil {
		return PasskeyCeremony{}, err
	}
	assertion, session, err := u.webauthn.BeginDiscoverableLogin(
		webauthn.WithUserVerification(protocol.VerificationRequired),
	)
	if err != nil {
		return PasskeyCeremony{}, model.ErrPlatform.WithError(err).
			WithMessage("Failed to begin passkey sign-in").
			Err()
	}
	return u.passkeyCeremonyOf(
		ctx,
		temporalCodeModel.PasskeyLoginCodeType,
		passkeyLoginCeremony{Session: *session},
		assertion,
	)
}

// passkeyAssertion is what the discoverable-credential lookup found while the library verified an assertion.
type passkeyAssertion struct {
	stored passkeyModel.Passkey
	owner  userModel.User
	// backend is a database failure inside the lookup: unlike a missing key it is a platform error.
	backend error
}

// lookup is the library's DiscoverableUserHandler: it finds the key by its credential id, checks that the user
// handle names its owner and loads the live (not deleted) account.
func (u *AuthUseCase) lookup(
	ctx context.Context,
	found *passkeyAssertion,
) webauthn.DiscoverableUserHandler {
	return func(rawID, userHandle []byte) (webauthn.User, error) {
		var err error
		if found.stored, err = u.passkeys.GetByCredentialID(ctx, rawID); err != nil {
			return nil, found.lookupFailed(err, "unknown credential")
		}
		handle, handleErr := uuid.FromBytes(userHandle)
		if handleErr != nil || handle != found.stored.UserID {
			return nil, errors.New("user handle does not own the credential")
		}
		if found.owner, err = u.users.GetByID(ctx, found.stored.UserID); err != nil {
			return nil, found.lookupFailed(err, "account is gone")
		}
		return passkeyUser{
			user:  found.owner,
			creds: []webauthn.Credential{toCredential(found.stored)},
		}, nil
	}
}

func (a *passkeyAssertion) lookupFailed(err error, reason string) error {
	if !repositoryTools.IsObjectNotFoundError(err) {
		a.backend = err
	}
	return errors.New(reason)
}

// PasskeySignIn verifies the assertion and, on success, creates the session exactly as password and Google
// sign-in do. Every way the assertion can be wrong answers the same ErrPasskeySignInFailed: the client learns
// nothing about which keys or accounts exist.
func (u *AuthUseCase) PasskeySignIn(
	ctx context.Context,
	ceremonyID, redirect string,
	assertion []byte,
	meta authModel.SessionMetadata,
) (string, string, error) {
	if err := u.requirePasskeys(); err != nil {
		return "", "", err
	}
	safeRedirect := u.resolveRedirect(redirect)

	var ceremony passkeyLoginCeremony
	if err := u.consumePasskeyCeremony(
		ctx,
		ceremonyID,
		temporalCodeModel.PasskeyLoginCodeType,
		&ceremony,
	); err != nil {
		return "", "", err
	}
	parsed, err := protocol.ParseCredentialRequestResponseBytes(assertion)
	if err != nil {
		return "", "", passkeyModel.ErrPasskeySignInFailed.WithError(err).Err()
	}

	var found passkeyAssertion
	_, verified, err := u.webauthn.ValidatePasskeyLogin(
		u.lookup(ctx, &found),
		ceremony.Session,
		parsed,
	)
	if found.backend != nil {
		return "", "", model.ErrPlatform.WithError(found.backend).
			WithMessage("Failed to look up passkey").
			Err()
	}
	if err != nil {
		return "", "", passkeyModel.ErrPasskeySignInFailed.WithError(err).Err()
	}

	// Only after the signature checked out: revealing "blocked" earlier would let anyone probe account status.
	if err = refuseBlocked(&found.owner); err != nil {
		return "", "", err
	}
	if err = u.recordPasskeyUse(ctx, found.stored, verified); err != nil {
		return "", "", err
	}

	cookie, err := u.createSession(ctx, found.owner.ID, meta)
	if err != nil {
		return "", "", err
	}
	return cookie, safeRedirect, nil
}

// recordPasskeyUse stores the new counter. A counter that did not advance means a second copy of the key exists
// (or the assertion is a replay): refuse and report.
func (u *AuthUseCase) recordPasskeyUse(
	ctx context.Context,
	stored passkeyModel.Passkey,
	verified *webauthn.Credential,
) error {
	if verified.Authenticator.CloneWarning {
		u.reportPasskeyClone(stored, "the signature counter did not advance")
		return passkeyModel.ErrPasskeySignInFailed.WithError(errors.New("passkey counter regression")).
			Err()
	}
	advanced, err := u.passkeys.RecordUse(
		ctx,
		stored.ID,
		verified.Authenticator.SignCount,
		verified.Flags.BackupState,
		time.Now(),
	)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to record passkey use").Err()
	}
	if !advanced {
		u.reportPasskeyClone(stored, "the same counter value was used twice")
		return passkeyModel.ErrPasskeySignInFailed.WithError(errors.New("passkey counter replay")).
			Err()
	}
	return nil
}

// reportPasskeyClone writes a counter regression to the error journal (one group per passkey, announced when
// new) and the log. The person's other sign-in methods are untouched.
func (u *AuthUseCase) reportPasskeyClone(p passkeyModel.Passkey, why string) {
	uid := p.UserID
	log.Warn().
		Str("user_id", uid.String()).
		Str("passkey_id", p.ID.String()).
		Msg("Passkey clone suspected: " + why)
	errorJournal.Report(errorJournal.Event{
		Kind:       errorJournal.KindHTTP403,
		Source:     "POST /api/auth/passkeys/sign-in/finish",
		Method:     "POST",
		Route:      "/api/auth/passkeys/sign-in/finish",
		HTTPStatus: http.StatusUnauthorized,
		Message:    fmt.Sprintf("Passkey clone suspected: %s", why),
		UserID:     &uid,
		Key:        "passkey-clone:" + p.ID.String(),
		Details:    map[string]string{"passkey_id": p.ID.String()},
	})
}

// ListPasskeys returns the account's passkeys, oldest first.
func (u *AuthUseCase) ListPasskeys(
	ctx context.Context,
	userID uuid.UUID,
) ([]passkeyModel.Passkey, error) {
	items, err := u.passkeys.ListByUser(ctx, userID)
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list passkeys").Err()
	}
	return items, nil
}

// RenamePasskey changes the name of the account's passkey.
func (u *AuthUseCase) RenamePasskey(
	ctx context.Context,
	userID, passkeyID uuid.UUID,
	name string,
) error {
	name, ok := passkeyModel.NormalizeName(name)
	if !ok {
		return passkeyModel.ErrPasskeyInvalidName.Err()
	}
	found, err := u.passkeys.Rename(ctx, userID, passkeyID, name)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to rename passkey").Err()
	}
	if !found {
		return passkeyModel.ErrPasskeyNotFound.Err()
	}
	return nil
}

// RemovePasskey deletes the account's passkey, refusing when it is the last way to sign in (lockout guard).
func (u *AuthUseCase) RemovePasskey(ctx context.Context, userID, passkeyID uuid.UUID) error {
	owned, err := u.passkeys.ListByUser(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list passkeys").Err()
	}
	mine := false
	for _, p := range owned {
		if p.ID == passkeyID {
			mine = true
			break
		}
	}
	if !mine {
		return passkeyModel.ErrPasskeyNotFound.Err()
	}
	methods, err := u.users.CountLoginMethods(ctx, userID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to count login methods").Err()
	}
	if methods <= 1 {
		return authModel.ErrNoLoginMethod.WithError(errors.New("passkey-remove: would leave account without any login method")).
			Err()
	}
	found, err := u.passkeys.Delete(ctx, userID, passkeyID)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove passkey").Err()
	}
	if !found {
		return passkeyModel.ErrPasskeyNotFound.Err()
	}
	return nil
}
