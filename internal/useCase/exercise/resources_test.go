package exercise

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	inboxUseCase "github.com/cybericebox/daemon/internal/useCase/notification/inbox"
)

const (
	mi = 1 << 20
	gi = 1 << 30
)

// fakeElevations is the in-memory store of elevation requests.
type fakeElevations struct {
	items []resourcesModel.Elevation
}

func (f *fakeElevations) CreateElevation(_ context.Context, e resourcesModel.Elevation) error {
	for _, it := range f.items {
		if it.ExerciseID == e.ExerciseID && it.Status == resourcesModel.ElevationPending {
			return &pgconn.PgError{Code: "23505"}
		}
	}
	f.items = append(f.items, e)
	return nil
}

func (f *fakeElevations) GetElevation(_ context.Context, id uuid.UUID) (resourcesModel.Elevation, error) {
	for _, it := range f.items {
		if it.ID == id {
			return it, nil
		}
	}
	return resourcesModel.Elevation{}, pgx.ErrNoRows
}

func (f *fakeElevations) DecideElevation(_ context.Context, e resourcesModel.Elevation) (int64, error) {
	for i, it := range f.items {
		if it.ID == e.ID && it.Status == resourcesModel.ElevationPending {
			f.items[i] = e
			return 1, nil
		}
	}
	return 0, nil
}

func (f *fakeElevations) ListElevations(_ context.Context, status *resourcesModel.ElevationStatus, exerciseID uuid.NullUUID) ([]resourcesModel.ElevationListed, error) {
	var out []resourcesModel.ElevationListed
	for _, it := range f.items {
		if (status == nil || *status == it.Status) && (!exerciseID.Valid || exerciseID.UUID == it.ExerciseID) {
			out = append(out, resourcesModel.ElevationListed{Elevation: it, ExerciseName: "Web"})
		}
	}
	return out, nil
}

func (f *fakeElevations) LatestElevation(_ context.Context, exerciseID uuid.UUID) (resourcesModel.Elevation, error) {
	for i := len(f.items) - 1; i >= 0; i-- {
		if f.items[i].ExerciseID == exerciseID {
			return f.items[i], nil
		}
	}
	return resourcesModel.Elevation{}, pgx.ErrNoRows
}

func (f *fakeElevations) ApprovedFor(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]resourcesModel.Approval, error) {
	out := map[uuid.UUID][]resourcesModel.Approval{}
	for _, it := range f.items {
		if it.Status != resourcesModel.ElevationApproved {
			continue
		}
		for _, id := range ids {
			if id == it.ExerciseID {
				out[id] = append(out[id], it.Approved...)
			}
		}
	}
	return out, nil
}

type fakeElevationInbox struct {
	requested []inboxUseCase.Elevation
	decided   []inboxUseCase.Elevation
	approved  []bool
}

func (f *fakeElevationInbox) ElevationRequested(_ context.Context, e inboxUseCase.Elevation) error {
	f.requested = append(f.requested, e)
	return nil
}

func (f *fakeElevationInbox) ElevationDecided(_ context.Context, e inboxUseCase.Elevation, approved bool, _ uuid.UUID) error {
	f.decided, f.approved = append(f.decided, e), append(f.approved, approved)
	return nil
}

func deviceOf(name, preset string) exerciseModel.Device {
	return exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: name, Type: exerciseModel.DeviceTypeContainer, Image: "img", ResourcePreset: preset}
}

// blocksOf is the amount of a whole number of blocks, as the default policy gives it.
func blocksOf(n int) resourcesModel.Amount { return resourcesModel.DefaultPolicy().Amount(n) }

func variantOf(devices ...exerciseModel.Device) exerciseModel.Variant {
	return exerciseModel.Variant{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Devices: devices}}
}

func TestPublishGateNeedsFrameOrApprovedElevation(t *testing.T) {
	ctx := context.Background()
	store := &fakeElevations{}
	u := &ExerciseUseCase{elevations: store}
	exerciseID := uuid.Must(uuid.NewV7())
	small := deviceOf("web", "small")
	heavy := deviceOf("db", "xlarge") // 32 blocks: 500m / 2Gi

	// Inside the frame: no agent, no approval needed.
	require.NoError(t, u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(small)}))

	// Above the frame without an approval: a clear error naming the device.
	err := u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(small, heavy)})
	require.True(t, errors.Is(err, exerciseModel.ErrDevicesNeedElevation.Err()), "%v", err)

	// An approved elevation covers exactly the approved block.
	store.items = []resourcesModel.Elevation{{ID: uuid.Must(uuid.NewV7()), ExerciseID: exerciseID, Status: resourcesModel.ElevationApproved,
		Approved: []resourcesModel.Approval{{DeviceID: heavy.ID, Name: "db", Amount: blocksOf(32)}}}}
	require.NoError(t, u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(small, heavy)}))

	// Lowering the block keeps the approval ...
	lower := heavy
	lower.ResourcePreset = "large"
	require.NoError(t, u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(lower)}))
	// ... asking for a larger block needs a new approval.
	raised := heavy
	raised.ResourcePreset = "huge"
	err = u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(raised)})
	require.True(t, errors.Is(err, exerciseModel.ErrDevicesNeedElevation.Err()), "%v", err)
}

