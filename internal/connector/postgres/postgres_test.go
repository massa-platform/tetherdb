package postgres

import (
	"context"
	"errors"
	"log/slog"
	"testing"

	"github.com/massa-platform/tetherdb/internal/connector"
)

// fakePool implements dbPool without a real database.
type fakePool struct {
	pingErr      error
	txErr        error   // error returned by Begin
	execErrs     []error // per-Exec errors, in call order
	execSQL      []string
	execArgs     [][]any
	rowsAffected int64
	lastTx       *fakeTx
}

func (f *fakePool) Ping(_ context.Context) error { return f.pingErr }

func (f *fakePool) Begin(_ context.Context) (dbTx, error) {
	if f.txErr != nil {
		return nil, f.txErr
	}
	tx := &fakeTx{pool: f}
	f.lastTx = tx
	return tx, nil
}

func (f *fakePool) Close() {}

// fakeTx implements dbTx for testing.
type fakeTx struct {
	pool       *fakePool
	callCount  int
	committed  bool
	rolledBack bool
}

func (t *fakeTx) Exec(_ context.Context, sql string, args ...any) (int64, error) {
	t.pool.execSQL = append(t.pool.execSQL, sql)
	t.pool.execArgs = append(t.pool.execArgs, args)
	idx := t.callCount
	t.callCount++
	if idx < len(t.pool.execErrs) && t.pool.execErrs[idx] != nil {
		return 0, t.pool.execErrs[idx]
	}
	return t.pool.rowsAffected, nil
}

func (t *fakeTx) Commit(_ context.Context) error {
	t.committed = true
	return nil
}

func (t *fakeTx) Rollback(_ context.Context) error {
	t.rolledBack = true
	return nil
}

// newTestConnector builds a Connector backed by a fakePool.
func newTestConnector(pool *fakePool) *Connector {
	return &Connector{
		pool: pool,
		cfg:  Config{Host: "localhost", Port: 5432, Database: "test", Username: "u", Password: "p"},
		log:  slog.Default(),
	}
}

// --- Probe tests ---

