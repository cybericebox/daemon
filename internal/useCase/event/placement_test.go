package event

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventConfigModel "github.com/cybericebox/daemon/internal/model/eventConfig"
	eventExerciseModel "github.com/cybericebox/daemon/internal/model/eventExercise"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

const (
	mi = 1 << 20
	gi = 1 << 30
)

// planInfra is the agent formula and maxima of the fleet; everything else of the port stays nil.
type planInfra struct {
	Infrastructure
	vpn, gateway infraModel.GroupPodSizing
	maxCPU       int64
}

func (p planInfra) GroupSizes(plan infraModel.GroupPlan) (infraModel.GroupSizes, bool) {
	return infraModel.LimitsFeature{VPN: p.vpn, Gateway: p.gateway}.SizesFor(plan), true
}

func (p planInfra) NeedFit(need infraModel.PlacementNeed) *infraModel.FitViolation {
	return infraModel.LimitsFeature{DeviceMaxCPUMillicores: p.maxCPU}.Fits(need)
}

func container(name, cpu, mem string) exerciseModel.Device {
	d := exerciseModel.Device{ID: uuid.Must(uuid.NewV7()), Name: name, Type: exerciseModel.DeviceTypeContainer, Image: "img"}
	if cpu != "" {
		d.Resources = &exerciseModel.DeviceResources{CPULimit: cpu, MemoryLimit: mem}
	}
	return d
}

func variant(internet bool, devices ...exerciseModel.Device) exerciseModel.Variant {
	return exerciseModel.Variant{ID: uuid.Must(uuid.NewV7()), Topology: exerciseModel.Topology{Internet: exerciseModel.NetworkSpec{Enabled: internet}, Devices: devices}}
}

func TestPlanTaskReservesTheLargestVariantOrThePinnedOne(t *testing.T) {
	policy := resourcesModel.DefaultPolicy()
	small := variant(false, container("web", "", ""))
	large := variant(true, container("web", "", ""), container("db", "250m", "1Gi"))
	exerciseID := uuid.Must(uuid.NewV7())

	perTeam := eventExerciseModel.EventExercise{ExerciseID: exerciseID, VariantMode: eventExerciseModel.VariantModePerTeam}
	task := planTask(policy, perTeam, []exerciseModel.Variant{small, large}, nil)
	assert.Equal(t, resourcesModel.Totals{Devices: 1, Amount: resourcesModel.Amount{CPUMillicores: 25, MemoryBytes: 64 * mi}}, task.Range.Min)
	assert.Equal(t, resourcesModel.Totals{Devices: 2, Amount: resourcesModel.Amount{CPUMillicores: 275, MemoryBytes: 64*mi + gi}}, task.Reserved, "the largest variant is reserved")
	assert.True(t, task.InternetLab)
	assert.Equal(t, resourcesModel.Amount{CPUMillicores: 250, MemoryBytes: gi}, task.deviceMax)
	assert.Equal(t, 2, task.labDevices)
	assert.False(t, task.Heavy)

	zero := int32(0)
	fixed := eventExerciseModel.EventExercise{ExerciseID: exerciseID, VariantMode: eventExerciseModel.VariantModeFixed, FixedVariantIndex: &zero}
	assert.Equal(t, 1, planTask(policy, fixed, []exerciseModel.Variant{small, large}, nil).Reserved.Devices, "a pinned variant is the only one that is deployed")
}

func TestPlanTaskIsHeavyOnlyWhenAnApprovalHoldsADeviceAboveTheFrame(t *testing.T) {
	policy := resourcesModel.DefaultPolicy()
	heavy := container("db", "500m", "2Gi")
	vs := []exerciseModel.Variant{variant(false, heavy)}
	link := eventExerciseModel.EventExercise{ExerciseID: uuid.Must(uuid.NewV7())}
	assert.False(t, planTask(policy, link, vs, nil).Heavy)
	approved := []resourcesModel.Approval{{DeviceID: heavy.ID, Amount: resourcesModel.Amount{CPUMillicores: 500, MemoryBytes: 2 * gi}}}
	assert.True(t, planTask(policy, link, vs, approved).Heavy)
}

