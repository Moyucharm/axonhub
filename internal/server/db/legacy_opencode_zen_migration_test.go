package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"entgo.io/ent/dialect"
	"github.com/stretchr/testify/require"

	"github.com/looplj/axonhub/internal/authz"
	"github.com/looplj/axonhub/internal/ent/channel"
	"github.com/looplj/axonhub/internal/objects"
)

func TestMigrateLegacyOpenCodeZen(t *testing.T) {
	for _, tc := range []struct{ name, credentials, wantKey string }{
		{"anonymous", `{}`, "public"},
		{"single legacy key", `{"apiKey":"user-key"}`, "user-key"},
		{"configured keys", `{"mode":"single","apiKeys":["user-key"],"customCredential":{"preserve":true}}`, "user-key"},
		{"blank keys", `{"apiKeys":[" "]}`, "public"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "migration.db"))
			require.NoError(t, err)
			defer db.Close()
			_, err = db.Exec(`CREATE TABLE channels (id INTEGER PRIMARY KEY,type TEXT,settings TEXT,credentials TEXT,supported_models TEXT,endpoints TEXT,name TEXT,base_url TEXT,status TEXT,deleted_at TEXT)`)
			require.NoError(t, err)
			const settings = `{"modelProtocols":[{"model":"model-a","apiFormats":["anthropic/messages"],"enabled":false},{"model":"alias","apiFormats":["openai/responses"]}],"passThroughBody":true,"customSetting":{"preserve":true}}`
			const endpoints = `[{"api_format":"anthropic/messages","path":"/messages"},{"api_format":"openai/chat_completions","path":"/chat/completions","base_url":"https://custom.example/v1"},{"api_format":"openai/responses"}]`
			for _, typ := range []string{"opencode_zen", "openai", "opencode_go"} {
				_, err = db.Exec(`INSERT INTO channels(type,settings,credentials,supported_models,endpoints,name,base_url,status,deleted_at) VALUES (?,?,?,?,?,?,?,?,?)`, typ, settings, tc.credentials, `["model-a","model-b"]`, endpoints, typ, "https://opencode.ai/zen/v1", "disabled", "2026-01-01")
				require.NoError(t, err)
			}
			ctx := context.Background()
			require.NoError(t, migrateLegacyChannelTypes(ctx, dialect.SQLite, db))
			var typ, name, baseURL, status, deleted, settingsAfter, credentialsAfter, endpointsAfter string
			require.NoError(t, db.QueryRow(`SELECT type,name,base_url,status,deleted_at,settings,credentials,endpoints FROM channels WHERE id=1`).Scan(&typ, &name, &baseURL, &status, &deleted, &settingsAfter, &credentialsAfter, &endpointsAfter))
			require.Equal(t, "openai", typ)
			require.Equal(t, "opencode_zen", name)
			require.Equal(t, "https://opencode.ai/zen/v1", baseURL)
			require.Equal(t, "disabled", status)
			require.Equal(t, "2026-01-01", deleted)
			var settingsValue map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(settingsAfter), &settingsValue))
			require.JSONEq(t, `{"preserve":true}`, string(settingsValue["customSetting"]))
			require.Equal(t, "true", string(settingsValue["passThroughBody"]))
			var protocols []objects.ModelProtocol
			require.NoError(t, json.Unmarshal(settingsValue["modelProtocols"], &protocols))
			require.Len(t, protocols, 3)
			for _, protocol := range protocols {
				require.Equal(t, []string{"openai/chat_completions"}, protocol.APIFormats)
				require.True(t, protocol.IsEnabled())
			}
			var credentials objects.ChannelCredentials
			require.NoError(t, json.Unmarshal([]byte(credentialsAfter), &credentials))
			require.Equal(t, []string{tc.wantKey}, credentials.APIKeys)
			if tc.name == "configured keys" {
				var extra map[string]json.RawMessage
				require.NoError(t, json.Unmarshal([]byte(credentialsAfter), &extra))
				require.JSONEq(t, `{"preserve":true}`, string(extra["customCredential"]))
			}
			require.JSONEq(t, `[{"api_format":"openai/chat_completions","path":"/chat/completions","base_url":"https://custom.example/v1"}]`, endpointsAfter)
			for id := 2; id <= 3; id++ {
				var unchanged string
				require.NoError(t, db.QueryRow(`SELECT settings FROM channels WHERE id=?`, id).Scan(&unchanged))
				require.Equal(t, settings, unchanged)
			}
			// A restart is a no-op, including a user edit made after conversion.
			_, err = db.Exec(`UPDATE channels SET settings='{}' WHERE id=1`)
			require.NoError(t, err)
			require.NoError(t, migrateLegacyChannelTypes(ctx, dialect.SQLite, db))
			var repeated string
			require.NoError(t, db.QueryRow(`SELECT settings FROM channels WHERE id=1`).Scan(&repeated))
			require.Equal(t, "{}", repeated)
		})
	}
}

