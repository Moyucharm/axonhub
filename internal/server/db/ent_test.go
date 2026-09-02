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

	tests := []struct {
		id       int
		input    string
		expected string
		name     string
		baseURL  string
	}{
		{id: 1, input: "atlascloud", expected: "openai", name: "legacy-atlascloud", baseURL: "https://api.atlascloud.ai/v1"},
		{id: 2, input: "qiniu", expected: "openai", name: "legacy-qiniu", baseURL: "https://api.qnaigc.com/v1"},
		{id: 3, input: "qiniu_anthropic", expected: "anthropic", name: "legacy-qiniu-anthropic", baseURL: "https://api.qnaigc.com"},
		{id: 4, input: "fenno", expected: "openai_responses", name: "legacy-fenno", baseURL: "https://api.fenno.ai"},
		{id: 5, input: "openai", expected: "openai", name: "existing", baseURL: "https://api.openai.com/v1"},
	}
	for _, tt := range tests {
		if _, err := db.Exec(
			`INSERT INTO channels (id, type, name, base_url) VALUES (?, ?, ?, ?)`,
			tt.id, tt.input, tt.name, tt.baseURL,
		); err != nil {
			t.Fatal(err)
		}
	}

	ctx := context.Background()
	if err := migrateLegacyChannelTypes(ctx, dialect.SQLite, db); err != nil {
		t.Fatalf("first migrateLegacyChannelTypes() error = %v", err)
	}
	if err := migrateLegacyChannelTypes(ctx, dialect.SQLite, db); err != nil {
		t.Fatalf("second migrateLegacyChannelTypes() error = %v", err)
	}

	for _, tt := range tests {
		var channelType, name, baseURL string
		if err := db.QueryRow(`SELECT type, name, base_url FROM channels WHERE id = ?`, tt.id).Scan(&channelType, &name, &baseURL); err != nil {
			t.Fatal(err)
		}
		if channelType != tt.expected {
			t.Fatalf("channel %q type = %q, want %q", tt.name, channelType, tt.expected)
		}
		if name != tt.name || baseURL != tt.baseURL {
			t.Fatalf("channel %q configuration changed: name=%q base_url=%q", tt.name, name, baseURL)
		}
	}
}

func TestNewEntClientMigratesLegacyChannelTypesBeforeSchemaMigration(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "legacy-channel.db") + "?_fk=0"
	client := NewEntClient(Config{Dialect: "sqlite3", DSN: dsn})
	ctx := authz.WithTestBypass(context.Background())

	tests := []struct {
		name       string
		seededType channel.Type
		legacyType string
		expected   channel.Type
	}{
		{name: "legacy-atlascloud", seededType: channel.TypeOpenai, legacyType: "atlascloud", expected: channel.TypeOpenai},
		{name: "legacy-qiniu", seededType: channel.TypeOpenai, legacyType: "qiniu", expected: channel.TypeOpenai},
		{name: "legacy-qiniu-anthropic", seededType: channel.TypeAnthropic, legacyType: "qiniu_anthropic", expected: channel.TypeAnthropic},
		{name: "legacy-fenno", seededType: channel.TypeOpenaiResponses, legacyType: "fenno", expected: channel.TypeOpenaiResponses},
	}
	created := make([]struct {
		id         int
		name       string
		legacyType string
		expected   channel.Type
	}, 0, len(tests))
	for _, tt := range tests {
		createdChannel, err := client.Channel.Create().
			SetType(tt.seededType).
			SetName(tt.name).
			SetBaseURL("https://api.example.com/v1").
			SetCredentials(objects.ChannelCredentials{APIKey: "legacy-key"}).
			SetSupportedModels([]string{"test-model"}).
			SetDefaultTestModel("test-model").
			Save(ctx)
		if err != nil {
			client.Close()
			t.Fatal(err)
		}
		created = append(created, struct {
			id         int
			name       string
			legacyType string
			expected   channel.Type
		}{
			id:         createdChannel.ID,
			name:       tt.name,
			legacyType: tt.legacyType,
			expected:   tt.expected,
		})
	}
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}

	rawDB, err := sql.Open("sqlite3", dsn)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range created {
		if _, err := rawDB.Exec(`UPDATE channels SET type = ? WHERE id = ?`, tt.legacyType, tt.id); err != nil {
			rawDB.Close()
			t.Fatal(err)
		}
	}
	if err := rawDB.Close(); err != nil {
		t.Fatal(err)
	}

	migratedClient := NewEntClient(Config{Dialect: "sqlite3", DSN: dsn})
	defer migratedClient.Close()
	for _, tt := range created {
		migrated, err := migratedClient.Channel.Get(ctx, tt.id)
		if err != nil {
			t.Fatal(err)
		}
		if migrated.Type != tt.expected {
			t.Fatalf("migrated channel %q type = %q, want %q", tt.name, migrated.Type, tt.expected)
		}
		if migrated.BaseURL != "https://api.example.com/v1" || migrated.Credentials.APIKey != "legacy-key" {
			t.Fatalf("migrated channel %q configuration changed: base_url=%q api_key=%q", tt.name, migrated.BaseURL, migrated.Credentials.APIKey)
		}
		if len(migrated.SupportedModels) != 1 || migrated.SupportedModels[0] != "test-model" {
			t.Fatalf("migrated channel %q supported models = %v, want [test-model]", tt.name, migrated.SupportedModels)
		}
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