type fakeApprovals map[uuid.UUID][]resourcesModel.Approval

func (f fakeApprovals) ApprovedFor(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]resourcesModel.Approval, error) {
	out := map[uuid.UUID][]resourcesModel.Approval{}
	for _, id := range ids {
		out[id] = f[id]
	}
	return out, nil
}

func TestResourcePlanCountsDevicesPlusGroupOverheadForTheTeams(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	eventID, exerciseID, versionID, linkID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	heavy := container("db", "500m", "2Gi")
	vs := []exerciseModel.Variant{variant(true, container("web", "", ""), heavy)}
	body, err := json.Marshal(vs)
	require.NoError(t, err)

	team := int16(eventConfigModel.ParticipationTeam)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{
		EventID: eventID, Participation: pgtype.Int2{Int16: team, Valid: true}, MaxTeamSize: 4, MaxTeams: pgtype.Int4{Int32: 10, Valid: true},
	}, nil)
	q.EXPECT().ListEventExercises(gomock.Any(), eventID).Return([]postgres.EventExercise{
		{ID: linkID, EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: versionID},
		{ID: uuid.Must(uuid.NewV7()), EventID: eventID, ExerciseID: uuid.Must(uuid.NewV7()), ExerciseVersionID: uuid.Must(uuid.NewV7()), Status: int16(eventExerciseModel.StatusDetached)},
	}, nil)
	q.EXPECT().ListVersionVariantDevices(gomock.Any(), []uuid.UUID{versionID}).Return([]postgres.ListVersionVariantDevicesRow{{VersionID: versionID, ExerciseID: exerciseID, Variants: body}}, nil)
	q.EXPECT().ListEventExerciseDetails(gomock.Any(), eventID).Return([]postgres.ListEventExerciseDetailsRow{{ID: linkID, ExerciseID: exerciseID, ExerciseVersionID: versionID, ExerciseName: "Web"}}, nil)

	infra := planInfra{
		vpn:     infraModel.GroupPodSizing{Base: resourcesModel.Amount{CPUMillicores: 10, MemoryBytes: 16 * mi}, PerUnit: resourcesModel.Amount{CPUMillicores: 5, MemoryBytes: 8 * mi}, Max: resourcesModel.Amount{CPUMillicores: 100, MemoryBytes: 64 * mi}},
		gateway: infraModel.GroupPodSizing{Base: resourcesModel.Amount{CPUMillicores: 20, MemoryBytes: 32 * mi}, PerUnit: resourcesModel.Amount{CPUMillicores: 10, MemoryBytes: 16 * mi}},
		maxCPU:  4000,
	}
	u := NewEventUseCase(Dependencies{Repo: q, Infra: infra, Elevations: fakeApprovals{exerciseID: {{DeviceID: heavy.ID, Amount: resourcesModel.Amount{CPUMillicores: 500, MemoryBytes: 2 * gi}}}}})
	plan, err := u.GetResourcePlan(context.Background(), eventID)
	require.NoError(t, err)

	require.Len(t, plan.Tasks, 1, "a detached attachment is not planned")
	assert.Equal(t, "Web", plan.Tasks[0].ExerciseName)
	assert.True(t, plan.Tasks[0].Heavy)
	assert.True(t, plan.Tasks[0].InternetLab)
	assert.Equal(t, resourcesModel.Totals{Devices: 2, Amount: resourcesModel.Amount{CPUMillicores: 525, MemoryBytes: 64*mi + 2*gi}}, plan.TeamTasks)

	// The VPN is sized by the maximum team size (4 users), the gateway by the internet labs (1).
	assert.Equal(t, GroupOverhead{MaxUsers: 4, InternetLabs: 1, Known: true,
		VPN:     resourcesModel.Amount{CPUMillicores: 30, MemoryBytes: 48 * mi},
		Gateway: resourcesModel.Amount{CPUMillicores: 30, MemoryBytes: 48 * mi}}, plan.Group)
	assert.Equal(t, resourcesModel.Amount{CPUMillicores: 585, MemoryBytes: 64*mi + 2*gi + 96*mi}, plan.PerTeam.Amount)
	assert.Equal(t, 2, plan.PerTeam.Devices, "the group pods are a separate line, not devices")
	assert.Equal(t, 10, plan.Teams)
	assert.Equal(t, "max_teams", plan.TeamsBasis)
	assert.Equal(t, int64(5850), plan.Total.CPUMillicores)
	assert.False(t, plan.NoAgentFits)
}

