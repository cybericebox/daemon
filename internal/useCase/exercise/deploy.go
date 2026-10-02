package exercise

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/gofrs/uuid"

	testDeployRepo "github.com/cybericebox/daemon/internal/delivery/repository/testDeployRepo"
	repositoryTools "github.com/cybericebox/daemon/internal/delivery/repository/tools"
	"github.com/cybericebox/daemon/internal/model"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	"github.com/cybericebox/daemon/internal/model/flagpattern"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	labAccessModel "github.com/cybericebox/daemon/internal/model/labAccess"
	labBindingModel "github.com/cybericebox/daemon/internal/model/labBinding"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	"github.com/cybericebox/daemon/pkg/labaccess"
)

// IInfrastructure is the optional deploy port. It is nil when no infrastructure
// agent is configured, in which case every deploy operation is refused with an
// explicit ErrInfrastructureUnavailable rather than silently doing nothing.
type IInfrastructure interface {
	DeployLab(ctx context.Context, group, lab string, meta infraModel.LabMeta, topo exerciseModel.Topology) error
	LabStatus(ctx context.Context, group, lab string) (exerciseModel.LabDeployStatus, error)
	DestroyLabGroup(ctx context.Context, group string) error
	// DeleteLab removes one Lab and keeps its group; a Lab that is already gone is fine.
	DeleteLab(ctx context.Context, group, lab string) error
	// DeleteLabClient removes one named VPN client of a group; one that is already gone is fine.
	DeleteLabClient(ctx context.Context, group, client string) error
	EnsureLabClient(ctx context.Context, group, client string) (string, error)
	ReconcileLabGroupAccess(ctx context.Context, group string, policies []labAccessModel.ClientPolicy) error
	// LabClientHandshake is the last WireGuard handshake of a client; zero when it never connected.
	LabClientHandshake(ctx context.Context, group, client string) (time.Time, error)
}

// ITestSessions signs the author's handoff link of the laboratory L7 proxy for a test
// deploy: the deploy's group and the author's client. It is nil when no proxy key
// is configured.
type ITestSessions interface {
	Issue(context.Context, labaccess.Session, time.Time) (labaccess.Link, error)
}

// IVPNStore persists a user's VPN config. It is nil when secret storage is not
// configured; the test deploy then just returns the config inline without saving.
type IVPNStore interface {
	StoreConfig(ctx context.Context, userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID, plaintext string) error
	GetConfig(ctx context.Context, userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID) (string, error)
	DeleteConfig(ctx context.Context, userID uuid.UUID, scope vpnModel.Scope, scopeRef uuid.NullUUID) error
}

// wgPrivateKeyPlaceholder is what the cluster writes where the client's private key
// goes; the agent swaps in the real key only in the answer to the client's creation.
// A config that still holds it cannot be used, so it is never stored or handed out.
const wgPrivateKeyPlaceholder = "__PRIVATE_KEY__"

// testVPNRef keys the author's VPN config: one per author (no scope ref), shared by every
// test lab of the author, because they all live in the author's one lab group and one client.
func testVPNRef() uuid.NullUUID { return uuid.NullUUID{} }

// testGroupName is the author's lab group: every test lab of a user is a Lab in it.
func testGroupName(userID uuid.UUID) string { return "tu-" + userID.String() }

// testLabName is one deploy's Lab within the author's group (a DNS-safe name, unique per deploy).
func testLabName(deployID uuid.UUID) string {
	hex := strings.ReplaceAll(deployID.String(), "-", "")
	return "l-" + hex[len(hex)-12:]
}

// keepAuthorConfig stores the author's complete VPN config, encrypted, once, with the group's client.
// It fails the deploy rather than leave the author with a config they cannot download.
func (u *ExerciseUseCase) keepAuthorConfig(ctx context.Context, ownerID uuid.UUID, config string) error {
	if config == "" {
		return nil
	}
	if strings.Contains(config, wgPrivateKeyPlaceholder) {
		return errors.New("the laboratory agent returned a VPN config without the private key")
	}
	if u.vpnStore == nil {
		return vpnModel.ErrVPNSecretsNotConfigured.Err()
	}
	return u.vpnStore.StoreConfig(ctx, ownerID, vpnModel.ScopeTest, testVPNRef(), config)
}

