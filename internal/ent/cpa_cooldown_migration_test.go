package ent_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"entgo.io/ent/dialect/sql/schema"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/cpacredential"
	"github.com/looplj/axonhub/internal/ent/migrate"
	_ "github.com/looplj/axonhub/internal/ent/runtime"
	"github.com/looplj/axonhub/internal/objects"
	_ "github.com/looplj/axonhub/internal/pkg/sqlite"
)

func TestCPARoutingCooldownMigration(t *testing.T) {
	ctx := authz.WithTestBypass(context.Background())
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "cpa-cooldown.db")+"?_fk=0")
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	driver := entsql.OpenDB(dialect.SQLite, db)
	client := ent.NewClient(ent.Driver(driver))
	t.Cleanup(func() { require.NoError(t, client.Close()) })

	// The normal Ent test helper migrates immediately. Start with a copy of the
	// old schema instead so the upgrade is exercised with a pre-existing row.
	legacyTables, err := schema.CopyTables(migrate.Tables)
	require.NoError(t, err)
	var legacyCredentials *schema.Table
	for _, table := range legacyTables {
		if table.Name == cpacredential.Table {
			legacyCredentials = table
			break
		}
	}
	require.NotNil(t, legacyCredentials)
	isRoutingColumn := func(column *schema.Column) bool {
		return column.Name == "routing_cooldown" || column.Name == "routing_cooldown_until"
	}
	legacyCredentials.Columns = slices.DeleteFunc(legacyCredentials.Columns, isRoutingColumn)
	legacyCredentials.Indexes = slices.DeleteFunc(legacyCredentials.Indexes, func(index *schema.Index) bool {
		return slices.ContainsFunc(index.Columns, isRoutingColumn)
	})
	require.NoError(t, migrate.Create(ctx, client.Schema, legacyTables, migrate.WithForeignKeys(false)))

	// Quota exhaustion and unavailable are deliberately present: neither is
	// evidence of a CPA routing cooldown after upgrading an older installation.
	quotaUntil := time.Now().UTC().Add(time.Hour)
	_, err = db.ExecContext(ctx, `INSERT INTO cpa_credentials
		(id, cpa_instance_id, external_key, remote_name, display_name, quota_context, quota_data,
		 quota_cooling, quota_cooldown_until, unavailable)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		1, 1, "legacy", "legacy.json", "Legacy credential", "{}", "{}", true, quotaUntil, true)
	require.NoError(t, err)

	for range 2 {
		// A second migration must also preserve the row and the named index.
		require.NoError(t, client.Schema.Create(ctx, migrate.WithForeignKeys(false)))
		credential, err := client.CPACredential.Get(ctx, 1)
		require.NoError(t, err)
		require.Equal(t, objects.CPARoutingCooldownSnapshot{}, credential.RoutingCooldown)
		require.Nil(t, credential.RoutingCooldownUntil)
		require.True(t, credential.QuotaCooling)
		require.NotNil(t, credential.QuotaCooldownUntil)
		require.True(t, credential.Unavailable)

		count, err := client.CPACredential.Query().Where(
			cpacredential.CpaInstanceIDEQ(1),
			cpacredential.RoutingCooldownUntilGT(time.Now().UTC()),
		).Count(ctx)
		require.NoError(t, err)
		require.Zero(t, count)
	}

	const indexName = "cpa_credentials_by_instance_routing_cooldown"
	var storedIndexName string
	require.NoError(t, db.QueryRowContext(ctx,
		"SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = ? AND name = ?",
		cpacredential.Table, indexName,
	).Scan(&storedIndexName))
	require.Equal(t, indexName, storedIndexName)

	rows, err := db.QueryContext(ctx, `PRAGMA index_info("cpa_credentials_by_instance_routing_cooldown")`)
	require.NoError(t, err)
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var sequence, columnID int
		var name string
		require.NoError(t, rows.Scan(&sequence, &columnID, &name))
		require.Equal(t, len(columns), sequence)
		columns = append(columns, name)
	}
	require.NoError(t, rows.Err())
	require.Equal(t, []string{"cpa_instance_id", "routing_cooldown_until"}, columns)
}
