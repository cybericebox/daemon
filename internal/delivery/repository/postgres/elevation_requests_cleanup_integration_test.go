package postgres_test

import (
	"context"
	"testing"

	"github.com/gofrs/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/exerciseRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	inboxModel "github.com/cybericebox/daemon/internal/model/notification/inbox"
	"github.com/cybericebox/daemon/internal/model/rbac"
	resourcesModel "github.com/cybericebox/daemon/internal/model/resources"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

func seedElevationRequest(t *testing.T, q *postgres.Queries, recipient, elevationID uuid.UUID) uuid.UUID {
	t.Helper()
	id := uuid.Must(uuid.NewV7())
	require.NoError(t, q.CreateInApp(context.Background(), postgres.CreateInAppParams{
		ID: id, UserID: recipient, Title: "Запит на більше ресурсів", Surface: "inbox", Actions: []byte(`[]`),
		NotificationType: inboxModel.TypeElevationRequested, Category: "requests", ActionRequired: true,
		SubjectRef: pgtype.Text{String: inboxModel.ElevationRef(elevationID), Valid: true},
	}))
	return id
}

type inboxState struct {
	resolution string
	resolvedBy uuid.NullUUID
	read       bool
}

func loadInboxState(t *testing.T, db *testhelpers.TestDB, id uuid.UUID) inboxState {
	t.Helper()
	var s inboxState
	var resolution pgtype.Text
	require.NoError(t, db.Pool.QueryRow(context.Background(),
		`SELECT resolution, resolved_by, read_at IS NOT NULL FROM in_app_notifications WHERE id = $1`, id).Scan(&resolution, &s.resolvedBy, &s.read))
	s.resolution = resolution.String
	return s
}

// TestElevationRequestsCleanupMigration: the 0192 data migration closes every open elevation request whose
// elevation is gone (withdrawn) or decided (its decision), remembers the decision per subject, keeps the requests
// of pending elevations open, leaves other request types alone and is idempotent.
func TestElevationRequestsCleanupMigration(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	q := db.Queries
	repo := exerciseRepo.New(q)
	device := resourcesModel.Approval{DeviceID: uuid.Must(uuid.NewV7()), Name: "db", Amount: resourcesModel.Amount{CPUMillicores: 500, MemoryBytes: 2 << 30}}
	admin := newInboxUser(t, q, "Admin", "A.", rbac.RoleUser)
	first, second := uuid.Must(uuid.NewV7()), uuid.Must(uuid.NewV7())

	// A task deleted with its pending elevation: the row cascades, the inbox copies stay open.
	deleted := mustCreateExercise(t, repo, "IT deleted task")
	orphan := mustElevation(t, deleted.ID, device)
	require.NoError(t, repo.CreateElevation(ctx, orphan))
	orphanFirst := seedElevationRequest(t, q, first, orphan.ID)
	orphanSecond := seedElevationRequest(t, q, second, orphan.ID)
	n, err := repo.Delete(ctx, deleted.ID)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)

	// An elevation decided while its inbox copy stayed open.
	decidedTask := mustCreateExercise(t, repo, "IT decided task")
	decided := mustElevation(t, decidedTask.ID, device)
	require.NoError(t, repo.CreateElevation(ctx, decided))
	require.NoError(t, decided.Reject(admin, "no", itNow))
	n, err = repo.DecideElevation(ctx, decided)
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	decidedCopy := seedElevationRequest(t, q, first, decided.ID)

	// A still pending, valid request.
	pendingTask := mustCreateExercise(t, repo, "IT pending task")
	pending := mustElevation(t, pendingTask.ID, device)
	require.NoError(t, repo.CreateElevation(ctx, pending))
	pendingCopy := seedElevationRequest(t, q, first, pending.ID)

	// Another request type with a subject that has no elevation behind it.
	other := seedInbox(t, q, inboxSeed{user: first, category: "requests", action: true, ref: "elevation:" + uuid.Must(uuid.NewV7()).String()})

	up := readMigration(t, "0192_elevation_requests_cleanup.up.sql")
	for range 2 {
		_, err = db.Pool.Exec(ctx, up)
		require.NoError(t, err)
	}

	for _, id := range []uuid.UUID{orphanFirst, orphanSecond} {
		got := loadInboxState(t, db, id)
		assert.Equal(t, "withdrawn", got.resolution, "the orphan is closed for every recipient")
		assert.False(t, got.resolvedBy.Valid)
		assert.True(t, got.read)
	}
	got := loadInboxState(t, db, decidedCopy)
	assert.Equal(t, "rejected", got.resolution, "a decided elevation closes with its decision")
	assert.Equal(t, uuid.NullUUID{UUID: admin, Valid: true}, got.resolvedBy)

	got = loadInboxState(t, db, pendingCopy)
	assert.Empty(t, got.resolution, "a pending elevation stays open")
	assert.False(t, got.read)
	assert.Empty(t, loadInboxState(t, db, other).resolution, "other request types are not touched")

	var remembered string
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT resolution FROM inbox_subject_resolutions WHERE subject_ref = $1`,
		inboxModel.ElevationRef(orphan.ID)).Scan(&remembered))
	assert.Equal(t, "withdrawn", remembered, "a copy still in delivery is created resolved")
	var count int
	require.NoError(t, db.Pool.QueryRow(ctx, `SELECT count(*) FROM inbox_subject_resolutions WHERE subject_ref = $1`,
		inboxModel.ElevationRef(pending.ID)).Scan(&count))
	assert.Zero(t, count)

	_, err = db.Pool.Exec(ctx, readMigration(t, "0192_elevation_requests_cleanup.down.sql"))
	require.NoError(t, err)
}

// TestWithdrawPendingElevationKeepsDecidedOnes: only an undecided request is withdrawn.
func TestWithdrawPendingElevationKeepsDecidedOnes(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	repo := exerciseRepo.New(db.Queries)
	device := resourcesModel.Approval{DeviceID: uuid.Must(uuid.NewV7()), Name: "db", Amount: resourcesModel.Amount{CPUMillicores: 500, MemoryBytes: 2 << 30}}
	ex := mustCreateExercise(t, repo, "IT withdraw")

	decided := mustElevation(t, ex.ID, device)
	require.NoError(t, repo.CreateElevation(ctx, decided))
	require.NoError(t, decided.Reject(uuid.Nil, "", itNow))
	_, err := repo.DecideElevation(ctx, decided)
	require.NoError(t, err)
	n, err := repo.WithdrawPendingElevation(ctx, decided.ID)
	require.NoError(t, err)
	assert.Zero(t, n, "a decided request is history")

	pending := mustElevation(t, ex.ID, device)
	require.NoError(t, repo.CreateElevation(ctx, pending))
	n, err = repo.WithdrawPendingElevation(ctx, pending.ID)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	_, err = repo.GetElevation(ctx, pending.ID)
	require.Error(t, err, "the withdrawn request is gone")
}