// dropAuthorConfig removes the author's stored VPN config; best effort, it belongs to the
// group that is gone.
func (u *ExerciseUseCase) dropAuthorConfig(ctx context.Context, ownerID uuid.UUID) {
	if u.vpnStore != nil {
		_ = u.vpnStore.DeleteConfig(ctx, ownerID, vpnModel.ScopeTest, testVPNRef())
	}
}

// testClientName is the access-policy client of a test deploy's author. It is
// the same "p-<user>" name event participants use: one LabGroupClient is the VPN
// peer and the proxy identity, and it is created with the deploy, not on demand.
func testClientName(userID uuid.UUID) string { return labBindingModel.ParticipantClientName(userID) }

// testFlagRandomBytes sizes a generated test flag like an event's default.
const testFlagRandomBytes = 20

// vpnConnectedWindow is how recent the author's last WireGuard handshake must be to count as connected.
const vpnConnectedWindow = 3 * time.Minute

// testLabOperationTimeout bounds one deploy or teardown, agent calls included.
const testLabOperationTimeout = 60 * time.Second

// The defaults of EXERCISE_TEST_DEPLOY_TTL and EXERCISE_TEST_DEPLOY_TTL_MAX, used while the config is unset.
const (
	defaultTestDeployTTL    = 2 * time.Hour
	defaultTestDeployTTLMax = 8 * time.Hour
)

// testDeployTTL is the lease of a test lab.
func (u *ExerciseUseCase) testDeployTTL() time.Duration {
	if u.flagConfig.TestDeployTTL > 0 {
		return u.flagConfig.TestDeployTTL
	}
	return defaultTestDeployTTL
}

// maxTestDeployTTL is the longest a test lab lives from its start, however often it is extended.
func (u *ExerciseUseCase) maxTestDeployTTL() time.Duration {
	if u.flagConfig.TestDeployTTLMax > 0 {
		return u.flagConfig.TestDeployTTLMax
	}
	return defaultTestDeployTTLMax
}

// DevicePersistenceAllowed says some agent offers device state persistence to this platform, so the
// editor offers the option. The agents report it; there is no setting for it here.
func (u *ExerciseUseCase) DevicePersistenceAllowed() bool {
	p, ok := u.infra.(interface{ PersistenceAvailable() bool })
	return ok && p.PersistenceAvailable()
}

// MaxActiveTestDeploys is how many test labs one user may run at once (at least one).
func (u *ExerciseUseCase) MaxActiveTestDeploys() int {
	return max(1, u.flagConfig.MaxActiveTestDeploys)
}

// InfrastructureAvailable reports whether an infrastructure agent is wired, so
// the admin UI can enable or disable the per-variant "Test" action up front.
func (u *ExerciseUseCase) InfrastructureAvailable() bool {
	if reporter, ok := u.infra.(infraModel.AvailabilityReporter); ok && u.infra != nil {
		return reporter.Available()
	}
	return u.infra != nil
}

