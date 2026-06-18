package postgres

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/massa-platform/tetherdb/internal/connector"
)

// applyBatch executes each change in batch against tx in order.
//
// Returns the first error encountered — the caller must roll back on any error.
func applyBatch(ctx context.Context, tx dbTx, batch []connector.Change) error {
	for i, ch := range batch {
		var err error
		switch ch.Op {
		case connector.Insert, connector.Update:
			err = execUpsert(ctx, tx, ch)
		case connector.Delete:
			err = execDelete(ctx, tx, ch)
		default:
			return fmt.Errorf("change[%d]: unknown op %d", i, ch.Op)
		}
		if err != nil {
			return fmt.Errorf("change[%d] %s.%s op=%d: %w", i, ch.Schema, ch.Table, ch.Op, err)
		}
	}
	return nil
}

// execUpsert runs an INSERT ... ON CONFLICT DO UPDATE SET for a single change.
//
// Column names from Change.After are quoted with quoteIdent to prevent SQL injection.
// PK columns from Change.PK define the conflict target.
func execUpsert(ctx context.Context, tx dbTx, ch connector.Change) error {
	// Sort column names for deterministic SQL (required for test assertions).
	cols := sortedKeys(ch.After)
	pkCols := sortedKeys(ch.PK)

	// Build $1, $2, ... placeholders and collect values in column order.
	placeholders := make([]string, len(cols))
	vals := make([]any, len(cols))
	for i, col := range cols {
		placeholders[i] = fmt.Sprintf("$%d", i+1)
		vals[i] = ch.After[col]
	}

	// Build SET list for the DO UPDATE clause, excluding PK columns.
	pkSet := make(map[string]bool, len(pkCols))
	for _, pk := range pkCols {
		pkSet[pk] = true
	}
	var setClauses []string
	for _, col := range cols {
		if !pkSet[col] {
			setClauses = append(setClauses, fmt.Sprintf("%s = EXCLUDED.%s", quoteIdent(col), quoteIdent(col)))
		}
	}

	// Build quoted conflict target.
	quotedPKs := make([]string, len(pkCols))
	for i, pk := range pkCols {
		quotedPKs[i] = quoteIdent(pk)
	}

	// Build quoted column list.
	quotedCols := make([]string, len(cols))
	for i, col := range cols {
		quotedCols[i] = quoteIdent(col)
	}

	table := quoteTable(ch.Schema, ch.Table)

	var sql string
	if len(setClauses) == 0 {
		// All columns are PK columns — use DO NOTHING to preserve idempotency.
		sql = fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO NOTHING",
			table,
			strings.Join(quotedCols, ", "),
			strings.Join(placeholders, ", "),
			strings.Join(quotedPKs, ", "),
		)
	} else {
		sql = fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s) ON CONFLICT (%s) DO UPDATE SET %s",
			table,
			strings.Join(quotedCols, ", "),
			strings.Join(placeholders, ", "),
			strings.Join(quotedPKs, ", "),
			strings.Join(setClauses, ", "),
		)
	}

	_, err := tx.Exec(ctx, sql, vals...)
	return err
}

// execDelete runs a DELETE FROM ... WHERE for a single change.
//
// Missing rows are treated as a no-op — this is intentional for idempotency.
func execDelete(ctx context.Context, tx dbTx, ch connector.Change) error {
	pkCols := sortedKeys(ch.PK)

	whereClauses := make([]string, len(pkCols))
	vals := make([]any, len(pkCols))
	for i, col := range pkCols {
		whereClauses[i] = fmt.Sprintf("%s = $%d", quoteIdent(col), i+1)
		vals[i] = ch.PK[col]
	}

	sql := fmt.Sprintf(
		"DELETE FROM %s WHERE %s",
		quoteTable(ch.Schema, ch.Table),
		strings.Join(whereClauses, " AND "),
	)

	// Ignore rows-affected count — 0 rows deleted means the row was already gone.
	_, err := tx.Exec(ctx, sql, vals...)
	return err
}

// quoteIdent quotes a single Postgres identifier (column or table name).
//
// Double-quotes are doubled per the SQL standard to prevent injection.
func quoteIdent(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

// quoteTable returns a fully-qualified quoted table reference.
func quoteTable(schema, table string) string {
	if schema == "" {
		return quoteIdent(table)
	}
	return quoteIdent(schema) + "." + quoteIdent(table)
}

// sortedKeys returns the keys of m sorted alphabetically.
func sortedKeys(m map[string]any) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
