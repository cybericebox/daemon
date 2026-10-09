package event_test

import (
	"context"
	"errors"
	"github.com/cybericebox/daemon/internal/delivery/repository/eventManagerRepo"
	manager "github.com/cybericebox/daemon/internal/model/eventManager"
	event "github.com/cybericebox/daemon/internal/useCase/event"
	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestOptionalManagerMissingAllowsApprovedParticipantJoinInfo(t *testing.T) {
	f := newStandFixture(t)
	user := approveBlueCaptain(t, f, time.Now().UTC())
	_, e := eventManagerRepo.New(f.db.Queries).Get(context.Background(), f.eventID, user)
	require.ErrorIs(t, e, manager.ErrEventManagerNotFound.Err())
	info, e := f.uc.GetJoinInfo(context.Background(), f.eventID, user)
	require.NoError(t, e)
	require.EqualValues(t, 2, info.Status)
}
func TestOptionalManagerMissingAllowsNormalNewManagerAssignment(t *testing.T) {
	f := newStandFixture(t)
	var user uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(context.Background(), "SELECT captain_id FROM event_teams WHERE id=$1", f.blueID).Scan(&user))
	out, e := f.uc.SetEventManager(context.Background(), f.eventID, event.SetEventManagerInput{UserID: user, Role: int16(manager.RoleManager)})
	require.NoError(t, e)
	require.Equal(t, user, out.UserID)
	require.EqualValues(t, manager.RoleManager, out.Role)
}
func TestOptionalManagerMissingModeratorStatsRemainForbidden(t *testing.T) {
	f := newStandFixture(t)
	var user uuid.UUID
	require.NoError(t, f.db.Pool.QueryRow(context.Background(), "SELECT captain_id FROM event_teams WHERE id=$1", f.blueID).Scan(&user))
	_, e := f.uc.GetModeratorsParticipationStats(context.Background(), f.eventID, user)
	if !errors.Is(e, manager.ErrEventManagementForbidden.Err()) {
		t.Fatalf("missing management must be forbidden, got %v", e)
	}
}