// DeployVariantTest stands up a variant's topology for testing and returns a
// handle whose Group is the deploy id. The lab provisions asynchronously; the
// caller polls DeployTestStatus. Secret env values are decrypted here (never
// leaving the daemon in plaintext until this deploy path), so the agent receives
// resolved values for its write-only env Secret.
func (u *ExerciseUseCase) DeployVariantTest(ctx context.Context, ownerID, versionID, variantID uuid.UUID) (exerciseModel.DeployHandle, error) {
	if u.infra == nil {
		return exerciseModel.DeployHandle{}, infraModel.ErrInfrastructureUnavailable.Err()
	}

	// A user may run a limited number of test laboratories, across all exercises.
	active, err := u.ListTestDeploys(ctx, ownerID, uuid.Nil)
	if err != nil {
		return exerciseModel.DeployHandle{}, err
	}
	if len(active) >= u.MaxActiveTestDeploys() {
		return exerciseModel.DeployHandle{}, exerciseModel.ErrTestDeployActiveExists.Err()
	}

	variant, err := u.loadVariant(ctx, versionID, variantID)
	if err != nil {
		return exerciseModel.DeployHandle{}, err
	}
	if err := variant.ValidateTopologyForDeploy(); err != nil {
		return exerciseModel.DeployHandle{}, err
	}
	if len(variant.Topology.Devices) == 0 {
		return exerciseModel.DeployHandle{}, exerciseModel.ErrTestDeployNoLab.Err()
	}
	topo, err := u.decryptTopologySecrets(variant.ID, variant.Topology)
	if err != nil {
		return exerciseModel.DeployHandle{}, err
	}
	// Each flag-linked task gets one test value for this deploy, the same
	// policy as an event (none: random, one: fixed, several: a pick, template:
	// generated). The values stay on the server; the author checks a found flag with CheckTestFlag.
	links := variant.FlagLinks()
	for i := range links {
		flag, flagErr := flagpattern.Resolve(links[i].Candidates, testFlagRandomBytes, rand.Reader)
		if flagErr != nil {
			return exerciseModel.DeployHandle{}, model.ErrPlatform.WithError(flagErr).WithMessage("Failed to resolve test flag").Err()
		}
		links[i].Flag = flag
	}
	if topo, err = topo.WithFlagEnv(links); err != nil {
		return exerciseModel.DeployHandle{}, err
	}

	now := time.Now()
	deployFlags := make([]exerciseModel.DeployFlag, 0, len(links))
	for _, link := range links {
		deployFlags = append(deployFlags, exerciseModel.DeployFlag{TaskID: link.TaskID, Name: link.Name, Flag: link.Flag})
	}
	id := uuid.Must(uuid.NewV7())
	deploy := exerciseModel.TestDeploy{Flags: deployFlags, ID: id, GroupName: testGroupName(ownerID), LabName: testLabName(id), VersionID: versionID, VariantID: variantID, CreatedBy: ownerID, CreatedAt: now, ExpiresAt: now.Add(u.testDeployTTL())}

	// Everything an author does to their test labs is serialized, so the group is created with the
	// first lab, grows with the next ones and is deleted with the last one without racing.
	err = u.underOwnerLock(ctx, ownerID, func(ctx context.Context, repo *testDeployRepo.Repository) (bool, error) {
		existing, err := repo.ListOwned(ctx, ownerID)
		if err != nil {
			return false, model.ErrPlatform.WithError(err).WithMessage("Failed to reserve test deploy").Err()
		}
		if u.activeCount(existing, now) >= u.MaxActiveTestDeploys() {
			return false, exerciseModel.ErrTestDeployActiveExists.Err()
		}
		if _, err = repo.Create(ctx, deploy); err != nil {
			return false, model.ErrPlatform.WithError(err).WithMessage("Failed to reserve test deploy").Err()
		}
		first := len(existing) == 0
		if err = u.provisionTestLab(ctx, deploy, topo, existing, first); err != nil {
			// The group or the lab may be half created. A canceled request must not prevent the cleanup;
			// if it fails, the lease is kept so the periodic expiry pass can retry it.
			cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
			defer cancel()
			if cleanupErr := u.removeTestLab(cleanupCtx, deploy, existing, first); cleanupErr != nil {
				return true, model.ErrPlatform.WithError(errors.Join(err, cleanupErr)).WithMessage("Failed to deploy variant lab").Err()
			}
			_, _ = repo.DeleteOwned(cleanupCtx, deploy.ID, ownerID)
			return false, model.ErrPlatform.WithError(err).WithMessage("Failed to deploy variant lab").Err()
		}
		return true, nil
	})
	if err != nil {
		return exerciseModel.DeployHandle{}, err
	}

	handle := exerciseModel.DeployHandle{Group: deploy.ID.String(), Lab: deploy.LabName}
	handle.Flags = deployFlags
	if topo.VPN.Enabled {
		handle.VPNClient = "tester"
	}
	return handle, nil
}

// testLabMeta labels a test lab and its group; test labs are independent for the scheduler.
func testLabMeta(deploy exerciseModel.TestDeploy) infraModel.LabMeta {
	return infraModel.LabMeta{
		Labels: map[string]string{
			infraModel.LabelKind:    infraModel.KindTest,
			infraModel.LabelVersion: deploy.VersionID.String(),
		},
		GroupLabels: map[string]string{infraModel.LabelKind: infraModel.KindTest},
	}
}

// activeCount is how many of the author's test labs still have a running lease.
func (u *ExerciseUseCase) activeCount(items []exerciseModel.TestDeploy, now time.Time) int {
	n := 0
	for _, item := range items {
		if item.ExpiresAt.IsZero() || item.ExpiresAt.After(now) {
			n++
		}
	}
	return n
}

// labNames lists the Lab names of deploys, plus extra.
func labNames(items []exerciseModel.TestDeploy, skip uuid.UUID, extra ...string) []string {
	names := make([]string, 0, len(items)+len(extra))
	for _, item := range items {
		if item.ID != skip {
			names = append(names, item.LabName)
		}
	}
	return append(names, extra...)
}

