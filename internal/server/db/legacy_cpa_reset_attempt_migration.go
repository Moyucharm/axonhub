package db

import (
	"context"
	"database/sql"
	"fmt"
)

// migrateLegacyCPACodexResetAttempts moves Codex reset claims from the retired
// cpa_codex_reset_attempts table into cpa_reset_attempts, which Ent has just
// created. Claims must survive the move: a missing row would let an already
// consumed or uncertain card be offered and consumed again. The copy and the
// drop share one transaction, so a failure leaves the legacy table in place
// and the next start retries.
func migrateLegacyCPACodexResetAttempts(ctx context.Context, dbDialect string, db *sql.DB) error {
	exists, err := tableExists(ctx, dbDialect, db, "cpa_codex_reset_attempts")
	if err != nil {
		return fmt.Errorf("check legacy CPA Codex reset attempts table: %w", err)
	}
	if !exists {
		return nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin legacy CPA Codex reset attempts migration: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// credit_key is unique in both tables; skipping keys already present keeps a
	// retried migration from failing on rows copied by an earlier attempt.
	if _, err := tx.ExecContext(ctx, `
INSERT INTO cpa_reset_attempts (created_at, updated_at, provider, credit_key, credential_id, state, request_id, grant_id)
SELECT legacy.created_at, legacy.updated_at, 'codex', legacy.credit_key, legacy.credential_id, legacy.state, '', ''
FROM cpa_codex_reset_attempts legacy
WHERE NOT EXISTS (SELECT 1 FROM cpa_reset_attempts current WHERE current.credit_key = legacy.credit_key)`); err != nil {
		return fmt.Errorf("copy legacy CPA Codex reset attempts: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DROP TABLE cpa_codex_reset_attempts"); err != nil {
		return fmt.Errorf("drop legacy CPA Codex reset attempts table: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit legacy CPA Codex reset attempts migration: %w", err)
	}
	return nil
}