func TestMigrateLegacyOpenCodeZenRollsBack(t *testing.T) {
	db, err := sql.Open("sqlite3", "file:"+filepath.Join(t.TempDir(), "rollback.db"))
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE channels (id INTEGER PRIMARY KEY,type TEXT,settings TEXT,credentials TEXT,supported_models TEXT,endpoints TEXT)`)
	require.NoError(t, err)
	for _, settings := range []string{`{}`, `invalid JSON`} {
		_, err = db.Exec(`INSERT INTO channels(type,settings,credentials,supported_models,endpoints) VALUES ('opencode_zen',?,'{}','["model"]','[]')`, settings)
		require.NoError(t, err)
	}
	require.Error(t, migrateLegacyChannelTypes(context.Background(), dialect.SQLite, db))
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channels WHERE type='opencode_zen'`).Scan(&count))
	require.Equal(t, 2, count)

	// A failure after the first UPDATE must roll back that row as well.
	_, err = db.Exec(`UPDATE channels SET settings='{}'; CREATE TRIGGER reject_second_zen BEFORE UPDATE ON channels WHEN OLD.id=2 BEGIN SELECT RAISE(ABORT, 'injected update failure'); END`)
	require.NoError(t, err)
	require.Error(t, migrateLegacyChannelTypes(context.Background(), dialect.SQLite, db))
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM channels WHERE type='opencode_zen'`).Scan(&count))
	require.Equal(t, 2, count)
}

func TestNewEntClientMigratesOpenCodeZenBeforeSchema(t *testing.T) {
	dsn := "file:" + filepath.Join(t.TempDir(), "legacy-zen.db") + "?_fk=0"
	client := NewEntClient(Config{Dialect: "sqlite3", DSN: dsn})
	ctx := authz.WithTestBypass(context.Background())
	row, err := client.Channel.Create().SetType(channel.TypeOpenai).SetName("Legacy Zen").SetBaseURL("https://opencode.ai/zen/v1").SetCredentials(objects.ChannelCredentials{}).SetSupportedModels([]string{"model"}).SetDefaultTestModel("model").SetSettings(&objects.ChannelSettings{ModelProtocols: []objects.ModelProtocol{{Model: "model", APIFormats: []string{"anthropic/messages"}}}}).SetEndpoints([]objects.ChannelEndpoint{{APIFormat: "anthropic/messages"}}).Save(ctx)
	require.NoError(t, err)
	require.NoError(t, client.Close())
	db, err := sql.Open("sqlite3", dsn)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE channels SET type='opencode_zen' WHERE id=?`, row.ID)
	require.NoError(t, err)
	require.NoError(t, db.Close())
	migrated := NewEntClient(Config{Dialect: "sqlite3", DSN: dsn})
	defer migrated.Close()
	result, err := migrated.Channel.Get(ctx, row.ID)
	require.NoError(t, err)
	require.Equal(t, channel.TypeOpenai, result.Type)
	require.Equal(t, []string{"public"}, result.Credentials.APIKeys)
	require.Equal(t, []objects.ChannelEndpoint{{APIFormat: "openai/chat_completions"}}, result.Endpoints)
	require.Len(t, result.Settings.ModelProtocols, 1)
	require.Equal(t, []string{"openai/chat_completions"}, result.Settings.ModelProtocols[0].APIFormats)
	require.Error(t, channel.TypeValidator(channel.LegacyTypeOpencodeZen))
}