// provisionTestLab stands one Lab up in the author's group. The group policy is default-deny for
// the VPN and, in per-user mode, for the proxy, and it is replaced as a whole, so it lists every
// Lab of the author. The group's client and its config are made once, with the first Lab; later
// Labs reuse them.
func (u *ExerciseUseCase) provisionTestLab(ctx context.Context, deploy exerciseModel.TestDeploy, topo exerciseModel.Topology, existing []exerciseModel.TestDeploy, first bool) error {
	client := testClientName(deploy.CreatedBy)
	// The author's group has one user (the author's client); its gateway is for the labs that use the internet.
	plan := infraModel.GroupPlan{MaxUsers: 1}
	if topo.Internet.Enabled {
		plan.InternetLabs = 1
	}
	ctx = infraModel.WithPlacementNeed(ctx, infraModel.PlacementNeed{Plan: plan})
	if err := u.infra.DeployLab(ctx, deploy.GroupName, deploy.LabName, testLabMeta(deploy), topo); err != nil {
		return err
	}
	if err := u.infra.ReconcileLabGroupAccess(ctx, deploy.GroupName, []labAccessModel.ClientPolicy{{Name: client, AllowedLabs: labNames(existing, uuid.Nil, deploy.LabName)}}); err != nil {
		return err
	}
	if !first {
		return nil
	}
	// A client left over from a group that was never cleaned cannot give its config again.
	if err := u.infra.DeleteLabClient(ctx, deploy.GroupName, client); err != nil {
		return err
	}
	// The agent returns the complete VPN config once, at creation, so it is kept encrypted now.
	config, err := u.infra.EnsureLabClient(ctx, deploy.GroupName, client)
	if err != nil {
		return err
	}
	return u.keepAuthorConfig(ctx, deploy.CreatedBy, config)
}

// removeTestLab takes one Lab down: the whole group with its VPN config when it is the author's
// last Lab, otherwise only the Lab, and the group policy loses it.
func (u *ExerciseUseCase) removeTestLab(ctx context.Context, deploy exerciseModel.TestDeploy, all []exerciseModel.TestDeploy, last bool) error {
	if last {
		if err := u.infra.DestroyLabGroup(ctx, deploy.GroupName); err != nil {
			return err
		}
		u.dropAuthorConfig(ctx, deploy.CreatedBy)
		return nil
	}
	if err := u.infra.DeleteLab(ctx, deploy.GroupName, deploy.LabName); err != nil {
		return err
	}
	return u.infra.ReconcileLabGroupAccess(ctx, deploy.GroupName, []labAccessModel.ClientPolicy{{Name: testClientName(deploy.CreatedBy), AllowedLabs: labNames(all, deploy.ID)}})
}

// underOwnerLock runs fn with the author's lock held until it returns: with a unit of work it is the
// per-user advisory lock of one transaction, so a deploy, a destroy and an expiry of one author
// never overlap. The whole run, the laboratory agent's calls included, is bounded by
// testLabOperationTimeout. fn says whether to commit even when it fails (a lease that is kept so the expiry
// pass can retry a cleanup that failed).
func (u *ExerciseUseCase) underOwnerLock(ctx context.Context, owner uuid.UUID, fn func(context.Context, *testDeployRepo.Repository) (bool, error)) error {
	ctx, cancel := context.WithTimeout(ctx, testLabOperationTimeout)
	defer cancel()
	if u.deployUoW == nil {
		_, err := fn(ctx, u.testDeploys)
		return err
	}
	txCtx, queries, tx, err := u.deployUoW.UnitOfWork(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Restore() }()
	repo := testDeployRepo.New(queries)
	if err = repo.LockOwner(txCtx, owner); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to lock the test laboratories").Err()
	}
	commit, fnErr := fn(txCtx, repo)
	if fnErr != nil && !commit {
		return fnErr
	}
	if err = tx.Save(); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to save the test laboratories").Err()
	}
	return fnErr
}

