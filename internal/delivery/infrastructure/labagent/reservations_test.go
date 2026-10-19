package labagent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	labclient "github.com/cybericebox/laboratory/pkg/agent/client"
	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// resAgent is an agent's reservation API: it records the requests and answers every item with result.
type resAgent struct {
	labpb.LabManagerClient
	list    *labpb.ListRequest
	create  *labpb.CreateReservationsRequest
	update  *labpb.UpdateReservationsRequest
	del     *labpb.DeleteRequest
	items   []*labpb.Reservation
	result  *labpb.ItemResult
	callErr error
}

func (a *resAgent) Close() error { return nil }

func (a *resAgent) answer(ids []string) *labpb.BatchResult {
	out := &labpb.BatchResult{}
	for _, id := range ids {
		r := &labpb.ItemResult{Ref: &labpb.ItemRef{Name: id}, State: labpb.ItemState_ITEM_STATE_CREATED}
		if a.result != nil {
			r = &labpb.ItemResult{Ref: &labpb.ItemRef{Name: id}, State: a.result.State, Error: a.result.Error, Reason: a.result.Reason, Retryable: a.result.Retryable}
		}
		out.Results = append(out.Results, r)
	}
	return out
}

func (a *resAgent) ListReservations(_ context.Context, in *labpb.ListRequest, _ ...grpc.CallOption) (*labpb.ReservationList, error) {
	a.list = in
	if a.callErr != nil {
		return nil, a.callErr
	}
	return &labpb.ReservationList{Items: a.items}, nil
}

func (a *resAgent) CreateReservations(_ context.Context, in *labpb.CreateReservationsRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	a.create = in
	if a.callErr != nil {
		return nil, a.callErr
	}
	var ids []string
	for _, it := range in.GetItems() {
		ids = append(ids, it.GetId())
	}
	return a.answer(ids), nil
}

func (a *resAgent) UpdateReservations(_ context.Context, in *labpb.UpdateReservationsRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	a.update = in
	var ids []string
	for _, it := range in.GetItems() {
		ids = append(ids, it.GetId())
	}
	return a.answer(ids), a.callErr
}

func (a *resAgent) DeleteReservations(_ context.Context, in *labpb.DeleteRequest, _ ...grpc.CallOption) (*labpb.BatchResult, error) {
	a.del = in
	var ids []string
	for _, it := range in.GetItems() {
		ids = append(ids, it.GetName())
	}
	return a.answer(ids), a.callErr
}

func resFleet(a *resAgent) (*Fleet, *Member) {
	m := &Member{ID: uuid.Must(uuid.NewV7()), Name: "a", Enabled: true, Client: &Client{Client: labclient.Client(a), instance: "prod"}, Features: NewFeatureCell(nil)}
	return NewFleet(nil, nil, m), m
}

func TestReasonErrMapsEveryReason(t *testing.T) {
	cases := map[string]error{
		infraModel.ReasonReservationRequired: infraModel.ErrReservationRequired,
		infraModel.ReasonReservationNotFound: infraModel.ErrReservationNotFound,
		infraModel.ReasonReservationEnded:    infraModel.ErrReservationEnded,
		infraModel.ReasonReservationFull:     infraModel.ErrReservationFull,
		infraModel.ReasonReservationMismatch: infraModel.ErrReservationMismatch,
		infraModel.ReasonOverQuota:           infraModel.ErrReservationOverQuota,
	}
	for reason, want := range cases {
		err := reasonErr(&labpb.ItemResult{State: labpb.ItemState_ITEM_STATE_FAILED, Reason: reason, Error: "text"})
		assert.ErrorIs(t, err, want, reason)
	}
	unknown := reasonErr(&labpb.ItemResult{State: labpb.ItemState_ITEM_STATE_FAILED, Reason: "NEW_REASON", Error: "agent text"})
	assert.EqualError(t, unknown, "agent text", "an unknown reason keeps the agent's text")
	// A deploy refusal carries the typed error, also when the agent marks it retryable.
	full := oneResult("create lab", []*labpb.ItemResult{{State: labpb.ItemState_ITEM_STATE_FAILED, Reason: infraModel.ReasonReservationFull, Error: "not enough room in this reservation", Retryable: true}})
	assert.ErrorIs(t, full, infraModel.ErrReservationFull)
	_, retry := infraModel.AsTerminating(full)
	assert.True(t, retry, "RESERVATION_FULL is retryable: room may come back")
	required := oneResult("create lab", []*labpb.ItemResult{{State: labpb.ItemState_ITEM_STATE_FAILED, Reason: infraModel.ReasonReservationRequired, Error: "a reservation id is required"}})
	assert.ErrorIs(t, required, infraModel.ErrReservationRequired)
}

