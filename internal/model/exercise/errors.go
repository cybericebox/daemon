package exerciseModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`. Each var has exactly one non-test call site; the
// validation helpers below are structured so every rule fails in one place.
//
// ExerciseObjectCode — next free detail code: 74
var (
	// multi-site: covers all read paths plus the optimistic-lock re-read
	// discrimination (row gone) — one public "not found" fact shared across
	// catalog, useCase, and lifecycle flows.
	ErrExerciseNotFound = err.ErrObjectNotFound.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Exercise not found").WithDetailCode(1)
	// multi-site: covers rollback-source-missing and get-version missing-or-foreign;
	// foreign ownership is deliberately indistinguishable from absence (anti-IDOR).
	ErrExerciseVersionNotFound = err.ErrObjectNotFound.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Exercise version not found").WithDetailCode(2)

	ErrExerciseExists = err.ErrObjectExists.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Exercise with this name already exists").WithDetailCode(3)
	ErrTestDeployNotFound = err.ErrObjectNotFound.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Test deployment not found").WithDetailCode(37)
	// ErrTestDeployNoLab: a variant without devices is static, there is no laboratory to test.
	ErrTestDeployNoLab = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("The variant has no laboratory to test").WithDetailCode(58)
	// ErrTestDeployNotReady: the web session opens only once the test laboratory is ready.
	ErrTestDeployNotReady = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("The test laboratory is not ready yet").WithDetailCode(59)
	// ErrTestDeployActiveExists: one active test laboratory per user across all exercises.
	ErrTestDeployActiveExists = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("The limit of running test laboratories is reached").WithDetailCode(63)
	// ErrTestDeployNoWebDevice: the requested device port has no web address.
	ErrTestDeployNoWebDevice = err.ErrObjectNotFound.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("The device has no web address").WithDetailCode(60)

	// ErrExerciseModified: optimistic-lock guard on the identity UPDATE hit 0
	// rows while the row still exists. 409: the client reloads and retries.
	ErrExerciseModified = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Exercise was modified concurrently").WithDetailCode(4)
	// multi-site: covers publish/discard without a draft, the
	// publish-vs-concurrent-discard race, and checkpoint without a draft or
	// published version to snapshot — one public "no draft" fact.
	ErrNoDraft = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
			WithMessage("Exercise has no draft version").WithDetailCode(5)
	ErrDraftAlreadyExists = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Exercise already has a draft version").WithDetailCode(6)
	// ErrSecretsNotConfigured: a secret env-var value was submitted but the
	// platform has no EXERCISE_SECRETS_KEY configured.
	ErrSecretsNotConfigured = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Secret storage is not configured").WithDetailCode(7)
	// ErrExerciseArchived: the exercise is archived — hidden from the catalog
	// and frozen. Raised only by Exercise.EnsureNotArchived, which every
	// content/identity mutation and every event attach/replace calls.
	ErrExerciseArchived = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Exercise is archived").WithDetailCode(48)
	// ErrExerciseInUse: delete refused — at least one event (active or
	// archived) references a version of the exercise. Raised only by
	// EnsureNotInUse; the "events" context names them (server logs), clients
	// list them via GET :id/usage.
	ErrExerciseInUse = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Exercise is used by events").WithDetailCode(49)

	// ── identity validation ──
	ErrExerciseNameInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Exercise name must be 3-50 characters").WithDetailCode(8)
	ErrExerciseDescriptionTooLong = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Exercise description must be at most 2000 characters").WithDetailCode(9)
	ErrExerciseTagsInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Exercise tags must be 1-30 characters each, at most 20 tags").WithDetailCode(10)

	// ── version structural validation (call sites in version.go, Task 3) ──
	ErrVersionNoVariants = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Version must have at least one variant").WithDetailCode(11)
	ErrTaskCountMismatch = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("All variants must have the same number of tasks").WithDetailCode(12)
	ErrTaskIdentityMismatch = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Task identifiers must match at the same position in every variant").WithDetailCode(35)
	ErrTaskDifficultyMismatch = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Task difficulty must match at the same position in every variant").WithDetailCode(36)
	ErrTaskDescriptionRequired = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Task description is required before publication").WithDetailCode(38)
	ErrTaskNameInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Task name must be 3-50 characters").WithDetailCode(13)
	ErrTaskDifficultyInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Task difficulty is invalid").WithDetailCode(14)
	ErrFlagSourceInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Task flag must use ICE{...} without whitespace").WithDetailCode(15)
	ErrDeviceNameInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Container name must be a DNS label: lowercase a-z, 0-9 and '-', not starting or ending with '-'").WithDetailCode(16)
	// ErrDeviceNameTooLong: the name becomes part of the lab's web address
	// <device>-<code>.<domain>, which must be one 63-char DNS label.
	ErrDeviceNameTooLong = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Container name must be at most 35 characters").WithDetailCode(61)
	ErrDeviceDisplayNameInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Switch or hub display name must not be blank").WithDetailCode(46)
	ErrDeviceTypeInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Device type is invalid").WithDetailCode(17)
	ErrDeviceInterfaceInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Device interface is invalid").WithDetailCode(18)
	ErrDeviceResourcesInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Device resource quantity is invalid or request exceeds limit").WithDetailCode(39)
	ErrDeviceRouteInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Device static route requires a CIDR destination and same-family next-hop IP").WithDetailCode(40)
	ErrDeviceStaticAddressCountInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
						WithMessage("Static interface requires exactly one IP address").WithDetailCode(41)
	ErrDeviceAddressRefInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Static address reference is invalid or conflicts with another address").WithDetailCode(44)
	ErrNetworkDHCPInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("DHCP ranges or internet DNS address are invalid").WithDetailCode(47)
	ErrFlagEnvironmentConflict = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Flag target environment variable is already used by a device variable or another task").WithDetailCode(42)
	ErrDeviceSecurityPresetInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Device security preset is invalid").WithDetailCode(34)
	ErrDeviceExternalInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Device external access is invalid").WithDetailCode(27)
	ErrConnectionArityInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Connection must have exactly two endpoints").WithDetailCode(28)
	ErrConnectionEndpointsInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Connection must have exactly two valid endpoints").WithDetailCode(19)

	// ── publish-time graph validation (call sites in topology.go, Task 4) ──
	ErrEndpointUnresolved = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Connection endpoint references a missing device or interface").WithDetailCode(20)
	ErrPortInUse = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
			WithMessage("A port may be used by at most one connection").WithDetailCode(21)
	// ErrDevicePersistenceInvalid: state persistence is a property of a container device and its
	// debounce is a positive duration of at most a day (context reason: device_type or debounce).
	ErrDevicePersistenceInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Device state persistence is allowed only on container devices, with a debounce between 1s and 24h").WithDetailCode(64)
	ErrForwardingPortInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Switch or hub port must be GigabitEthernet0/1 through GigabitEthernet0/48").WithDetailCode(43)
	// Detail codes 22 and 23 (ErrSwitchLoop, ErrGatewaysBridged) are retired:
	// static L2 loop/broadcast-domain checks were dropped as meaningless
	// (unmanaged switches can be cabled to real hardware outside the declared
	// topology); the gap is not reusable.
	ErrFlagDeviceUnresolved = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Task flag references a device absent from the topology").WithDetailCode(24)

	// ── publish-time graph validation (new specifics, Task 4 review) ──
	ErrVPNDisabled = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
			WithMessage("VPN is not enabled in this variant's topology").WithDetailCode(29)
	ErrInternetDisabled = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Internet is not enabled in this variant's topology").WithDetailCode(30)
	ErrVPNGatewayInUse = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("VPN gateway is already connected").WithDetailCode(31)
	ErrInternetGatewayInUse = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Internet gateway is already connected").WithDetailCode(32)
	ErrDeviceNameDuplicate = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Device names must be unique within the topology").WithDetailCode(33)
	ErrDeviceAddressRefUnreachable = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Static address reference must be connected to its gateway network").WithDetailCode(45)

	// ── hints ──
	ErrHintsInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
			WithMessage("Task hints must match across variants: at most 10, level nudge|direction|steps|near_solution, text up to 20000 characters").WithDetailCode(50)
	ErrHintTextRequired = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Hint text is required before publication").WithDetailCode(51)

	// ── scope, access and proposals (W4) ──
	ErrExerciseInfrastructureNotAllowed = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
						WithMessage("The owner event does not allow exercises with infrastructure").WithDetailCode(52)
	ErrExerciseAccessInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Access level applies to catalog exercises and needs events for the selected level").WithDetailCode(53)
	ErrExerciseProposalNotFound = err.ErrObjectNotFound.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Exercise proposal not found").WithDetailCode(54)
	ErrExerciseProposalInvalid = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Only a published event exercise without a pending proposal can be proposed").WithDetailCode(55)
	ErrExerciseProposalDecided = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Exercise proposal was already decided").WithDetailCode(56)
	// category B — data-dependent authorization guard (event membership or
	// catalog access), always 403; the reason goes to server logs only.
	ErrExerciseForbidden = err.ErrForbidden.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Not allowed to access this exercise").WithDetailCode(57)

	// ── placeholder validation (call sites in placeholder.go, Task 5) ──
	ErrPlaceholderInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Description placeholder is malformed").WithDetailCode(25)
	ErrPlaceholderNode = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Description placeholder references a node absent from the topology").WithDetailCode(26)
	ErrPlaceholderLinkInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("IP placeholder link needs http or https, a port from 1 to 65535 and a path starting with / and no mask").WithDetailCode(62)

	// ── device resources (the platform frame, elevation requests) ──
	// ErrDeviceResourcePresetInvalid: a device names a preset the platform does not offer; the context names
	// the device and the preset. Publishing is refused.
	ErrDeviceResourcePresetInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Device resource preset is not offered by the platform").WithDetailCode(65)
	// ErrDevicesNeedElevation: publishing needs every device inside the platform frame or covered by an
	// approved elevation; the context lists the devices (variant, device, name, cpu, memory) that are not.
	ErrDevicesNeedElevation = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Devices above the platform frame need an approved resource elevation").WithDetailCode(66)
	// ErrDeviceResourcesAboveCeiling: a device asks for more than the elevation ceiling, which no approval can
	// give; the context lists the devices and the ceiling.
	ErrDeviceResourcesAboveCeiling = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Device resources are above the platform elevation ceiling").WithDetailCode(67)
	ErrElevationNotFound = err.ErrObjectNotFound.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Resource elevation request not found").WithDetailCode(68)
	// ErrElevationNotNeeded: every device of the working copy is inside the frame or already covered.
	ErrElevationNotNeeded = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("No device needs a resource elevation").WithDetailCode(69)
	// ErrElevationPending: an exercise has one open elevation request at a time.
	ErrElevationPending = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("The exercise already has a pending resource elevation request").WithDetailCode(70)
	ErrElevationDecided = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("The resource elevation request was already decided").WithDetailCode(71)
	// ErrElevationInvalid: approved values must name requested devices, be positive and stay within the ceiling.
	ErrElevationInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Approved values must be positive, for the requested devices and within the ceiling").WithDetailCode(72)
	ErrElevationReasonRequired = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("A resource elevation request needs a reason").WithDetailCode(73)
)