// DeployTestStatus returns the current runtime status of a test deploy, keyed by
// the group id from DeployVariantTest. Once the lab is up and a tester VPN config
// exists, it is persisted for the caller under the test scope (best-effort: the
// config is also returned inline, so a store hiccup must not break polling).
func (u *ExerciseUseCase) DeployTestStatus(ctx context.Context, userID, deployID uuid.UUID) (exerciseModel.LabDeployStatus, error) {
	if u.infra == nil {
		return exerciseModel.LabDeployStatus{}, infraModel.ErrInfrastructureUnavailable.Err()
	}
	deploy, err := u.testDeploys.GetOwned(ctx, deployID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseModel.LabDeployStatus{}, exerciseModel.ErrTestDeployNotFound.Err()
		}
		return exerciseModel.LabDeployStatus{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load test deploy").Err()
	}
	if !deploy.ExpiresAt.IsZero() && !deploy.ExpiresAt.After(time.Now()) {
		return exerciseModel.LabDeployStatus{}, exerciseModel.ErrTestDeployNotFound.Err()
	}
	status, err := u.infra.LabStatus(ctx, deploy.GroupName, deploy.LabName)
	if err != nil {
		return exerciseModel.LabDeployStatus{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read deploy status").Err()
	}
	// The agent's own view of the shared tester client carries the key placeholder and
	// no access; only the author's config, stored with this deploy, is ever handed out.
	status.VPNConfig = ""
	if status.Ready && u.vpnStore != nil {
		config, cfgErr := u.vpnStore.GetConfig(ctx, userID, vpnModel.ScopeTest, testVPNRef())
		if cfgErr != nil {
			return exerciseModel.LabDeployStatus{}, model.ErrPlatform.WithError(cfgErr).WithMessage("Failed to read test VPN config").Err()
		}
		if !strings.Contains(config, wgPrivateKeyPlaceholder) {
			status.VPNConfig = config
		}
	}
	status.SolvedTasks = deploy.Solved
	// The handshake is a hint for the author; a stats hiccup must not break polling.
	if handshake, hsErr := u.infra.LabClientHandshake(ctx, deploy.GroupName, testClientName(userID)); hsErr == nil && !handshake.IsZero() {
		status.VPNLastHandshake = handshake
		status.VPNConnected = time.Since(handshake) <= vpnConnectedWindow
	}
	return status, nil
}

// CheckTestFlag tells the author of a test deploy whether a flag they found
// matches the value injected for one task. The expected values never leave the
// server; the compare is exact, case-sensitive and constant-time.
func (u *ExerciseUseCase) CheckTestFlag(ctx context.Context, userID, deployID, taskID uuid.UUID, flag string) (bool, error) {
	deploy, err := u.testDeploys.GetOwned(ctx, deployID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return false, exerciseModel.ErrTestDeployNotFound.Err()
		}
		return false, model.ErrPlatform.WithError(err).WithMessage("Failed to load test deploy").Err()
	}
	if !deploy.ExpiresAt.IsZero() && !deploy.ExpiresAt.After(time.Now()) {
		return false, exerciseModel.ErrTestDeployNotFound.Err()
	}
	for _, f := range deploy.Flags {
		if f.TaskID != taskID {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(f.Flag), []byte(flag)) != 1 {
			return false, nil
		}
		// A correct answer is remembered with the deploy, so the solved state survives a reload.
		if !slices.Contains(deploy.Solved, taskID) {
			if _, err = u.testDeploys.MarkSolved(ctx, deployID, userID, taskID); err != nil {
				return false, model.ErrPlatform.WithError(err).WithMessage("Failed to record the solved task").Err()
			}
		}
		return true, nil
	}
	return false, nil
}

// OpenTestDeployLink returns the author's link that opens one web device of a
// ready test deploy. It names the deploy's lab group and the author's client only
// (the group policy still decides which labs it reaches), and the session it
// grants lasts until the deploy's lease ends. Test traffic is never counted for
// an event. The link is short-lived and fetched fresh on every click; the
// platform sets no cookie on the lab domain.
func (u *ExerciseUseCase) OpenTestDeployLink(ctx context.Context, userID, deployID uuid.UUID, device string, port int32) (labaccess.Link, error) {
	if u.infra == nil || u.sessions == nil {
		return labaccess.Link{}, infraModel.ErrInfrastructureUnavailable.Err()
	}
	deploy, err := u.testDeploys.GetOwned(ctx, deployID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return labaccess.Link{}, exerciseModel.ErrTestDeployNotFound.Err()
		}
		return labaccess.Link{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load test deploy").Err()
	}
	now := time.Now()
	if !deploy.ExpiresAt.After(now) {
		return labaccess.Link{}, exerciseModel.ErrTestDeployNotFound.Err()
	}
	status, err := u.infra.LabStatus(ctx, deploy.GroupName, deploy.LabName)
	if err != nil {
		return labaccess.Link{}, model.ErrPlatform.WithError(err).WithMessage("Failed to read deploy status").Err()
	}
	if !status.Ready {
		return labaccess.Link{}, exerciseModel.ErrTestDeployNotReady.Err()
	}
	accessURL, ok := status.WebURL(device, port)
	if !ok {
		return labaccess.Link{}, exerciseModel.ErrTestDeployNoWebDevice.Err()
	}
	link, err := u.sessions.Issue(ctx, labaccess.Session{Group: deploy.GroupName, Client: testClientName(userID), AccessURL: accessURL, ExpiresAt: deploy.ExpiresAt}, now)
	if err != nil {
		return labaccess.Link{}, model.ErrPlatform.WithError(err).WithMessage("Failed to issue the test laboratory web link").Err()
	}
	return link, nil
}