func TestProbe_Success(t *testing.T) {
	pool := &fakePool{}
	c := newTestConnector(pool)
	if err := c.Probe(context.Background()); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

func TestProbe_ConnectionFailure(t *testing.T) {
	pool := &fakePool{pingErr: errors.New("connection refused")}
	c := newTestConnector(pool)
	err := c.Probe(context.Background())
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var ce *ConnectorError
	if !errors.As(err, &ce) {
		t.Fatalf("expected *ConnectorError, got %T", err)
	}
	if ce.Kind != ErrConnection {
		t.Fatalf("expected ErrConnection, got %v", ce.Kind)
	}
}

// --- Apply tests ---

func TestApply_Insert(t *testing.T) {
	pool := &fakePool{rowsAffected: 1}
	c := newTestConnector(pool)

	batch := []connector.Change{{
		Schema: "public",
		Table:  "orders",
		Op:     connector.Insert,
		PK:     map[string]any{"id": 1},
		After:  map[string]any{"id": 1, "name": "foo"},
	}}

	if err := c.Apply(context.Background(), batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pool.execSQL) != 1 {
		t.Fatalf("expected 1 SQL statement, got %d", len(pool.execSQL))
	}
	sql := pool.execSQL[0]
	if !containsAll(sql, "INSERT INTO", `"public"."orders"`, "ON CONFLICT", "DO UPDATE SET") {
		t.Errorf("upsert SQL missing expected clauses: %s", sql)
	}
}

func TestApply_Update(t *testing.T) {
	pool := &fakePool{rowsAffected: 1}
	c := newTestConnector(pool)

	batch := []connector.Change{{
		Schema: "public",
		Table:  "orders",
		Op:     connector.Update,
		PK:     map[string]any{"id": 1},
		After:  map[string]any{"id": 1, "name": "updated"},
	}}

	if err := c.Apply(context.Background(), batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pool.execSQL) != 1 {
		t.Fatalf("expected 1 SQL, got %d", len(pool.execSQL))
	}
	// Update reuses the upsert path — same SQL structure as Insert.
	if !containsAll(pool.execSQL[0], "INSERT INTO", "ON CONFLICT", "DO UPDATE SET") {
		t.Errorf("update SQL missing upsert clauses: %s", pool.execSQL[0])
	}
}

func TestApply_Delete(t *testing.T) {
	pool := &fakePool{rowsAffected: 1}
	c := newTestConnector(pool)

	batch := []connector.Change{{
		Schema: "public",
		Table:  "orders",
		Op:     connector.Delete,
		PK:     map[string]any{"id": 1},
	}}

	if err := c.Apply(context.Background(), batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(pool.execSQL) != 1 {
		t.Fatalf("expected 1 SQL, got %d", len(pool.execSQL))
	}
	if !containsAll(pool.execSQL[0], "DELETE FROM", `"public"."orders"`, "WHERE") {
		t.Errorf("delete SQL missing expected clauses: %s", pool.execSQL[0])
	}
}

func TestApply_DeleteMissingRow(t *testing.T) {
	// rowsAffected=0 → no-op, not an error.
	pool := &fakePool{rowsAffected: 0}
	c := newTestConnector(pool)

	batch := []connector.Change{{
		Schema: "public",
		Table:  "orders",
		Op:     connector.Delete,
		PK:     map[string]any{"id": 999},
	}}

	if err := c.Apply(context.Background(), batch); err != nil {
		t.Fatalf("expected nil for delete of missing row, got %v", err)
	}
}

func TestApply_BatchTransaction(t *testing.T) {
	pool := &fakePool{rowsAffected: 1}
	c := newTestConnector(pool)

	batch := []connector.Change{
		{Schema: "public", Table: "t", Op: connector.Insert, PK: map[string]any{"id": 1}, After: map[string]any{"id": 1}},
		{Schema: "public", Table: "t", Op: connector.Delete, PK: map[string]any{"id": 2}},
	}

	if err := c.Apply(context.Background(), batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Both changes executed within one transaction (one Begin call → two Exec calls).
	if len(pool.execSQL) != 2 {
		t.Fatalf("expected 2 SQL statements, got %d", len(pool.execSQL))
	}
}

func TestApply_RollbackOnError(t *testing.T) {
	execErr := errors.New("FK violation")
	pool := &fakePool{
		execErrs:     []error{nil, execErr},
		rowsAffected: 1,
	}
	c := newTestConnector(pool)

	batch := []connector.Change{
		{Schema: "public", Table: "t", Op: connector.Insert, PK: map[string]any{"id": 1}, After: map[string]any{"id": 1}},
		{Schema: "public", Table: "t", Op: connector.Delete, PK: map[string]any{"id": 2}},
	}

	err := c.Apply(context.Background(), batch)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if pool.lastTx == nil || !pool.lastTx.rolledBack {
		t.Error("expected transaction to be rolled back")
	}
}

func TestApply_CompositePK(t *testing.T) {
	pool := &fakePool{rowsAffected: 1}
	c := newTestConnector(pool)

	batch := []connector.Change{{
		Schema: "public",
		Table:  "order_items",
		Op:     connector.Delete,
		PK:     map[string]any{"order_id": 1, "item_id": 2},
	}}

	if err := c.Apply(context.Background(), batch); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sql := pool.execSQL[0]
	// Both PK columns must appear in the WHERE clause.
	if !containsAll(sql, `"order_id"`, `"item_id"`) {
		t.Errorf("composite PK WHERE clause missing columns: %s", sql)
	}
}

func TestApply_SpecialCharsInPassword(t *testing.T) {
	cfg := Config{
		Host:     "pg.internal",
		Port:     5432,
		Database: "mydb",
		Username: "user",
		Password: "p@ss!word#123",
		SSLMode:  "require",
	}
	dsn := buildDSN(cfg)
	if dsn == "" {
		t.Fatal("expected non-empty DSN")
	}
	// The URL must not contain the raw special characters unencoded.
	if containsAll(dsn, "p@ss!word#123") {
		t.Errorf("DSN contains unencoded special chars: %q", dsn)
	}
}

func TestConfig_ExplicitFields(t *testing.T) {
	cfg := Config{
		Host:     "pg.internal",
		Port:     5432,
		Database: "erp_mirror",
		Username: "sync_user",
		Password: "secret",
		SSLMode:  "require",
	}
	dsn := buildDSN(cfg)
	if !containsAll(dsn, "pg.internal", "erp_mirror") {
		t.Errorf("DSN missing expected host/db: %q", dsn)
	}
}

func TestConfig_RawDSN(t *testing.T) {
	raw := "postgres://user:pass@pg.internal:5432/erp_mirror?sslmode=require"
	cfg := Config{DSN: raw}
	dsn := buildDSN(cfg)
	if dsn != raw {
		t.Errorf("expected raw DSN %q, got %q", raw, dsn)
	}
}

// containsAll returns true if s contains all of the given substrings.
func containsAll(s string, subs ...string) bool {
	for _, sub := range subs {
		if !containsStr(s, sub) {
			return false
		}
	}
	return true
}
