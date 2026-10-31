package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/gofrs/uuid"
	"github.com/stretchr/testify/require"

	"github.com/cybericebox/daemon/internal/delivery/repository/agentProfileRepo"
	"github.com/cybericebox/daemon/internal/delivery/repository/postgres"
	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The last node profile report of an agent is kept, a newer report replaces it, an older one does not, and it
// goes with the agent.
func TestAgentNodeProfileReportIsKeptAndOverwritten(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	ctx := context.Background()
	id := uuid.Must(uuid.NewV7())
	_, err := db.Queries.CreateInfrastructureAgent(ctx, postgres.CreateInfrastructureAgentParams{
		ID:                  id,
		Key:                 "profiles-agent",
		Name:                "profiles-agent",
		Source:              "admin",
		Endpoint:            "agent:443",
		Tenant:              "t",
		ClientCertPem:       "c",
		ClientKeyCiphertext: "k",
		CaPem:               "ca",
		Enabled:             true,
		CreatedAt:           itNow,
		UpdatedAt:           itNow,
	})
	require.NoError(t, err)

	repo := agentProfileRepo.New(db.Pool)
	require.NoError(
		t,
		repo.Save(ctx, id, []byte(`[{"profile":"container","available_now":true}]`), itNow),
	)
	require.NoError(
		t,
		repo.Save(
			ctx,
			id,
			[]byte(`[{"profile":"microvm","available_now":true}]`),
			itNow.Add(time.Minute),
		),
	)
	require.NoError(
		t,
		repo.Save(ctx, id, []byte(`[]`), itNow.Add(-time.Hour)),
		"an older report changes nothing",
	)

	got, err := repo.List(ctx)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, id, got[0].AgentID)
	require.JSONEq(t, `[{"profile":"microvm","available_now":true}]`, string(got[0].Data))
	require.True(t, got[0].ReceivedAt.Equal(itNow.Add(time.Minute)))

	_, err = db.Pool.Exec(ctx, `DELETE FROM infrastructure_agents WHERE id=$1`, id)
	require.NoError(t, err)
	got, err = repo.List(ctx)
	require.NoError(t, err)
	require.Empty(t, got)
}