// DestroyDeployTest tears a test deploy down: its Lab, and the author's whole group with the VPN
// config when it was their last Lab.
func (u *ExerciseUseCase) DestroyDeployTest(ctx context.Context, userID, deployID uuid.UUID) error {
	if u.infra == nil {
		return infraModel.ErrInfrastructureUnavailable.Err()
	}
	return u.underOwnerLock(ctx, userID, func(ctx context.Context, repo *testDeployRepo.Repository) (bool, error) {
		deploy, err := repo.GetOwned(ctx, deployID, userID)
		if err != nil {
			if repositoryTools.IsObjectNotFoundError(err) {
				return false, exerciseModel.ErrTestDeployNotFound.Err()
			}
			return false, model.ErrPlatform.WithError(err).WithMessage("Failed to load test deploy").Err()
		}
		return false, u.endTestLab(ctx, repo, deploy)
	})
}

// endTestLab removes one Lab of an author (the lock is held) and its row.
func (u *ExerciseUseCase) endTestLab(ctx context.Context, repo *testDeployRepo.Repository, deploy exerciseModel.TestDeploy) error {
	all, err := repo.ListOwned(ctx, deploy.CreatedBy)
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list test deployments").Err()
	}
	if err = u.removeTestLab(ctx, deploy, all, len(all) <= 1); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to destroy deploy").Err()
	}
	if _, err = repo.DeleteOwned(ctx, deploy.ID, deploy.CreatedBy); err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to remove test deploy").Err()
	}
	return nil
}

// CleanupExpiredTestDeploys removes the Labs whose persisted lease expired (and, with the last
// Lab of an author, the group). A failed agent deletion retains its row, so the next periodic
// pass retries it.
func (u *ExerciseUseCase) CleanupExpiredTestDeploys(ctx context.Context) error {
	if u.infra == nil {
		return infraModel.ErrInfrastructureUnavailable.Err()
	}
	items, err := u.testDeploys.ListExpired(ctx, time.Now())
	if err != nil {
		return model.ErrPlatform.WithError(err).WithMessage("Failed to list expired test deployments").Err()
	}
	var failed []error
	for _, item := range items {
		err := u.underOwnerLock(ctx, item.CreatedBy, func(ctx context.Context, repo *testDeployRepo.Repository) (bool, error) {
			deploy, err := repo.GetOwned(ctx, item.ID, item.CreatedBy)
			if err != nil {
				if repositoryTools.IsObjectNotFoundError(err) {
					return false, nil // ended by its author meanwhile
				}
				return false, err
			}
			return false, u.endTestLab(ctx, repo, deploy)
		})
		if err != nil {
			failed = append(failed, err)
		}
	}
	if len(failed) > 0 {
		return model.ErrPlatform.WithError(errors.Join(failed...)).WithMessage("Failed to remove expired test deploys").Err()
	}
	return nil
}

// ListTestDeploys returns the owner's active deploys; a non-nil exerciseID
// keeps those of that exercise's versions only.
func (u *ExerciseUseCase) ListTestDeploys(ctx context.Context, ownerID, exerciseID uuid.UUID) ([]exerciseModel.TestDeploy, error) {
	var items []exerciseModel.TestDeploy
	var err error
	if exerciseID != uuid.Nil {
		items, err = u.testDeploys.ListOwnedForExercise(ctx, ownerID, exerciseID)
	} else {
		items, err = u.testDeploys.ListOwned(ctx, ownerID)
	}
	if err != nil {
		return nil, model.ErrPlatform.WithError(err).WithMessage("Failed to list test deployments").Err()
	}
	now := time.Now()
	active := make([]exerciseModel.TestDeploy, 0, len(items))
	for _, item := range items {
		if item.ExpiresAt.IsZero() || item.ExpiresAt.After(now) {
			if version, verr := u.exercises.GetVersion(ctx, item.VersionID); verr == nil {
				item.ExerciseID = version.ExerciseID
			}
			active = append(active, item)
		}
	}
	return active, nil
}