func TestResourcePlanFlagsATaskNoAgentCanRunAndNeverNamesOne(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	body, err := json.Marshal([]exerciseModel.Variant{variant(false, container("big", "2", "1Gi"))})
	require.NoError(t, err)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID}, nil)
	q.EXPECT().ListEventExercises(gomock.Any(), eventID).Return([]postgres.EventExercise{{ID: uuid.Must(uuid.NewV7()), EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: versionID}}, nil)
	q.EXPECT().ListVersionVariantDevices(gomock.Any(), gomock.Any()).Return([]postgres.ListVersionVariantDevicesRow{{VersionID: versionID, ExerciseID: exerciseID, Variants: body}}, nil)
	q.EXPECT().ListEventExerciseDetails(gomock.Any(), eventID).Return(nil, nil)

	u := NewEventUseCase(Dependencies{Repo: q, Infra: planInfra{maxCPU: 1000}})
	plan, err := u.GetResourcePlan(context.Background(), eventID)
	require.NoError(t, err)
	assert.True(t, plan.NoAgentFits)
	assert.True(t, plan.Tasks[0].NoAgentFits)
	assert.Equal(t, 1, plan.Teams, "individual participation: one group per participant, at least one")
	assert.Equal(t, 1, plan.Group.MaxUsers)
}

func TestPlacementNeedIsTheEventsLargestDeviceAndItsGroupPlan(t *testing.T) {
	ctrl := gomock.NewController(t)
	q := postgresMocks.NewMockQuerier(ctrl)
	eventID, exerciseID, versionID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	body, err := json.Marshal([]exerciseModel.Variant{variant(true, container("a", "", ""), container("b", "500m", "2Gi"))})
	require.NoError(t, err)
	team := int16(eventConfigModel.ParticipationTeam)
	q.EXPECT().GetEventConfig(gomock.Any(), eventID).Return(postgres.EventConfig{EventID: eventID, Participation: pgtype.Int2{Int16: team, Valid: true}, MaxTeamSize: 6}, nil)
	q.EXPECT().ListEventExercises(gomock.Any(), eventID).Return([]postgres.EventExercise{{ID: uuid.Must(uuid.NewV7()), EventID: eventID, ExerciseID: exerciseID, ExerciseVersionID: versionID}}, nil)
	q.EXPECT().ListVersionVariantDevices(gomock.Any(), gomock.Any()).Return([]postgres.ListVersionVariantDevicesRow{{VersionID: versionID, ExerciseID: exerciseID, Variants: body}}, nil)
	q.EXPECT().ListEventExerciseDetails(gomock.Any(), eventID).Return(nil, nil)
	q.EXPECT().CountEventTeams(gomock.Any(), eventID).Return(int64(3), nil)

	u := NewEventUseCase(Dependencies{Repo: q})
	need, err := u.eventPlacementNeed(context.Background(), eventID)
	require.NoError(t, err)
	assert.Equal(t, infraModel.PlacementNeed{
		Device: resourcesModel.Amount{CPUMillicores: 500, MemoryBytes: 2 * gi}, LabDevices: 2,
		Plan: infraModel.GroupPlan{MaxUsers: 6, InternetLabs: 1},
	}, need)
	// A second read in the TTL is served from the cache (no more query expectations).
	again, err := u.eventPlacementNeed(context.Background(), eventID)
	require.NoError(t, err)
	assert.Equal(t, need, again)
}
