package db

import (
	"context"
	"database/sql"
	"fmt"

	"entgo.io/ent/dialect"

	"github.com/looplj/axonhub/internal/ent/channel"
)

// migrateLegacyChannelTypes normalizes removed channel types before Ent contracts
// the database enum during schema migration.
func migrateLegacyChannelTypes(ctx context.Context, dbDialect string, db *sql.DB) error {
	exists, err := channelsTableExists(ctx, dbDialect, db)
	if err != nil {
		return fmt.Errorf("check channels table before legacy type migration: %w", err)
	}
	if !exists {
		return nil
	}

	query := "UPDATE channels SET type = ? WHERE type = ?"
	if dbDialect == dialect.Postgres {
		query = "UPDATE channels SET type = $1 WHERE type = $2"
	}

	if _, err := db.ExecContext(
		ctx,
		query,
		channel.TypeOpenai.String(),
		channel.LegacyTypeAtlascloud.String(),
	); err != nil {
		return fmt.Errorf("normalize legacy channel types: %w", err)
	}

	return nil
}

func channelsTableExists(ctx context.Context, dbDialect string, db *sql.DB) (bool, error) {
	var query string

	switch dbDialect {
	case dialect.SQLite:
		query = "SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'channels'"
	case dialect.MySQL:
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = DATABASE() AND table_name = 'channels'"
	case dialect.Postgres:
		query = "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = current_schema() AND table_name = 'channels'"
	default:
		return false, fmt.Errorf("unsupported database dialect %q", dbDialect)
	}

	var count int
	if err := db.QueryRowContext(ctx, query).Scan(&count); err != nil {
		return false, err
	}

	return count > 0, nil
}
