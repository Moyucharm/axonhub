package datamigrate_test

import (
	"context"
	"database/sql"
	sqldriver "database/sql/driver"
	"errors"
	"fmt"
	"strings"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/ent/enttest"
	"github.com/looplj/axonhub/internal/ent/migrate/datamigrate"
	"github.com/looplj/axonhub/internal/ent/system"
	"github.com/looplj/axonhub/internal/objects"
	"github.com/looplj/axonhub/internal/server/biz"
)

func extractSettingsJSONField(t *testing.T, driver *entsql.Driver, id int, path string) []byte {
	t.Helper()
	var null sql.NullString
	err := driver.DB().QueryRowContext(context.Background(),
		"SELECT json_extract(settings, ?) FROM channels WHERE id = ?",
		path, id).Scan(&null)
	require.NoError(t, err)
	if !null.Valid {
		return nil
	}
	return []byte(null.String)
}

func TestV1_0_0_Beta9_StripsProviderQuotaFromSettings(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-providerquota?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())

	ch := client.Channel.Create().
		SetName("opencode-legacy").
		SetType(channel.TypeOpencodeGo).
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-test"}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").
		SetSettings(&objects.ChannelSettings{
			RateLimit: &objects.ChannelRateLimit{RPM: int64Ptr(10)},
		}).
		SaveX(ctx)

	// Simulate a legacy row: inject the obsolete providerQuota (incl. auth cookie).
	driver := client.Driver().(*entsql.Driver)
	legacySettings := `{"rateLimit":{"rpm":10},"providerQuota":{"opencodeGo":{"workspaceId":"wk_1","authCookie":"auth=live-session-cookie"}}}`
	_, err := driver.ExecContext(ctx,
		"UPDATE channels SET settings = ? WHERE id = ?", legacySettings, ch.ID)
	require.NoError(t, err)

	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))

	require.Nil(t, extractSettingsJSONField(t, driver, ch.ID, "$.providerQuota"))
	require.JSONEq(t, `{"rpm":10}`, string(extractSettingsJSONField(t, driver, ch.ID, "$.rateLimit")))
}

func TestV1_0_0_Beta9_IsIdempotent(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-idempotent?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())

	ch := client.Channel.Create().
		SetName("opencode-legacy-2").
		SetType(channel.TypeOpencodeGo).
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-test"}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").
		SetSettings(&objects.ChannelSettings{}).
		SaveX(ctx)

	driver := client.Driver().(*entsql.Driver)
	_, err := driver.ExecContext(ctx,
		"UPDATE channels SET settings = ? WHERE id = ?",
		`{"providerQuota":{"opencodeGo":{"workspaceId":"wk_2"}}}`, ch.ID)
	require.NoError(t, err)

	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))
	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))

	require.Nil(t, extractSettingsJSONField(t, driver, ch.ID, "$.providerQuota"))
}

func TestV1_0_0_Beta9_StripsJsonNullProviderQuota(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-jsonnull?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())

	ch := client.Channel.Create().
		SetName("opencode-json-null").
		SetType(channel.TypeOpencodeGo).
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-test"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		SetSettings(&objects.ChannelSettings{}).
		SaveX(ctx)

	driver := client.Driver().(*entsql.Driver)
	// JSON-null providerQuota is a present key that json_extract maps to SQL NULL.
	_, err := driver.ExecContext(ctx,
		"UPDATE channels SET settings = ? WHERE id = ?",
		`{"providerQuota":null}`, ch.ID)
	require.NoError(t, err)

	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))

	require.Nil(t, extractSettingsJSONField(t, driver, ch.ID, "$.providerQuota"))
}

func TestV1_0_0_Beta9_LeavesUntouchedChannelsAlone(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-untouched?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())

	ch := client.Channel.Create().
		SetName("opencode-clean").
		SetType(channel.TypeOpencodeGo).
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-test"}).SetSupportedModels([]string{"test-model"}).SetDefaultTestModel("test-model").
		SetSettings(&objects.ChannelSettings{
			RateLimit: &objects.ChannelRateLimit{RPM: int64Ptr(10)},
		}).
		SaveX(ctx)

	driver := client.Driver().(*entsql.Driver)
	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))

	require.JSONEq(t, `{"rpm":10}`, string(extractSettingsJSONField(t, driver, ch.ID, "$.rateLimit")))
	require.Nil(t, extractSettingsJSONField(t, driver, ch.ID, "$.providerQuota"))
}

func int64Ptr(v int64) *int64 { return &v }

// dirtyUpdatedAt mimics the corrupted value channel_price.go used to persist:
// time.Now() (monotonic suffix "m=+..." plus local timezone) serialized by the
// SQLite driver via time.Time.String().
const dirtyUpdatedAt = "2026-08-18 00:13:11.396794292 +0800 CST m=+0.000011483"