func (u *ExerciseUseCase) ExtendTestDeploy(ctx context.Context, ownerID, deployID uuid.UUID) (exerciseModel.TestDeploy, error) {
	current, err := u.testDeploys.GetOwned(ctx, deployID, ownerID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseModel.TestDeploy{}, exerciseModel.ErrTestDeployNotFound.Err()
		}
		return exerciseModel.TestDeploy{}, model.ErrPlatform.WithError(err).WithMessage("Failed to load test deploy").Err()
	}
	now := time.Now()
	if !current.ExpiresAt.IsZero() && !current.ExpiresAt.After(now) {
		return exerciseModel.TestDeploy{}, exerciseModel.ErrTestDeployNotFound.Err()
	}
	expiresAt := now.Add(u.testDeployTTL())
	if !current.CreatedAt.IsZero() {
		if absoluteLimit := current.CreatedAt.Add(u.maxTestDeployTTL()); expiresAt.After(absoluteLimit) {
			expiresAt = absoluteLimit
		}
	}
	value, err := u.testDeploys.ExtendOwned(ctx, deployID, ownerID, expiresAt)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseModel.TestDeploy{}, exerciseModel.ErrTestDeployNotFound.Err()
		}
		return exerciseModel.TestDeploy{}, model.ErrPlatform.WithError(err).WithMessage("Failed to extend test deploy").Err()
	}
	return value, nil
}

// ResolveDeployedTopology returns one pinned version's variant topology by its
// stable event-runtime index. It is deliberately owned by the catalog use case:
// encrypted environment values are resolved here and never exposed through an
// event repository or a participant-facing DTO.
func (u *ExerciseUseCase) ResolveDeployedTopology(ctx context.Context, versionID uuid.UUID, variantIndex int32) (exerciseModel.Topology, error) {
	version, err := u.exercises.GetVersion(ctx, versionID)
	if err != nil {
		return exerciseModel.Topology{}, exerciseModel.ErrExerciseVersionNotFound.Err()
	}
	for _, variant := range version.Variants {
		if variant.Index == variantIndex {
			return u.decryptTopologySecrets(variant.ID, variant.Topology)
		}
	}
	return exerciseModel.Topology{}, exerciseModel.ErrExerciseVersionNotFound.Err()
}

// ResolveDeployedFlagLinks lists the flag-linked tasks of one pinned variant, so
// the event stand engine can inject each team's stored flag into the device.
// Only names and ids are returned; no flag value leaves the catalog here.
func (u *ExerciseUseCase) ResolveDeployedFlagLinks(ctx context.Context, versionID uuid.UUID, variantIndex int32) ([]exerciseModel.FlagLink, error) {
	version, err := u.exercises.GetVersion(ctx, versionID)
	if err != nil {
		return nil, exerciseModel.ErrExerciseVersionNotFound.Err()
	}
	for _, variant := range version.Variants {
		if variant.Index == variantIndex {
			links := variant.FlagLinks()
			for i := range links {
				links[i].Candidates = nil
			}
			return links, nil
		}
	}
	return nil, exerciseModel.ErrExerciseVersionNotFound.Err()
}

// ResolveVersionTopologies exposes the non-secret topology shape for every
// variant of a pinned version. Event preflight uses it before a lab exists, so
// it must consider every possible assignment without decrypting environment
// values or exposing them outside the catalog use case.
func (u *ExerciseUseCase) ResolveVersionTopologies(ctx context.Context, versionID uuid.UUID) ([]exerciseModel.Topology, error) {
	version, err := u.exercises.GetVersion(ctx, versionID)
	if err != nil {
		return nil, exerciseModel.ErrExerciseVersionNotFound.Err()
	}
	out := make([]exerciseModel.Topology, 0, len(version.Variants))
	for _, variant := range version.Variants {
		topology := variant.Topology
		topology.Devices = append([]exerciseModel.Device(nil), topology.Devices...)
		for index := range topology.Devices {
			topology.Devices[index].EnvVars = nil
		}
		out = append(out, topology)
	}
	return out, nil
}

