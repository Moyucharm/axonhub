package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"entgo.io/ent/dialect"
)

// migrateLegacyOpenCodeZen runs before Ent removes the legacy enum value. Keep
// each row's type, credentials and protocol settings atomic and preserve unknown
// JSON settings so upgrades do not discard unrelated configuration.
func migrateLegacyOpenCodeZen(ctx context.Context, dbDialect string, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin OpenCode Zen migration: %w", err)
	}
	defer tx.Rollback()
	query := "SELECT id, settings, credentials, supported_models, endpoints FROM channels WHERE type = ?"
	update := "UPDATE channels SET type = ?, settings = ?, credentials = ?, endpoints = ? WHERE id = ? AND type = ?"
	if dbDialect == dialect.Postgres {
		query = "SELECT id, settings, credentials, supported_models, endpoints FROM channels WHERE type = $1"
		update = "UPDATE channels SET type = $1, settings = $2, credentials = $3, endpoints = $4 WHERE id = $5 AND type = $6"
	}
	rows, err := tx.QueryContext(ctx, query, "opencode_zen")
	if err != nil {
		return fmt.Errorf("query OpenCode Zen channels: %w", err)
	}
	type migratedRow struct {
		id                               int
		settings, credentials, endpoints string
	}
	var updates []migratedRow
	for rows.Next() {
		var id int
		var settingsJSON, credentialsJSON, modelsJSON, endpointsJSON []byte
		if err := rows.Scan(&id, &settingsJSON, &credentialsJSON, &modelsJSON, &endpointsJSON); err != nil {
			rows.Close()
			return fmt.Errorf("read OpenCode Zen channel: %w", err)
		}
		settings := make(map[string]json.RawMessage)
		credentials := make(map[string]json.RawMessage)
		var models []string
		var protocols []struct {
			Model string `json:"model"`
		}
		var endpoints []map[string]json.RawMessage
		for _, value := range []struct {
			data   []byte
			target any
		}{{settingsJSON, &settings}, {credentialsJSON, &credentials}, {modelsJSON, &models}, {endpointsJSON, &endpoints}} {
			if len(value.data) != 0 && string(value.data) != "null" {
				if err := json.Unmarshal(value.data, value.target); err != nil {
					rows.Close()
					return fmt.Errorf("decode OpenCode Zen channel %d configuration: %w", id, err)
				}
			}
		}
		if raw := settings["modelProtocols"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &protocols); err != nil {
				rows.Close()
				return fmt.Errorf("decode OpenCode Zen channel %d protocols: %w", id, err)
			}
		}
		for _, protocol := range protocols {
			models = append(models, protocol.Model)
		}
		seen := make(map[string]bool)
		chatProtocols := make([]map[string]any, 0, len(models))
		for _, model := range models {
			if model == "" || seen[model] {
				continue
			}
			seen[model] = true
			chatProtocols = append(chatProtocols, map[string]any{"model": model, "apiFormats": []string{"openai/chat_completions"}, "enabled": true})
		}
		settings["modelProtocols"], _ = json.Marshal(chatProtocols)
		var apiKey string
		var apiKeys []string
		if raw := credentials["apiKey"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &apiKey); err != nil {
				rows.Close()
				return fmt.Errorf("decode OpenCode Zen channel %d key: %w", id, err)
			}
		}
		if raw := credentials["apiKeys"]; len(raw) != 0 {
			if err := json.Unmarshal(raw, &apiKeys); err != nil {
				rows.Close()
				return fmt.Errorf("decode OpenCode Zen channel %d keys: %w", id, err)
			}
		}
		// The old transformer used public only when no configured key existed. Do
		// not reactivate keys disabled by operational state during migration.
		hasKeys := false
		for _, key := range apiKeys {
			hasKeys = hasKeys || strings.TrimSpace(key) != ""
		}
		if !hasKeys {
			key := strings.TrimSpace(apiKey)
			if key == "" {
				key = "public"
			}
			// Regular OpenAI editors read apiKeys; carry the legacy single key
			// there as well so editing a migrated channel echoes its credential.
			credentials["apiKeys"], _ = json.Marshal([]string{key})
		}
		chatEndpoint := map[string]json.RawMessage{"api_format": json.RawMessage(`"openai/chat_completions"`)}
		for _, endpoint := range endpoints {
			var format string
			if err := json.Unmarshal(endpoint["api_format"], &format); err != nil {
				rows.Close()
				return fmt.Errorf("decode OpenCode Zen channel %d endpoint: %w", id, err)
			}
			if format == "openai/chat_completions" {
				chatEndpoint = endpoint
				delete(chatEndpoint, "transport")
				break
			}
		}
		settingsBytes, err := json.Marshal(settings)
		if err != nil {
			rows.Close()
			return err
		}
		credentialsBytes, err := json.Marshal(credentials)
		if err != nil {
			rows.Close()
			return err
		}
		endpointBytes, err := json.Marshal([]map[string]json.RawMessage{chatEndpoint})
		if err != nil {
			rows.Close()
			return err
		}
		updates = append(updates, migratedRow{id, string(settingsBytes), string(credentialsBytes), string(endpointBytes)})
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("iterate OpenCode Zen channels: %w", err)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, row := range updates {
		if _, err := tx.ExecContext(ctx, update, "openai", row.settings, row.credentials, row.endpoints, row.id, "opencode_zen"); err != nil {
			return fmt.Errorf("migrate OpenCode Zen channel %d: %w", row.id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit OpenCode Zen migration: %w", err)
	}
	return nil
}