// cleanedUpdatedAt is dirtyUpdatedAt with the monotonic suffix stripped, i.e.
// the exact string the driver produces after a scan round-trip. The optimistic
// lock in UpdateChannel compares against this value via updated_at = ?.
const cleanedUpdatedAt = "2026-08-18 00:13:11.396794292 +0800 CST"

// updatedAtMatches simulates the optimistic-lock comparison UpdateChannel
// performs: WHERE updated_at = <snapshot>. Returns how many rows match.
func updatedAtMatches(t *testing.T, driver *entsql.Driver, id int, updatedAt string) int {
	t.Helper()
	var n int
	require.NoError(t, driver.DB().QueryRowContext(context.Background(),
		"SELECT count(*) FROM channels WHERE id = ? AND updated_at = ?", id, updatedAt).Scan(&n))
	return n
}

func TestV1_0_0_Beta9_StripsMonotonicSuffixFromUpdatedAt(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-updated-at?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())

	ch := client.Channel.Create().
		SetName("dirty-updated-at").
		SetType(channel.TypeOpenai).
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-test"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		SaveX(ctx)

	driver := client.Driver().(*entsql.Driver)
	_, err := driver.ExecContext(ctx,
		"UPDATE channels SET updated_at = ? WHERE id = ?", dirtyUpdatedAt, ch.ID)
	require.NoError(t, err)

	// Before migration the optimistic lock can never match: the stored value
	// still carries the "m=+..." suffix.
	require.Equal(t, 0, updatedAtMatches(t, driver, ch.ID, cleanedUpdatedAt))

	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))

	// After migration the cleaned snapshot matches, so UpdateChannel recovers.
	require.Equal(t, 1, updatedAtMatches(t, driver, ch.ID, cleanedUpdatedAt))
	// The raw dirty value is gone.
	require.Equal(t, 0, updatedAtMatches(t, driver, ch.ID, dirtyUpdatedAt))
}

func TestV1_0_0_Beta9_PreservesCleanUpdatedAt(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-clean-updated-at?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())

	ch := client.Channel.Create().
		SetName("clean-updated-at").
		SetType(channel.TypeOpenai).
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-test"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		SaveX(ctx)

	clean := "2026-08-18 00:13:11.396794292 +0000 UTC"
	driver := client.Driver().(*entsql.Driver)
	_, err := driver.ExecContext(ctx,
		"UPDATE channels SET updated_at = ? WHERE id = ?", clean, ch.ID)
	require.NoError(t, err)

	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))

	// Unaffected rows still satisfy the optimistic lock.
	require.Equal(t, 1, updatedAtMatches(t, driver, ch.ID, clean))
}

func TestV1_0_0_Beta9_StripsMonotonicSuffixIsIdempotent(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-updated-at-idem?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())

	ch := client.Channel.Create().
		SetName("dirty-updated-at-idem").
		SetType(channel.TypeOpenai).
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-test"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		SaveX(ctx)

	driver := client.Driver().(*entsql.Driver)
	_, err := driver.ExecContext(ctx,
		"UPDATE channels SET updated_at = ? WHERE id = ?", dirtyUpdatedAt, ch.ID)
	require.NoError(t, err)

	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))
	require.Equal(t, 1, updatedAtMatches(t, driver, ch.ID, cleanedUpdatedAt))

	// Running again must not corrupt anything further.
	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))
	require.Equal(t, 1, updatedAtMatches(t, driver, ch.ID, cleanedUpdatedAt))
	require.Equal(t, 0, updatedAtMatches(t, driver, ch.ID, dirtyUpdatedAt))
}

type recordingDriver struct {
	dialect     string
	sql         *sql.DB
	execQueries []string
	queries     []string
}

func (d *recordingDriver) Dialect() string { return d.dialect }

func (d *recordingDriver) Close() error { return nil }

func (d *recordingDriver) Tx(context.Context) (dialect.Tx, error) {
	return nil, errors.New("unexpected tx")
}

// recordingScanner answers fake-driver queries. INSERT ... RETURNING gets one
// row carrying id=1 so the marker upsert succeeds; plain SELECTs get zero rows
// so the marker lookup maps to ent NotFound and the migration proceeds.
type recordingScanner struct{ oneRow bool }

func (s *recordingScanner) Next() bool {
	n := s.oneRow
	s.oneRow = false
	return n
}
func (s *recordingScanner) Close() error                            { return nil }
func (s *recordingScanner) Columns() ([]string, error)              { return []string{"id"}, nil }
func (s *recordingScanner) ColumnTypes() ([]*sql.ColumnType, error) { return nil, nil }
func (s *recordingScanner) Err() error                              { return nil }
func (s *recordingScanner) NextResultSet() bool                     { return false }
func (s *recordingScanner) Scan(dest ...any) error {
	for _, d := range dest {
		if p, ok := d.(*int64); ok {
			*p = 1
		}
	}
	return nil
}