func TestPublishGateRefusesUnknownPresets(t *testing.T) {
	ctx := context.Background()
	u := &ExerciseUseCase{elevations: &fakeElevations{}}
	exerciseID := uuid.Must(uuid.NewV7())
	// The ceiling block itself needs an approval but is allowed; no block is above it.
	err := u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(deviceOf("huge", "huge"))})
	require.True(t, errors.Is(err, exerciseModel.ErrDevicesNeedElevation.Err()), "%v", err)

	unknown := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "x", Type: exerciseModel.DeviceTypeContainer, ResourcePreset: "gigantic"}
	err = u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(unknown)})
	require.True(t, errors.Is(err, exerciseModel.ErrDeviceResourcePresetInvalid.Err()), "%v", err)

	// A device that picked nothing is the default preset: inside the frame.
	plain := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: "p", Type: exerciseModel.DeviceTypeContainer}
	require.NoError(t, u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(plain)}))
}

func TestVersionResourcesTotalsRangeAndOutside(t *testing.T) {
	ctx := context.Background()
	store := &fakeElevations{}
	u := &ExerciseUseCase{elevations: store}
	exerciseID := uuid.Must(uuid.NewV7())
	heavy := deviceOf("db", "xlarge")
	small := variantOf(deviceOf("web", "small"))
	large := variantOf(deviceOf("web", "small"), heavy)

	res := u.versionResources(ctx, exerciseID, []exerciseModel.Variant{small, large})
	assert.Equal(t, ResourceTotals{Devices: 1, Blocks: 2, CPUMillicores: 32, MemoryBytes: 128 * mi}, res.Min)
	assert.Equal(t, ResourceTotals{Devices: 2, Blocks: 34, CPUMillicores: 532, MemoryBytes: 128*mi + 2*gi}, res.Max)
	require.Len(t, res.Variants, 2)
	assert.Equal(t, 94, res.SpreadPercent, "memory: (2176Mi-128Mi)/2176Mi")
	require.Len(t, res.Outside, 1)
	assert.Equal(t, "db", res.Outside[0].Name)
	assert.False(t, res.Outside[0].Covered)
	assert.False(t, res.Heavy)

	store.items = []resourcesModel.Elevation{{ExerciseID: exerciseID, Status: resourcesModel.ElevationApproved,
		Approved: []resourcesModel.Approval{{DeviceID: heavy.ID, Amount: blocksOf(32)}}}}
	res = u.versionResources(ctx, exerciseID, []exerciseModel.Variant{small, large})
	assert.True(t, res.Outside[0].Covered)
	assert.True(t, res.Heavy, "an approved elevation holds a device above the frame")

	assert.Empty(t, u.versionResources(ctx, exerciseID, nil).Variants, "no variants need nothing")
}

func elevationUC(t *testing.T, working []exerciseModel.Variant) (*ExerciseUseCase, *fakeElevations, *fakeElevationInbox, uuid.UUID) {
	t.Helper()
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	exerciseID, draftID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetExerciseByID(gomock.Any(), exerciseID).Return(postgres.Exercise{ID: exerciseID, Name: "Web"}, nil).AnyTimes()
	body := mustJSON(t, working)
	q.EXPECT().GetDraftVersion(gomock.Any(), exerciseID).Return(postgres.ExerciseVersion{ID: draftID, ExerciseID: exerciseID, Status: "draft", Variants: body}, nil).AnyTimes()
	q.EXPECT().ListUserNames(gomock.Any(), gomock.Any()).Return(nil, nil).AnyTimes()
	store, inbox := &fakeElevations{}, &fakeElevationInbox{}
	u := NewExerciseUseCase(Dependencies{Repo: q, Elevations: store})
	u.SetElevationInbox(inbox)
	return u, store, inbox, exerciseID
}