func TestListReservationsSelectsInstance(t *testing.T) {
	from, to := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC), time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC)
	id := uuid.Must(uuid.NewV7())
	a := &resAgent{items: []*labpb.Reservation{
		{Id: id.String(), FromUnixMs: from.UnixMilli(), ToUnixMs: to.UnixMilli(), CpuMillicores: 2000, MemoryBytes: 1 << 30,
			Status: &labpb.ReservationStatus{Phase: labpb.ReservationPhase_RESERVATION_PHASE_ENDING, NoticeAtUnixMs: to.Add(-5 * time.Minute).UnixMilli(),
				EndsAtUnixMs: to.UnixMilli(), EndingReason: "WindowClosing", UsedCpuMillicores: 500, UsedMemoryBytes: 1 << 20, Groups: 2}},
		{Id: "not-a-part-id"},
	}}
	f, m := resFleet(a)
	got, err := f.ListReservations(context.Background(), m.ID)
	require.NoError(t, err)
	assert.Equal(t, infraModel.LabelInstance+"=prod", a.list.GetSelector())
	require.Len(t, got, 1, "an id that is not ours is skipped")
	assert.Equal(t, id, got[0].ID)
	assert.Equal(t, from, got[0].From)
	assert.Equal(t, to, got[0].To)
	assert.Equal(t, resourcesModel.Amount{CPUMillicores: 2000, MemoryBytes: 1 << 30}, got[0].Amount)
	require.NotNil(t, got[0].Status)
	assert.Equal(t, "Ending", got[0].Status.Phase)
	assert.Equal(t, 2, got[0].Status.Groups)
	assert.Equal(t, to, *got[0].Status.EndsAt)
}

func TestUnimplementedMarksAgentIncompatible(t *testing.T) {
	a := &resAgent{callErr: status.Error(codes.Unimplemented, "unknown method ListReservations")}
	f, m := resFleet(a)
	_, err := f.ListReservations(context.Background(), m.ID)
	assert.ErrorIs(t, err, infraModel.ErrAgentIncompatible)
	assert.True(t, m.Features.Incompatible())
	assert.Empty(t, f.eligible(), "an incompatible agent gets no new groups")
	// Once the agent answers (upgraded), it is used again.
	a.callErr = nil
	_, err = f.ListReservations(context.Background(), m.ID)
	require.NoError(t, err)
	assert.False(t, m.Features.Incompatible())
	// Another error is not an incompatibility.
	a.callErr = errors.New("connection refused")
	_, err = f.ListReservations(context.Background(), m.ID)
	assert.NotErrorIs(t, err, infraModel.ErrAgentIncompatible)
	assert.False(t, m.Features.Incompatible())
}

func TestCreateReservationsCarriesInstanceLabel(t *testing.T) {
	a := &resAgent{}
	f, m := resFleet(a)
	id := uuid.Must(uuid.NewV7())
	from := time.Date(2026, 10, 19, 10, 0, 0, 0, time.UTC)
	out, err := f.CreateReservations(context.Background(), m.ID, []infraModel.ClusterReservation{{ID: id, From: from, To: from.Add(time.Hour), Amount: resourcesModel.Amount{CPUMillicores: 1, MemoryBytes: 2}}})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{infraModel.LabelInstance: "prod"}, a.create.GetLabels())
	require.Len(t, a.create.GetItems(), 1)
	assert.Equal(t, id.String(), a.create.GetItems()[0].GetId())
	assert.Equal(t, from.UnixMilli(), a.create.GetItems()[0].GetFromUnixMs())
	require.Len(t, out, 1)
	assert.True(t, out[0].OK)

	a.result = &labpb.ItemResult{State: labpb.ItemState_ITEM_STATE_FAILED, Reason: infraModel.ReasonOverQuota, Error: "over quota"}
	out, err = f.UpdateReservations(context.Background(), m.ID, []infraModel.ClusterReservation{{ID: id, From: from, To: from.Add(2 * time.Hour), Amount: resourcesModel.Amount{CPUMillicores: 1, MemoryBytes: 2}}})
	require.NoError(t, err)
	assert.False(t, out[0].OK)
	assert.Equal(t, infraModel.ReasonOverQuota, out[0].Reason)
	assert.ErrorIs(t, out[0].Err, infraModel.ErrReservationOverQuota)
	assert.Equal(t, from.Add(2*time.Hour).UnixMilli(), a.update.GetItems()[0].GetChanges().GetToUnixMs())

	a.result = &labpb.ItemResult{State: labpb.ItemState_ITEM_STATE_NOT_FOUND}
	out, err = f.DeleteReservations(context.Background(), m.ID, []uuid.UUID{id})
	require.NoError(t, err)
	assert.True(t, out[0].OK, "a reservation the agent does not know is gone already")
	assert.Equal(t, id.String(), a.del.GetItems()[0].GetName())
}