func (d *recordingDriver) Query(_ context.Context, query string, _ any, v any) error {
	d.queries = append(d.queries, query)
	rows, ok := v.(*entsql.Rows)
	if !ok {
		return fmt.Errorf("expected *entsql.Rows, got %T", v)
	}
	rows.ColumnScanner = &recordingScanner{oneRow: strings.Contains(query, "INSERT")}
	return nil
}

func (d *recordingDriver) Exec(_ context.Context, query string, _ any, v any) error {
	d.execQueries = append(d.execQueries, query)
	result, ok := v.(*sql.Result)
	if !ok {
		return fmt.Errorf("expected *sql.Result, got %T", v)
	}
	*result = sqldriver.RowsAffected(0)
	return nil
}

// execFailingDriver wraps a real dialect.Driver and injects an Exec failure for
// queries containing failQuery, without affecting Query calls.
type execFailingDriver struct {
	dialect.Driver
	failQuery string
}

func (d *execFailingDriver) Exec(ctx context.Context, query string, args any, v any) error {
	if strings.Contains(query, d.failQuery) {
		return fmt.Errorf("injected exec failure for %q", d.failQuery)
	}
	return d.Driver.Exec(ctx, query, args, v)
}

func markerCount(t *testing.T, client *ent.Client, ctx context.Context) int {
	t.Helper()
	n, err := client.System.Query().
		Where(system.KeyEQ(biz.SystemKeyDataMigrateV1_0_0_Beta9Done)).
		Count(ctx)
	require.NoError(t, err)
	return n
}

func TestV1_0_0_Beta9_WritesCompletionMarker(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-marker?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	require.Equal(t, 0, markerCount(t, client, ctx))

	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))
	require.Equal(t, 1, markerCount(t, client, ctx))
}

func TestV1_0_0_Beta9_SkipsCleanupWhenMarkerPresent(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-skip?mode=memory&_fk=1")
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))

	// Simulate a late-arriving legacy row AFTER the migration already ran.
	driver := client.Driver().(*entsql.Driver)
	ch := client.Channel.Create().
		SetName("late-legacy").
		SetType(channel.TypeOpenai).
		SetCredentials(objects.ChannelCredentials{APIKey: "sk-test"}).
		SetSupportedModels([]string{"test-model"}).
		SetDefaultTestModel("test-model").
		SaveX(ctx)
	_, err := driver.ExecContext(ctx,
		"UPDATE channels SET settings = ? WHERE id = ?",
		`{"providerQuota":{"opencodeGo":{"workspaceId":"wk_3"}}}`, ch.ID)
	require.NoError(t, err)

	// The completion marker makes the re-run skip cleanup entirely, so the
	// stale providerQuota injected above stays untouched.
	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))
	require.NotNil(t, extractSettingsJSONField(t, driver, ch.ID, "$.providerQuota"))
	require.Equal(t, 1, markerCount(t, client, ctx))
}

func TestV1_0_0_Beta9_FailedCleanupDoesNotWriteMarker(t *testing.T) {
	client := enttest.NewEntClient(t, "sqlite3", "file:beta9-fail?mode=memory&_fk=1")
	defer client.Close()

	wrapped := &execFailingDriver{
		Driver:    client.Driver(),
		failQuery: "json_remove",
	}
	failing := ent.NewClient(ent.Driver(wrapped))
	defer failing.Close()

	ctx := authz.WithTestBypass(context.Background())

	require.Error(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, failing))
	require.Equal(t, 0, markerCount(t, client, ctx))

	// Once the failure is gone, the next run completes and writes the marker.
	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))
	require.Equal(t, 1, markerCount(t, client, ctx))
}

func TestV1_0_0_Beta9_PostgresSkipsMonotonicUpdatedAtCleanup(t *testing.T) {
	drv := &recordingDriver{dialect: dialect.Postgres}
	client := ent.NewClient(ent.Driver(drv))
	defer client.Close()

	ctx := authz.WithTestBypass(context.Background())
	require.NoError(t, datamigrate.NewV1_0_0_Beta9().Migrate(ctx, client))

	// Only the providerQuota purge is issued via Exec; the monotonic cleanup
	// is skipped on Postgres. The marker lookup and the INSERT ... RETURNING
	// completion-marker upsert travel through the Query path.
	require.Equal(t, []string{
		`UPDATE channels SET settings = settings #- '{providerQuota}' WHERE settings ? 'providerQuota'`,
	}, drv.execQueries)
	// The marker key travels as a bind argument, so assert on statement shape:
	// first the marker lookup, then the INSERT ... RETURNING upsert.
	require.Len(t, drv.queries, 2)
	require.Regexp(t, `^SELECT "systems"`, drv.queries[0])
	require.Regexp(t, `^INSERT INTO "systems"`, drv.queries[1])
	require.Contains(t, drv.queries[1], "RETURNING")
}
