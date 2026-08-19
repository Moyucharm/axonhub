package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"entgo.io/ent/dialect"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
)

func TestMigrateLegacyChannelTypesWithoutChannelsTable(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:legacy-channel-type-empty?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if err := migrateLegacyChannelTypes(context.Background(), dialect.SQLite, db); err != nil {
		t.Fatalf("migrateLegacyChannelTypes() error = %v", err)
	}
}

func TestMigrateLegacyChannelTypes(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:legacy-channel-type-existing?mode=memory&cache=shared")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	if _, err := db.Exec(`CREATE TABLE channels (
		id INTEGER PRIMARY KEY,
		type TEXT NOT NULL,
		name TEXT NOT NULL,
		base_url TEXT NOT NULL
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO channels (id, type, name, base_url) VALUES
		(1, 'atlascloud', 'legacy', 'https://api.atlascloud.ai/v1'),
		(2, 'openai', 'existing', 'https://api.openai.com/v1')
	`); err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	if err := migrateLegacyChannelTypes(ctx, dialect.SQLite, db); err != nil {
		t.Fatalf("first migrateLegacyChannelTypes() error = %v", err)
	}
	if err := migrateLegacyChannelTypes(ctx, dialect.SQLite, db); err != nil {
		t.Fatalf("second migrateLegacyChannelTypes() error = %v", err)
	}

	var channelType, name, baseURL string
	if err := db.QueryRow(`SELECT type, name, base_url FROM channels WHERE id = 1`).Scan(&channelType, &name, &baseURL); err != nil {
		t.Fatal(err)
	}
	if channelType != "openai" {
		t.Fatalf("legacy channel type = %q, want %q", channelType, "openai")
	}
	if name != "legacy" || baseURL != "https://api.atlascloud.ai/v1" {
		t.Fatalf("legacy channel configuration changed: name=%q base_url=%q", name, baseURL)
	}

	if err := db.QueryRow(`SELECT type FROM channels WHERE id = 2`).Scan(&channelType); err != nil {
		t.Fatal(err)
	}
	if channelType != "openai" {
		t.Fatalf("existing OpenAI channel type = %q, want %q", channelType, "openai")
	}
}

func TestNewEntClientMigratesLegacyChannelTypeBeforeSchemaMigration(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "legacy-channel.db") + "?_fk=0"
	client := NewEntClient(Config{Dialect: "sqlite3", DSN: dsn})
	ctx := authz.WithTestBypass(context.Background())
	created, err := client.Channel.Create().
		SetType(channel.TypeOpenai).
		SetName("legacy-official-channel").
		SetBaseURL("https://api.atlascloud.ai/v1").
		SetCredentials(objects.ChannelCredentials{APIKey: "legacy-key"}).
		SetSupportedModels([]string{"deepseek-v3"}).
		SetDefaultTestModel("deepseek-v3").
		Save(ctx)
	if err != nil {
		client.Close()
		t.Fatal(err)
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	rawDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rawDB.Exec(`UPDATE channels SET type = 'atlascloud' WHERE id = ?`, created.ID); err != nil {
		rawDB.Close()
		t.Fatal(err)
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	migratedClient := NewEntClient(Config{Dialect: "sqlite3", DSN: dsn})
	defer migratedClient.Close()
	migrated, err := migratedClient.Channel.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if migrated.Type != channel.TypeOpenai {
		t.Fatalf("migrated channel type = %q, want %q", migrated.Type, channel.TypeOpenai)
	}
	if migrated.BaseURL != "https://api.atlascloud.ai/v1" || migrated.Credentials.APIKey != "legacy-key" {
		t.Fatalf("migrated channel configuration changed: base_url=%q api_key=%q", migrated.BaseURL, migrated.Credentials.APIKey)
	}
	if len(migrated.SupportedModels) != 1 || migrated.SupportedModels[0] != "deepseek-v3" {
		t.Fatalf("migrated supported models = %v, want [deepseek-v3]", migrated.SupportedModels)
	}
}

func TestEnsureSQLiteDSN(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		dialect    string
		dsn        string
		disableWAL bool
		want       string
	}{
		{
			name:    "postgres unchanged",
			dialect: "postgres",
			dsn:     "postgres://localhost/axonhub",
			want:    "postgres://localhost/axonhub",
		},
		{
			name:    "sqlite adds wal and busy timeout",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db?cache=shared&_fk=1",
			want:    "file:axonhub.db?cache=shared&_fk=1&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		},
		{
			name:    "sqlite without query params",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db",
			want:    "file:axonhub.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)",
		},
		{
			name:       "wal disabled still adds busy timeout",
			dialect:    "sqlite3",
			dsn:        "file:axonhub.db",
			disableWAL: true,
			want:       "file:axonhub.db?_pragma=busy_timeout(5000)",
		},
		{
			name:    "existing wal preserved",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db?_pragma=journal_mode(DELETE)",
			want:    "file:axonhub.db?_pragma=journal_mode(DELETE)&_pragma=busy_timeout(5000)",
		},
		{
			name:    "existing busy timeout preserved",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db?_pragma=busy_timeout(10000)",
			want:    "file:axonhub.db?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)",
		},
		{
			name:    "both pragmas preserved",
			dialect: "sqlite3",
			dsn:     "file:axonhub.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)",
			want:    "file:axonhub.db?_pragma=journal_mode(WAL)&_pragma=busy_timeout(10000)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ensureSQLiteDSN(tt.dialect, tt.dsn, tt.disableWAL)
			if got != tt.want {
				t.Fatalf("ensureSQLiteDSN() = %q, want %q", got, tt.want)
			}
		})
	}
}
