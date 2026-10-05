package postgres_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cybericebox/daemon/internal/testhelpers"
)

// The stand engine's listing runs against the real migrated schema: a query still selecting a column
// that a migration dropped (event_configs.stand_deploy_lead_minutes, 0156) fails here, not in production.
func TestListStandEventsRunsAgainstTheMigratedSchema(t *testing.T) {
	db := testhelpers.SetupTestDB(t)
	if _, err := db.Queries.ListStandEvents(context.Background(), time.Now()); err != nil {
		t.Fatalf("ListStandEvents: %v", err)
	}
}

// No query may mention a column that a migration dropped.
func TestQueriesDoNotUseDroppedColumns(t *testing.T) {
	files, err := filepath.Glob(filepath.Join("queries", "*.sql"))
	if err != nil || len(files) == 0 {
		t.Fatalf("queries not found: %v", err)
	}
	for _, f := range files {
		body, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(body), "stand_deploy_lead_minutes") {
			t.Errorf("%s uses the dropped column stand_deploy_lead_minutes", f)
		}
	}
}
