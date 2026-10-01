package infrastructure

import (
	"net/http"

	"github.com/cybericebox/daemon/internal/model"
	"github.com/cybericebox/daemon/pkg/err"
)

// next free detail code: 17

var (
	// ErrInfrastructureUnavailable blocks any operation that needs the lab
	// infrastructure agent when it is not connected. It is deliberately an
	// explicit error, never a silent no-op or a mere warning: a process must
	// know there is no infrastructure to use rather than believe its request
	// succeeded. The delivery/UI layer also reads the availability up front (see
	// the infrastructure use case) so it can block such actions before they are
	// ever attempted.
	ErrInfrastructureUnavailable = err.ErrConflict.WithObjectCode(model.InfrastructureObjectCode).
					WithMessage("infrastructure agent is not connected").
					WithDetailCode(1).
					WithHTTPCode(http.StatusServiceUnavailable)

	// ErrLabAccessRetry answers an HTTP request whose agent write was refused
	// because the previous object of that name is still being deleted. It is
	// transient: the client shows "try again in a minute", nothing is broken.
	ErrLabAccessRetry = err.ErrConflict.WithObjectCode(model.InfrastructureObjectCode).
				WithMessage("The laboratory access is being reset, try again in a minute").
				WithDetailCode(3).
				WithHTTPCode(http.StatusServiceUnavailable)

	// ErrTestLabNotFound: the catalog test laboratory is already gone (stopped or cleaned up).
	ErrTestLabNotFound = err.ErrObjectNotFound.WithObjectCode(model.InfrastructureObjectCode).
				WithMessage("Test laboratory not found").
				WithDetailCode(4)

	// ErrDeviceNotPersistent: reset and rescue act on snapshot-backed state, and this device has none
	// (the lab runs without device state persistence).
	ErrDeviceNotPersistent = err.ErrConflict.WithObjectCode(model.InfrastructureObjectCode).
				WithMessage("The device does not keep its state").
				WithDetailCode(5)

	// ErrDeviceNotFound: the group, the lab or the device is not there (not deployed yet, already removed).
	ErrDeviceNotFound = err.ErrObjectNotFound.WithObjectCode(model.InfrastructureObjectCode).
				WithMessage("Device not found").
				WithDetailCode(6)

	// ErrDeviceActionRetry: the device is still being deleted or recreated. Transient: try again in a minute.
	ErrDeviceActionRetry = err.ErrConflict.WithObjectCode(model.InfrastructureObjectCode).
				WithMessage("The device is being restarted, try again in a minute").
				WithDetailCode(7).
				WithHTTPCode(http.StatusServiceUnavailable)

	// ErrAgentNotFound: no admin-configured agent with this id.
	ErrAgentNotFound = err.ErrObjectNotFound.WithObjectCode(model.InfrastructureObjectCode).
				WithMessage("Infrastructure agent not found").
				WithDetailCode(8)

	// ErrAgentInvalid: the agent form is invalid (name, endpoint, priority, or the TLS material).
	ErrAgentInvalid = err.ErrInvalidData.WithObjectCode(model.InfrastructureObjectCode).
			WithMessage("The infrastructure agent settings are invalid").
			WithDetailCode(9)

	// ErrAgentSecretsUnavailable: the platform secrets key is not configured, so the agent's
	// credentials cannot be stored.
	ErrAgentSecretsUnavailable = err.ErrConflict.WithObjectCode(model.InfrastructureObjectCode).
					WithMessage("The platform secrets key is not configured").
					WithDetailCode(10)

	// ErrAgentInUse: the agent still holds lab groups, so it cannot be deleted; disable it instead.
	ErrAgentInUse = err.ErrConflict.WithObjectCode(model.InfrastructureObjectCode).
			WithMessage("The agent still holds laboratories").
			WithDetailCode(11)

	// ErrAgentReadOnly: the environment agent is configured by the deployment, not in the admin.
	ErrAgentReadOnly = err.ErrConflict.WithObjectCode(model.InfrastructureObjectCode).
				WithMessage("The environment agent is configured by the deployment").
				WithDetailCode(12)

	// ErrAgentExists: an agent with this endpoint is already added.
	ErrAgentExists = err.ErrObjectExists.WithObjectCode(model.InfrastructureObjectCode).
			WithMessage("An agent with this endpoint is already added").
			WithDetailCode(13)

	// ErrAgentEnrollmentRejected: the agent refused the enrollment token (used, expired or unknown) or
	// the request; get a new token from the cluster administrator.
	ErrAgentEnrollmentRejected = err.ErrInvalidData.WithObjectCode(model.InfrastructureObjectCode).
					WithMessage("The agent rejected the enrollment token").
					WithDetailCode(14)

	// ErrAgentTenantChanged: a renewed certificate names another tenant than the agent was enrolled
	// as, which would change the issuer of its access tokens; it is refused.
	ErrAgentTenantChanged = err.ErrConflict.WithObjectCode(model.InfrastructureObjectCode).
				WithMessage("The renewed certificate names another tenant").
				WithDetailCode(15)

	// ErrAgentDeleteNeedsConfirm: the agent holds no running labs but future reservations would lose
	// capacity; the delete is repeated with confirm after the admin saw the preview.
	ErrAgentDeleteNeedsConfirm = err.ErrConflict.WithObjectCode(model.InfrastructureObjectCode).
					WithMessage("Deleting the agent affects future reservations: confirm the delete").
					WithDetailCode(16)
)