func TestElevationRequestApproveFlow(t *testing.T) {
	ctx := context.Background()
	heavy := deviceOf("db", "huge")
	u, store, inbox, exerciseID := elevationUC(t, []exerciseModel.Variant{variantOf(deviceOf("web", "small"), heavy)})
	author, admin := Actor{UserID: uuid.Must(uuid.NewV7())}, Actor{UserID: uuid.Must(uuid.NewV7())}

	_, err := u.RequestElevation(ctx, author, exerciseID, "  ")
	require.True(t, errors.Is(err, exerciseModel.ErrElevationReasonRequired.Err()), "%v", err)

	view, err := u.RequestElevation(ctx, author, exerciseID, "the database needs memory")
	require.NoError(t, err)
	assert.Equal(t, "pending", view.Status)
	require.Len(t, view.Requested, 1, "only the device above the frame is asked for")
	assert.Equal(t, "db", view.Requested[0].Name)
	require.Len(t, inbox.requested, 1)
	assert.Equal(t, "db: 64 blocks (1000m / 4Gi)", inbox.requested[0].Devices)
	assert.Equal(t, 64, view.Requested[0].Blocks)
	assert.Equal(t, author.UserID, inbox.requested[0].RequestedBy)

	_, err = u.RequestElevation(ctx, author, exerciseID, "again")
	require.True(t, errors.Is(err, exerciseModel.ErrElevationPending.Err()), "%v", err)

	// The admin may approve a smaller offered block, never a block that is not offered, one larger than asked, or
	// one for another device.
	_, err = u.DecideElevation(ctx, admin, view.ID, DecideElevationInput{Approve: true, Devices: []ElevationDevice{{DeviceID: heavy.ID, Blocks: 24}}})
	require.True(t, errors.Is(err, exerciseModel.ErrElevationInvalid.Err()), "%v", err)
	_, err = u.DecideElevation(ctx, admin, view.ID, DecideElevationInput{Approve: true, Devices: []ElevationDevice{{DeviceID: uuid.Must(uuid.NewV7()), Blocks: 32}}})
	require.True(t, errors.Is(err, exerciseModel.ErrElevationInvalid.Err()), "%v", err)

	decided, err := u.DecideElevation(ctx, admin, view.ID, DecideElevationInput{Approve: true, Note: "ok", Devices: []ElevationDevice{{DeviceID: heavy.ID, Blocks: 32}}})
	require.NoError(t, err)
	assert.Equal(t, "approved", decided.Status)
	require.Len(t, decided.Approved, 1)
	assert.Equal(t, 32, decided.Approved[0].Blocks)
	assert.Equal(t, int64(500), decided.Approved[0].CPUMillicores)
	assert.Equal(t, "db", decided.Approved[0].Name, "the name comes from the request")
	require.Len(t, inbox.decided, 1)
	assert.True(t, inbox.approved[0])
	assert.Equal(t, "db: 32 blocks (500m / 2Gi)", inbox.decided[0].Devices, "the author hears what was approved")

	_, err = u.DecideElevation(ctx, admin, view.ID, DecideElevationInput{Approve: false})
	require.True(t, errors.Is(err, exerciseModel.ErrElevationDecided.Err()), "%v", err)

	// The approved block (32) is below what the draft still asks (64): it is not covered yet.
	err = u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(heavy)})
	require.True(t, errors.Is(err, exerciseModel.ErrDevicesNeedElevation.Err()), "%v", err)
	assert.Len(t, store.items, 1)
}

func TestElevationRejectAndNotNeeded(t *testing.T) {
	ctx := context.Background()
	u, _, inbox, exerciseID := elevationUC(t, []exerciseModel.Variant{variantOf(deviceOf("db", "xlarge"))})
	author, admin := Actor{UserID: uuid.Must(uuid.NewV7())}, Actor{UserID: uuid.Must(uuid.NewV7())}
	view, err := u.RequestElevation(ctx, author, exerciseID, "why not")
	require.NoError(t, err)
	rejected, err := u.DecideElevation(ctx, admin, view.ID, DecideElevationInput{Approve: false, Note: "use a preset"})
	require.NoError(t, err)
	assert.Equal(t, "rejected", rejected.Status)
	assert.Equal(t, "use a preset", rejected.DecisionNote)
	assert.Equal(t, []bool{false}, inbox.approved)

	// A rejection is not an approval: the draft is still not allowed, and a new request may be filed.
	err = u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(deviceOf("db", "xlarge"))})
	require.True(t, errors.Is(err, exerciseModel.ErrDevicesNeedElevation.Err()), "%v", err)
	_, err = u.RequestElevation(ctx, author, exerciseID, "second try")
	require.NoError(t, err)

	inside, _, _, insideID := elevationUC(t, []exerciseModel.Variant{variantOf(deviceOf("web", "small"))})
	_, err = inside.RequestElevation(ctx, author, insideID, "unneeded")
	require.True(t, errors.Is(err, exerciseModel.ErrElevationNotNeeded.Err()), "%v", err)

}

func TestElevationApprovalIsKeptByALaterVersionAtOrBelowTheBlock(t *testing.T) {
	ctx := context.Background()
	heavy := deviceOf("db", "xlarge")
	u, _, _, exerciseID := elevationUC(t, []exerciseModel.Variant{variantOf(heavy)})
	author, admin := Actor{UserID: uuid.Must(uuid.NewV7())}, Actor{UserID: uuid.Must(uuid.NewV7())}
	view, err := u.RequestElevation(ctx, author, exerciseID, "db")
	require.NoError(t, err)
	_, err = u.DecideElevation(ctx, admin, view.ID, DecideElevationInput{Approve: true})
	require.NoError(t, err)

	require.NoError(t, u.requireResourcesAllowed(ctx, exerciseID, []exerciseModel.Variant{variantOf(heavy)}))
	// Another request is not needed while the block is covered.
	_, err = u.RequestElevation(ctx, author, exerciseID, "again")
	require.True(t, errors.Is(err, exerciseModel.ErrElevationNotNeeded.Err()), "%v", err)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	require.NoError(t, err)
	return b
}
