package labagent

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/gofrs/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	labpb "github.com/cybericebox/laboratory/pkg/agent/protobuf"

	errorJournal "github.com/cybericebox/daemon/internal/model/errorJournal"
	infraModel "github.com/cybericebox/daemon/internal/model/infrastructure"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
)

// The fleet is the calendar's port to the agents' reservations.
var _ infraModel.ReservationPort = (*Fleet)(nil)

// reasonErr maps the agent's refusal reason of an item onto its error; an unknown reason keeps the agent's text.
func reasonErr(r *labpb.ItemResult) error {
	if err := infraModel.ReasonError(r.GetReason()); err != nil {
		if r.GetError() != "" {
			return fmt.Errorf("%w: %s", err, r.GetError())
		}
		return err
	}
	if r.GetError() != "" {
		return errors.New(r.GetError())
	}
	return errors.New("the agent refused the item")
}

// reservationPhase maps the agent's phase onto the calendar's words.
func reservationPhase(p labpb.ReservationPhase) string {
	switch p {
	case labpb.ReservationPhase_RESERVATION_PHASE_PENDING:
		return "Pending"
	case labpb.ReservationPhase_RESERVATION_PHASE_ACTIVE:
		return "Active"
	case labpb.ReservationPhase_RESERVATION_PHASE_ENDING:
		return "Ending"
	case labpb.ReservationPhase_RESERVATION_PHASE_ENDED:
		return "Ended"
	default:
		return ""
	}
}

func optionalMillis(ms int64) *time.Time {
	if ms <= 0 {
		return nil
	}
	t := time.UnixMilli(ms).UTC()
	return &t
}

// reservationFrom maps a reservation the agent reports; an id that is not a part id (another backend's naming) is
// skipped by the caller.
func reservationFrom(r *labpb.Reservation) (infraModel.ClusterReservation, bool) {
	id, err := uuid.FromString(r.GetId())
	if err != nil {
		return infraModel.ClusterReservation{}, false
	}
	out := infraModel.ClusterReservation{
		ID: id, From: time.UnixMilli(r.GetFromUnixMs()).UTC(), To: time.UnixMilli(r.GetToUnixMs()).UTC(),
		Amount: resourcesModel.Amount{CPUMillicores: r.GetCpuMillicores(), MemoryBytes: r.GetMemoryBytes()},
	}
	if st := r.GetStatus(); st != nil {
		out.Status = &infraModel.ClusterReservationStatus{
			Phase: reservationPhase(st.GetPhase()), NoticeAt: optionalMillis(st.GetNoticeAtUnixMs()), EndsAt: optionalMillis(st.GetEndsAtUnixMs()),
			EndingReason: st.GetEndingReason(),
			Used:         resourcesModel.Amount{CPUMillicores: st.GetUsedCpuMillicores(), MemoryBytes: st.GetUsedMemoryBytes()},
			Groups:       int(st.GetGroups()), Exceeded: st.GetExceeded(), OverAmount: st.GetOverAmount(), Invalid: st.GetInvalid(),
		}
		if at := optionalMillis(st.GetUsedAtUnixMs()); at != nil {
			out.Status.ObservedAt = *at
		}
	}
	return out, true
}

// outcomes maps the answers of a batch onto its ids, by the reference name the agent echoes and else by order.
func outcomes(ids []uuid.UUID, res []*labpb.ItemResult) []infraModel.ItemOutcome {
	out := make([]infraModel.ItemOutcome, 0, len(ids))
	byName := make(map[string]*labpb.ItemResult, len(res))
	for _, r := range res {
		if name := r.GetRef().GetName(); name != "" {
			byName[name] = r
		}
	}
	for i, id := range ids {
		r := byName[id.String()]
		if r == nil && i < len(res) {
			r = res[i]
		}
		if r == nil {
			out = append(out, infraModel.ItemOutcome{ID: id, Err: errors.New("the agent gave no answer for the reservation")})
			continue
		}
		o := infraModel.ItemOutcome{ID: id, Reason: r.GetReason(), Retryable: r.GetRetryable()}
		if r.GetState() == labpb.ItemState_ITEM_STATE_FAILED {
			o.Err = reasonErr(r)
		} else {
			o.OK = true
		}
		out = append(out, o)
	}
	return out
}

// ReservationsOf converts the reservations a monitoring message carries and the ids of the deleted ones; a reservation
// whose id is not a part id is not ours and is left out.
func ReservationsOf(rs []*labpb.Reservation, deleted []string) ([]infraModel.ClusterReservation, []uuid.UUID) {
	listed := make([]infraModel.ClusterReservation, 0, len(rs))
	for _, r := range rs {
		if v, ok := reservationFrom(r); ok {
			listed = append(listed, v)
		}
	}
	var gone []uuid.UUID
	for _, name := range deleted {
		if id, err := uuid.FromString(name); err == nil {
			gone = append(gone, id)
		}
	}
	return listed, gone
}

// ListReservations lists this platform instance's reservations on the agent.
func (c *Client) ListReservations(ctx context.Context) ([]infraModel.ClusterReservation, error) {
	list, err := c.Client.ListReservations(ctx, &labpb.ListRequest{Selector: c.MonitoringSelector()})
	if err != nil {
		return nil, err
	}
	out := make([]infraModel.ClusterReservation, 0, len(list.GetItems()))
	for _, r := range list.GetItems() {
		if v, ok := reservationFrom(r); ok {
			out = append(out, v)
		}
	}
	return out, nil
}

