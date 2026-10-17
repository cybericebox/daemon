package exerciseModel

import (
	"github.com/cybericebox/daemon/pkg/err"

	"github.com/cybericebox/daemon/internal/model"
)

// Error-code convention: see internal/model/auth/errors.go. Enforced by
// `make lint-errors`. Each var has exactly one non-test call site; the
// validation helpers below are structured so every rule fails in one place.
//
// ExerciseObjectCode — next free detail code: 95
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
	// ErrTestDeployGroupBusy: the author's previous lab group is still being deleted and did not
	// finish within the wait. Transient: the author starts the lab again in a moment.
	ErrTestDeployGroupBusy = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("The previous test laboratory is still being removed, try again in a minute").WithDetailCode(75)
	// ErrTestDeployStatic: a static test has no laboratory, so lab-only operations refuse it.
	ErrTestDeployStatic = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("A static test has no laboratory").WithDetailCode(77)
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
	ErrFlagMultiNeedsDevice = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Several flags or a flag template are only available for tasks with a device").WithDetailCode(76)
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
	// ErrElevationInvalid: approved blocks must name requested devices, be sizes the platform offers and be no
	// larger than requested.
	ErrElevationInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Approved blocks must be sizes the platform offers, for the requested devices and no larger than requested").WithDetailCode(72)
	ErrElevationReasonRequired = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("A resource elevation request needs a reason").WithDetailCode(73)
	// ErrDeviceCustomResources: a device size is a preset (a whole number of blocks); custom values are not accepted.
	ErrDeviceCustomResources = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("A device size is a preset: custom resources are not accepted").WithDetailCode(74)
	// ErrVariantLinkInvalid: a linked variant must point to another, unlinked variant of the same version;
	// the context names the 1-based position of the offending variant.
	ErrVariantLinkInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("A linked variant must point to an unlinked variant of the same version").WithDetailCode(78)
	// ErrLinkedFlagsNotDistinct: a base and its linked variants share a fixed flag value for one task.
	ErrLinkedFlagsNotDistinct = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Linked variants of one base need different flags for the same task").WithDetailCode(79)
	// ErrArchiveInvalid: the exercise archive document breaks the schema. One public fact: the
	// `reason` context says what and where (a key, a 1-based path), so the author can fix the file.
	ErrArchiveInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Exercise archive is invalid").WithDetailCode(80)
	// ErrArchiveImageFileUnsupported: a device image file (the {file: ...} form) reached the synchronous import, which
	// does not push images (archives naming image files import in the background); the context names the device.
	ErrArchiveImageFileUnsupported = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Device images inside the archive cannot be imported this way").WithDetailCode(88)
	// ErrArchiveSchemaUnsupported: the document does not declare schema cib-exercise/1; the context
	// `expected` names the schema.
	ErrArchiveSchemaUnsupported = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Unsupported archive schema: expected schema: cib-exercise/1").WithDetailCode(90)
	// ErrArchiveTooLarge: an archive (imported or being exported) is over the archive limits: the zip, one file
	// or all files unpacked, the number of entries, or how far a file expands; the context `reason` says which.
	ErrArchiveTooLarge = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("The archive is over the size limits").WithDetailCode(91)
	// ErrArchiveBothDocuments: a task folder holds exercise.json and exercise.yaml at once.
	ErrArchiveBothDocuments = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("A task folder holds both exercise.json and exercise.yaml").WithDetailCode(81)
	// ErrArchiveFileUnlinked: a file in the archive that no task folder holds, or that nothing in its task
	// folder's exercise.* links to; the context `file` names it.
	ErrArchiveFileUnlinked = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("The archive holds a file nothing links to").WithDetailCode(82)
	// ErrArchivePathInvalid: a symlink entry, an unsafe zip path, or a link that leaves its task folder or names
	// exercise.* / manifest.json; the context `file` names the path and `reason` says why.
	ErrArchivePathInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("An archive path is not allowed").WithDetailCode(85)
	// ErrArchiveFileMissing: a link in exercise.* names a file its task folder does not hold; the context `file`
	// names the link.
	ErrArchiveFileMissing = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("A link names a file the archive does not hold").WithDetailCode(89)
	// ErrArchiveLinkedVariantField: a linked variant of the archive carries a key it does not own, or tasks that
	// do not match its base (another count, another task id); the context `field` is the 1-based path.
	ErrArchiveLinkedVariantField = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("A linked variant may carry only its note and, per task, the flag and attachments").WithDetailCode(86)
	// ErrArchiveLinkedToInvalid: linked_to names no variant of the version, the variant itself or a linked
	// variant; the context `variant` is the 1-based path of the linked variant and `linked_to` the value.
	ErrArchiveLinkedToInvalid = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("linked_to must name an unlinked variant of the same version").WithDetailCode(87)
	// ErrArchiveDescriptionKeyUnknown: a {{key}} in a plain-text task description names no placeholder of
	// that task; the context `key` is the name and `task` the task.
	ErrArchiveDescriptionKeyUnknown = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Task description names an unknown placeholder").WithDetailCode(84)
	// ErrArchiveChecksumMismatch: a file differs from manifest.json; the context `file` names it. Editing an
	// archive by hand breaks the checksums, so the fix is to delete manifest.json.
	ErrArchiveChecksumMismatch = err.ErrInvalidData.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("Archive checksum mismatch: delete manifest.json after editing an archive by hand").WithDetailCode(83)
	// ErrImportJobNotFound — category A (anti-enumeration): an absent import job and another user's are one
	// indistinguishable "not found".
	ErrImportJobNotFound = err.ErrObjectNotFound.WithObjectCode(model.ExerciseObjectCode).
				WithMessage("Import not found").WithDetailCode(92)
	// ErrArchiveUploadNotReady: the upload named for an import is not a task archive upload, or not all its chunks
	// arrived yet.
	ErrArchiveUploadNotReady = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("The archive upload is not complete").WithDetailCode(93)
	// ErrArchiveImportInterrupted: a background import stopped before it finished (the server restarted or the job ran
	// out of time); nothing was created, start it again.
	ErrArchiveImportInterrupted = err.ErrConflict.WithObjectCode(model.ExerciseObjectCode).
					WithMessage("The import stopped before it finished: start it again").WithDetailCode(94)
)
