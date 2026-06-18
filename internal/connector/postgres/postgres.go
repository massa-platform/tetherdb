package postgres

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/massa-platform/tetherdb/internal/connector"
)

// Config holds all connection parameters for a PostgreSQL sink connector.
//
// Either DSN or explicit fields must be provided. If DSN is non-empty it is
// used as-is; explicit fields take priority and DSN is ignored.
//
// No field may be hardcoded by the caller — values must come from a config
// file or environment variable.
type Config struct {
	// DSN is a raw postgres:// connection string. Used when explicit fields are empty.
	DSN string
	// Host is the Postgres hostname or IP. Required when DSN is empty.
	Host string
	// Port is the TCP port. Defaults to 5432 when zero.
	Port int
	// Database is the target database name. Required when DSN is empty.
	Database string
	// Username is the login name. Required when DSN is empty.
	Username string
	// Password is the login password. Never logged.
	Password string
	// SSLMode controls TLS behaviour. Defaults to "require" when empty.
	SSLMode string
}

// dbPool is the interface the Connector uses to interact with the connection pool.
// Extracted for testing — fakePool implements it without a real database.
type dbPool interface {
	Ping(ctx context.Context) error
	Begin(ctx context.Context) (dbTx, error)
	Close()
}

// dbTx is the interface the Connector uses to interact with a database transaction.
type dbTx interface {
	Exec(ctx context.Context, sql string, args ...any) (int64, error)
	Commit(ctx context.Context) error
	Rollback(ctx context.Context) error
}

// realPool wraps *pgxpool.Pool to satisfy dbPool.
type realPool struct {
	p *pgxpool.Pool
}

func (r *realPool) Ping(ctx context.Context) error {
	return r.p.Ping(ctx)
}

func (r *realPool) Begin(ctx context.Context) (dbTx, error) {
	tx, err := r.p.Begin(ctx)
	if err != nil {
		return nil, err
	}
	return &realTx{tx: tx}, nil
}

func (r *realPool) Close() {
	r.p.Close()
}

// realTx wraps pgx.Tx to satisfy dbTx.
type realTx struct {
	tx pgx.Tx
}

func (r *realTx) Exec(ctx context.Context, sql string, args ...any) (int64, error) {
	tag, err := r.tx.Exec(ctx, sql, args...)
	return tag.RowsAffected(), err
}

func (r *realTx) Commit(ctx context.Context) error   { return r.tx.Commit(ctx) }
func (r *realTx) Rollback(ctx context.Context) error { return r.tx.Rollback(ctx) }

// Connector implements connector.Writer for PostgreSQL.
//
// Create a Connector with New, then call Probe before using Apply.
type Connector struct {
	cfg  Config
	pool dbPool
	log  *slog.Logger
}

// New opens a PostgreSQL connection pool and returns a Connector.
//
// New validates Config before connecting. Call Probe to verify the connection
// is healthy before applying changes.
//
// Example:
//
//	c, err := postgres.New(ctx, postgres.Config{
//	    Host: "pg.internal", Port: 5432, Database: "erp_mirror",
//	    Username: "sync", Password: os.Getenv("PG_PASS"), SSLMode: "require",
//	})
func New(ctx context.Context, cfg Config) (*Connector, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	dsn := buildDSN(cfg)
	p, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, connErr(ErrConnection, "open pool", err)
	}

	// pgxpool.New is lazy — verify connectivity immediately.
	if err := p.Ping(ctx); err != nil {
		p.Close()
		return nil, classifyConnectError(err, cfg.Host)
	}

	slog.Default().Info("postgres: connected", "host", cfg.Host, "database", cfg.Database)

	return &Connector{
		cfg:  cfg,
		pool: &realPool{p: p},
		log:  slog.Default(),
	}, nil
}

// Close releases the underlying connection pool.
//
// Example:
//
//	defer conn.Close()
func (c *Connector) Close() error {
	c.pool.Close()
	return nil
}

// Probe verifies the database connection is reachable and credentials are valid.
//
// Returns a ConnectorError with kind=ErrConnection if the server is unreachable,
// or kind=ErrAuth if authentication fails.
//
// Example:
//
//	if err := c.Probe(ctx); err != nil { return err }
func (c *Connector) Probe(ctx context.Context) error {
	if err := c.pool.Ping(ctx); err != nil {
		return classifyConnectError(err, c.cfg.Host)
	}
	c.log.Info("postgres: probe succeeded", "host", c.cfg.Host, "database", c.cfg.Database)
	return nil
}

// Apply writes a batch of changes to Postgres inside a single transaction.
//
// All changes succeed or the transaction is rolled back and an error is returned.
// The source will not be ACKed on failure, so the same batch will be retried.
//
// Example:
//
//	if err := writer.Apply(ctx, batch); err != nil {
//	    return fmt.Errorf("apply: %w", err)
//	}
func (c *Connector) Apply(ctx context.Context, batch []connector.Change) error {
	if len(batch) == 0 {
		return nil
	}

	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return connErr(ErrConnection, "begin transaction", err)
	}

	if err := applyBatch(ctx, tx, batch); err != nil {
		// Rollback is best-effort — the server aborts the transaction regardless
		// if the connection drops.
		_ = tx.Rollback(ctx)
		return fmt.Errorf("postgres: apply batch: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return connErr(ErrConnection, "commit transaction", err)
	}
	return nil
}

// validateConfig checks required fields when DSN is not provided directly.
func validateConfig(cfg Config) error {
	if cfg.DSN != "" {
		return nil
	}
	if cfg.Host == "" {
		return connErr(ErrInvalidConfig, "host must not be empty", nil)
	}
	if cfg.Database == "" {
		return connErr(ErrInvalidConfig, "database must not be empty", nil)
	}
	if cfg.Username == "" {
		return connErr(ErrInvalidConfig, "username must not be empty", nil)
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}
	if port < 1 || port > 65535 {
		return connErr(ErrInvalidConfig, fmt.Sprintf("port %d out of range 1-65535", port), nil)
	}
	return nil
}

// buildDSN constructs the postgres:// DSN from Config.
//
// If cfg.DSN is set it is returned directly. Otherwise a DSN is built from
// explicit fields. Credentials are URL-encoded to handle special characters.
func buildDSN(cfg Config) string {
	if cfg.DSN != "" {
		return cfg.DSN
	}

	sslMode := cfg.SSLMode
	if sslMode == "" {
		sslMode = "require"
	}
	port := cfg.Port
	if port == 0 {
		port = 5432
	}

	q := url.Values{}
	q.Set("sslmode", sslMode)

	u := &url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(cfg.Username, cfg.Password),
		Host:     fmt.Sprintf("%s:%d", cfg.Host, port),
		Path:     "/" + cfg.Database,
		RawQuery: q.Encode(),
	}
	return u.String()
}

// classifyConnectError maps a pgx dial/auth error to a typed ConnectorError.
func classifyConnectError(err error, host string) *ConnectorError {
	msg := err.Error()
	// pgx surfaces auth failures via FATAL SQLSTATE 28xxx codes.
	if containsStr(msg, "28P01") || containsStr(msg, "28000") || containsStr(msg, "password authentication failed") {
		return connErr(ErrAuth, fmt.Sprintf("authentication failed for host %s", host), err)
	}
	return connErr(ErrConnection, fmt.Sprintf("cannot reach host %s", host), err)
}

func containsStr(s, sub string) bool {
	if len(sub) == 0 {
		return true
	}
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// Ensure Connector satisfies connector.Writer at compile time.
var _ connector.Writer = (*Connector)(nil)