// CreateReservations creates reservations; every one carries this instance's label.
func (c *Client) CreateReservations(ctx context.Context, rs []infraModel.ClusterReservation) ([]infraModel.ItemOutcome, error) {
	items := make([]*labpb.ReservationItem, 0, len(rs))
	ids := make([]uuid.UUID, 0, len(rs))
	for _, r := range rs {
		items = append(items, &labpb.ReservationItem{
			Id: r.ID.String(), FromUnixMs: r.From.UnixMilli(), ToUnixMs: r.To.UnixMilli(),
			CpuMillicores: r.Amount.CPUMillicores, MemoryBytes: r.Amount.MemoryBytes,
		})
		ids = append(ids, r.ID)
	}
	res, err := c.Client.CreateReservations(ctx, &labpb.CreateReservationsRequest{Labels: c.requestLabels(), Items: items})
	if err != nil {
		return nil, agentErr("create reservations", err)
	}
	return outcomes(ids, res.GetResults()), nil
}

// UpdateReservations sets the window and the amount of reservations by id (the id stays).
func (c *Client) UpdateReservations(ctx context.Context, rs []infraModel.ClusterReservation) ([]infraModel.ItemOutcome, error) {
	items := make([]*labpb.UpdateReservationItem, 0, len(rs))
	ids := make([]uuid.UUID, 0, len(rs))
	for _, r := range rs {
		from, to := r.From.UnixMilli(), r.To.UnixMilli()
		cpu, mem := r.Amount.CPUMillicores, r.Amount.MemoryBytes
		items = append(items, &labpb.UpdateReservationItem{Id: r.ID.String(), Changes: &labpb.ReservationChanges{
			FromUnixMs: &from, ToUnixMs: &to, CpuMillicores: &cpu, MemoryBytes: &mem,
		}})
		ids = append(ids, r.ID)
	}
	res, err := c.Client.UpdateReservations(ctx, &labpb.UpdateReservationsRequest{Items: items})
	if err != nil {
		return nil, agentErr("update reservations", err)
	}
	return outcomes(ids, res.GetResults()), nil
}

// DeleteReservations deletes reservations by id; one the agent does not know is gone already.
func (c *Client) DeleteReservations(ctx context.Context, ids []uuid.UUID) ([]infraModel.ItemOutcome, error) {
	refs := make([]*labpb.ItemRef, 0, len(ids))
	for _, id := range ids {
		refs = append(refs, &labpb.ItemRef{Name: id.String()})
	}
	res, err := c.Client.DeleteReservations(ctx, &labpb.DeleteRequest{Items: refs})
	if err != nil {
		return nil, agentErr("delete reservations", err)
	}
	return outcomes(ids, res.GetResults()), nil
}

// reservationMember finds the member of an agent for a reservation call.
func (f *Fleet) reservationMember(agentID uuid.UUID) (*Member, error) {
	m := f.member(agentID)
	if m == nil {
		return nil, fmt.Errorf("agent %s is not in the fleet", agentID)
	}
	return m, nil
}

// incompatible marks an agent that answers UNIMPLEMENTED on the reservation API: it gets no new groups, and the
// admin is told to upgrade it. There is no fallback.
func (f *Fleet) incompatible(m *Member, err error) error {
	if status.Code(err) != codes.Unimplemented {
		return err
	}
	if m.Features.MarkIncompatible() {
		errorJournal.Report(errorJournal.Event{
			Kind: errorJournal.KindLabComponent, Source: m.Name, Message: infraModel.ErrAgentIncompatible.Error(),
			Key: "reservation-api:" + m.ID.String(), At: time.Now(),
		})
	}
	return fmt.Errorf("%w: %v", infraModel.ErrAgentIncompatible, err)
}

// ListReservations lists this instance's reservations on one agent. An agent without the reservation API is marked
// incompatible.
func (f *Fleet) ListReservations(ctx context.Context, agentID uuid.UUID) ([]infraModel.ClusterReservation, error) {
	m, err := f.reservationMember(agentID)
	if err != nil {
		return nil, err
	}
	out, err := m.Client.ListReservations(ctx)
	if err != nil {
		return nil, f.incompatible(m, err)
	}
	m.Features.MarkCompatible()
	return out, nil
}

func (f *Fleet) CreateReservations(ctx context.Context, agentID uuid.UUID, rs []infraModel.ClusterReservation) ([]infraModel.ItemOutcome, error) {
	m, err := f.reservationMember(agentID)
	if err != nil {
		return nil, err
	}
	out, err := m.Client.CreateReservations(ctx, rs)
	if err != nil {
		return nil, f.incompatible(m, err)
	}
	return out, nil
}

func (f *Fleet) UpdateReservations(ctx context.Context, agentID uuid.UUID, rs []infraModel.ClusterReservation) ([]infraModel.ItemOutcome, error) {
	m, err := f.reservationMember(agentID)
	if err != nil {
		return nil, err
	}
	out, err := m.Client.UpdateReservations(ctx, rs)
	if err != nil {
		return nil, f.incompatible(m, err)
	}
	return out, nil
}

func (f *Fleet) DeleteReservations(ctx context.Context, agentID uuid.UUID, ids []uuid.UUID) ([]infraModel.ItemOutcome, error) {
	m, err := f.reservationMember(agentID)
	if err != nil {
		return nil, err
	}
	out, err := m.Client.DeleteReservations(ctx, ids)
	if err != nil {
		return nil, f.incompatible(m, err)
	}
	return out, nil
}

// ReservationNotice is how long before its end an agent gives the ending notice, as it reports; zero when unknown.
func (f *Fleet) ReservationNotice(agentID uuid.UUID) time.Duration {
	m := f.member(agentID)
	if m == nil {
		return 0
	}
	if feat := m.Features.Get(); feat != nil {
		return time.Duration(feat.Reservations.NoticeSeconds) * time.Second
	}
	return 0
}
