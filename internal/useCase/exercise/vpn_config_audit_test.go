package exercise_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"go.uber.org/mock/gomock"

	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	postgresMocks "github.com/cybericebox/daemon/internal/delivery/repository/postgres/mocks"
	eventActivityModel "github.com/cybericebox/daemon/internal/model/eventActivity"
	exerciseModel "github.com/cybericebox/daemon/internal/model/exercise"
	vpnModel "github.com/cybericebox/daemon/internal/model/vpn"
	"github.com/cybericebox/daemon/internal/useCase/exercise"
)

type auditSpy struct {
	rows   []eventActivityModel.Activity
	window time.Duration
	err    error
}

func (s *auditSpy) AppendOnce(_ context.Context, a eventActivityModel.Activity, window time.Duration) (bool, error) {
	s.rows, s.window = append(s.rows, a), window
	return true, s.err
}

func auditedDeploy(t *testing.T, eventID uuid.NullUUID, spy *auditSpy) (*exercise.ExerciseUseCase, uuid.UUID, uuid.UUID) {
	t.Helper()
	q := postgresMocks.NewMockQuerier(gomock.NewController(t))
	userID, deployID := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())
	q.EXPECT().GetOwnedExerciseTestDeploy(gomock.Any(), gomock.Any()).Return(postgres.ExerciseTestDeployment{
		ID: deployID, GroupName: "t-group", CreatedBy: userID, EventID: eventID,
	}, nil).AnyTimes()
	infra := &fakeInfra{status: exerciseModel.LabDeployStatus{Phase: "Ready", Ready: true}}
	// An event-scoped deploy keeps its config under the event id.
	store := &fakeVPNStore{userID: userID, scope: vpnModel.ScopeTest, ref: eventID, plaintext: "wg-author-config"}
	uc := exercise.NewExerciseUseCase(exercise.Dependencies{Repo: q, Infra: infra, VPNStore: store})
	uc.SetTestLabGate(&eventPreviewGate{until: time.Now().Add(time.Hour)})
	uc.SetVPNConfigAudit(spy)
	return uc, userID, deployID
}

func TestDeployStatusLogsTheConfigDownloadOfAnEventScopedDeploy(t *testing.T) {
	eventID := uuid.Must(uuid.NewV7())
	spy := &auditSpy{}
	uc, userID, deployID := auditedDeploy(t, uuid.NullUUID{UUID: eventID, Valid: true}, spy)
	st, err := uc.DeployTestStatus(context.Background(), userID, deployID)
	if err != nil || st.VPNConfig == "" {
		t.Fatalf("status=%+v err=%v", st, err)
	}
	if len(spy.rows) != 1 {
		t.Fatalf("rows=%d", len(spy.rows))
	}
	row := spy.rows[0]
	if row.Kind != eventActivityModel.KindVPNConfigDownloaded || row.EventID != eventID || row.UserID != userID ||
		row.Data["config"] != string(eventActivityModel.VPNConfigDeploy) || row.SubjectID == nil || *row.SubjectID != deployID {
		t.Fatalf("row=%+v", row)
	}
	if spy.window != eventActivityModel.VPNConfigWindow {
		t.Fatalf("a polled status must be deduplicated, window=%v", spy.window)
	}
}

func TestDeployStatusDoesNotLogAPlatformDeployOrAMissingConfig(t *testing.T) {
	spy := &auditSpy{}
	uc, userID, deployID := auditedDeploy(t, uuid.NullUUID{}, spy)
	if _, err := uc.DeployTestStatus(context.Background(), userID, deployID); err != nil {
		t.Fatal(err)
	}
	if len(spy.rows) != 0 {
		t.Fatalf("a deploy without an event has no event activity: %+v", spy.rows)
	}

	spy = &auditSpy{}
	uc, userID, deployID = auditedDeploy(t, uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}, spy)
	other, err := uc.PreviewDeployStatus(context.Background(), userID, deployID)
	if err != nil || other.VPNConfig == "" {
		t.Fatalf("status=%+v err=%v", other, err)
	}
	if len(spy.rows) != 0 {
		t.Fatalf("the preview logs its own download: %+v", spy.rows)
	}
}

func TestDeployStatusSurvivesAFailedAuditWrite(t *testing.T) {
	spy := &auditSpy{err: errors.New("db down")}
	uc, userID, deployID := auditedDeploy(t, uuid.NullUUID{UUID: uuid.Must(uuid.NewV7()), Valid: true}, spy)
	if st, err := uc.DeployTestStatus(context.Background(), userID, deployID); err != nil || st.VPNConfig == "" {
		t.Fatalf("a failed log write must not break polling: %+v %v", st, err)
	}
}
