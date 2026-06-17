package sqlserver

import (
	"context"
	"fmt"
	"strings"
)

// tableExists verifies that the named table exists in the SQL Server instance.
//
// table must be in "schema.name" format. Uses parameterised metadata queries —
// the table name is never interpolated into SQL strings.
func (c *Connector) tableExists(ctx context.Context, table string) error {
	schema, name, err := splitTable(table)
	if err != nil {
		return connErr(ErrInvalidConfig, fmt.Sprintf("invalid table %q: %v", table, err), err)
	}
	return tableExistsQ(ctx, c.q, c.cfg.Database, schema, name)
}

// tableExistsQ is the testable core of tableExists.
func tableExistsQ(ctx context.Context, q querier, database, schema, name string) error {
	const qry = `SELECT COUNT(1) FROM INFORMATION_SCHEMA.TABLES
	             WHERE TABLE_SCHEMA = @schema AND TABLE_NAME = @name`
	var count int
	if err := q.QueryRowContext(ctx, qry,
		namedArg("schema", schema),
		namedArg("name", name),
	).Scan(&count); err != nil {
		return connErr(ErrConnection, "query INFORMATION_SCHEMA.TABLES", err)
	}
	if count == 0 {
		return connErr(ErrMissingTable,
			fmt.Sprintf("table %q.%q not found in database %q", schema, name, database), nil)
	}
	return nil
}

// detectMechanism checks whether CDC or Change Tracking is enabled for the database.
//
// CDC is preferred: if it is enabled at the database level, mechanismCDC is returned.
// If CDC is not available but CT is enabled, mechanismCT is returned.
// If neither is enabled, an error is returned.
func detectMechanism(ctx context.Context, q querier, database string) (changeMechanism, error) {
	cdcEnabled, err := isCDCEnabled(ctx, q, database)
	if err != nil {
		return mechanismUnknown, err
	}
	if cdcEnabled {
		return mechanismCDC, nil
	}

	ctEnabled, err := isCTEnabled(ctx, q, database)
	if err != nil {
		return mechanismUnknown, err
	}
	if ctEnabled {
		return mechanismCT, nil
	}

	return mechanismUnknown, connErr(ErrNoChangeMechanism,
		fmt.Sprintf("database %q has neither CDC nor Change Tracking enabled; "+
			"enable one before starting the connector", database), nil)
}

// isCDCEnabled reports whether CDC is enabled at the database level.
func isCDCEnabled(ctx context.Context, q querier, database string) (bool, error) {
	var enabled bool
	const qry = `SELECT is_cdc_enabled FROM sys.databases WHERE name = @db`
	if err := q.QueryRowContext(ctx, qry, namedArg("db", database)).Scan(&enabled); err != nil {
		return false, connErr(ErrConnection, "query sys.databases for CDC status", err)
	}
	return enabled, nil
}

// isCTEnabled reports whether Change Tracking is enabled for the database.
func isCTEnabled(ctx context.Context, q querier, database string) (bool, error) {
	var count int
	const qry = `SELECT COUNT(1) FROM sys.change_tracking_databases WHERE database_id = DB_ID(@db)`
	if err := q.QueryRowContext(ctx, qry, namedArg("db", database)).Scan(&count); err != nil {
		return false, connErr(ErrConnection, "query sys.change_tracking_databases", err)
	}
	return count > 0, nil
}

// splitTable splits "schema.table" into its two parts.
func splitTable(table string) (schema, name string, err error) {
	parts := strings.SplitN(table, ".", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("expected schema.table format")
	}
	return parts[0], parts[1], nil
}

// Probe verifies the connection, validates that all configured tables exist, and
// detects the available change mechanism.
func (c *Connector) Probe(ctx context.Context) error {
	for _, table := range c.cfg.Tables {
		if err := c.tableExists(ctx, table); err != nil {
			return err
		}
	}

	mech, err := detectMechanism(ctx, c.q, c.cfg.Database)
	if err != nil {
		return err
	}
	c.mechanism = mech

	mechName := "CDC"
	if mech == mechanismCT {
		mechName = "Change Tracking"
	}
	c.log.Info("sqlserver: probe succeeded",
		"host", c.cfg.Host, "database", c.cfg.Database, "mechanism", mechName)
	return nil
}