// loadVariant fetches one variant of a version by id.
func (u *ExerciseUseCase) loadVariant(ctx context.Context, versionID, variantID uuid.UUID) (exerciseModel.Variant, error) {
	version, err := u.exercises.GetVersion(ctx, versionID)
	if err != nil {
		return exerciseModel.Variant{}, exerciseModel.ErrExerciseVersionNotFound.Err()
	}
	for _, v := range version.Variants {
		if v.ID == variantID {
			return v, nil
		}
	}
	return exerciseModel.Variant{}, exerciseModel.ErrExerciseVersionNotFound.Err()
}

// decryptTopologySecrets returns a copy of the topology with secret env-var
// values decrypted to plaintext for deployment. The stored variant (ciphertext)
// is never mutated — devices and their env slices are copied before rewriting.
func (u *ExerciseUseCase) decryptTopologySecrets(variantID uuid.UUID, topo exerciseModel.Topology) (exerciseModel.Topology, error) {
	out := topo
	out.Devices = make([]exerciseModel.Device, len(topo.Devices))
	copy(out.Devices, topo.Devices)
	for di := range out.Devices {
		d := &out.Devices[di]
		if len(d.EnvVars) == 0 {
			continue
		}
		envs := make([]exerciseModel.EnvVar, len(d.EnvVars))
		copy(envs, d.EnvVars)
		for ei := range envs {
			if !envs[ei].Secret || envs[ei].Value == "" {
				continue
			}
			if u.cipher == nil {
				return exerciseModel.Topology{}, exerciseModel.ErrSecretsNotConfigured.Err()
			}
			pt, err := u.cipher.DecryptWithContext(envs[ei].Value, envSecretContext(variantID, d.Name, envs[ei].Name))
			if err != nil {
				return exerciseModel.Topology{}, model.ErrPlatform.WithError(err).WithMessage("Failed to decrypt secret env var").Err()
			}
			envs[ei].Value = string(pt)
		}
		d.EnvVars = envs
	}
	return out, nil
}

// ResetTestDeployDevice discards the snapshots of one device of the author's own test lab and
// restarts it from its base image.
func (u *ExerciseUseCase) ResetTestDeployDevice(ctx context.Context, userID, deployID uuid.UUID, device string) error {
	deploy, controller, err := u.testDeviceTarget(ctx, userID, deployID)
	if err != nil {
		return err
	}
	return controller.ResetDevice(ctx, deploy.GroupName, deploy.LabName, device)
}

// RescueTestDeployDevice starts one device of the author's own test lab from its latest snapshot
// with a shell (enable) or back to its normal start.
func (u *ExerciseUseCase) RescueTestDeployDevice(ctx context.Context, userID, deployID uuid.UUID, device string, enable bool) error {
	deploy, controller, err := u.testDeviceTarget(ctx, userID, deployID)
	if err != nil {
		return err
	}
	return controller.RescueDevice(ctx, deploy.GroupName, deploy.LabName, device, enable)
}

// testDeviceTarget finds a live test lab the user owns and the agent's device capability.
func (u *ExerciseUseCase) testDeviceTarget(ctx context.Context, userID, deployID uuid.UUID) (exerciseModel.TestDeploy, infraModel.DeviceController, error) {
	if u.infra == nil {
		return exerciseModel.TestDeploy{}, nil, infraModel.ErrInfrastructureUnavailable.Err()
	}
	deploy, err := u.testDeploys.GetOwned(ctx, deployID, userID)
	if err != nil {
		if repositoryTools.IsObjectNotFoundError(err) {
			return exerciseModel.TestDeploy{}, nil, exerciseModel.ErrTestDeployNotFound.Err()
		}
		return exerciseModel.TestDeploy{}, nil, model.ErrPlatform.WithError(err).WithMessage("Failed to load test deploy").Err()
	}
	if !deploy.ExpiresAt.IsZero() && !deploy.ExpiresAt.After(time.Now()) {
		return exerciseModel.TestDeploy{}, nil, exerciseModel.ErrTestDeployNotFound.Err()
	}
	controller, ok := u.infra.(infraModel.DeviceController)
	if !ok {
		return exerciseModel.TestDeploy{}, nil, infraModel.ErrInfrastructureUnavailable.Err()
	}
	return deploy, controller, nil
}
