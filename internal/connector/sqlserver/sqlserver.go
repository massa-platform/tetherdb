package sqlserver

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"time"

	_ "github.com/microsoft/go-mssqldb" // registers the "sqlserver" driver
)

// Config holds all connection parameters for a SQL Server connector.
//
// All fields are required unless noted. No field may be hardcoded by the caller —
// values must come from a config file or environment variable.
type Config struct {
	// Host is the SQL Server hostname or IP address. Must be non-empty.
	Host string
	// Instance is the named SQL Server instance (e.g. "PRIMAVERAV10").
	// When set, Port is ignored — named instances use dynamic port negotiation
	// via SQL Server Browser service. Leave empty for default instance.
	Instance string
	// Port is the TCP port. Must be between 1 and 65535. Defaults to 1433.
	// Ignored when Instance is set.
	Port int
	// Database is the target database name. Must be non-empty.
	Database string
	// Auth controls the authentication method: "sqlserver" or "windows".
	Auth string
	// User is the login name. Required when Auth is "sqlserver".
	User string
	// Password is the login password. Required when Auth is "sqlserver".
	// Never logged.
	Password string
	// Tables is the list of tables this connector will read. Format: "schema.table".
	Tables []string
}

// changeMechanism records which SQL Server change feature is in use.
type changeMechanism int

const (
	mechanismUnknown changeMechanism = iota
	mechanismCDC
	mechanismCT
)

// Connector implements connector.Reader for SQL Server.
//
// Create a Connector with New, then call Probe before using InitialSync or Changes.
type Connector struct {
	cfg       Config
	db        *sql.DB
	q         querier
	mechanism changeMechanism
	log       *slog.Logger
}

// New opens a SQL Server connection and returns a Connector.
//
// New validates Config before opening the connection. It does not call Probe —
// the caller must call Probe to verify tables and detect the change mechanism.
//
// Example:
//
//	conn, err := sqlserver.New(ctx, sqlserver.Config{
//	    Host: "sql-server.example.com", Port: 1433,
//	    Database: "sales", Auth: "sqlserver",
//	    User: "sync_user", Password: os.Getenv("SS_PASSWORD"),
//	    Tables: []string{"dbo.Orders"},
//	})
func New(ctx context.Context, cfg Config) (*Connector, error) {
	if err := validateConfig(cfg); err != nil {
		return nil, err
	}

	dsn := buildDSN(cfg)
	db, err := sql.Open("sqlserver", dsn)
	if err != nil {
		return nil, connErr(ErrConnection, "open driver", err)
	}

	// Verify the connection is usable immediately — sql.Open is lazy.
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, connErr(ErrConnection, fmt.Sprintf("ping %s:%d", cfg.Host, cfg.Port), err)
	}

	c := &Connector{
		cfg: cfg,
		db:  db,
		log: slog.Default(),
	}
	c.q = &dbQuerier{db: db}
	return c, nil
}

// Close releases the underlying database connection.
//
// Example:
//
//	defer conn.Close()
func (c *Connector) Close() error {
	if err := c.db.Close(); err != nil {
		return fmt.Errorf("sqlserver: close: %w", err)
	}
	return nil
}

// validateConfig checks that required fields are present and values are in range.
func validateConfig(cfg Config) error {
	if cfg.Host == "" {
		return connErr(ErrInvalidConfig, "host must not be empty", nil)
	}
	if cfg.Database == "" {
		return connErr(ErrInvalidConfig, "database must not be empty", nil)
	}
	// Port validation only applies to default instances; named instances use
	// SQL Server Browser dynamic port negotiation and ignore Port.
	if cfg.Instance == "" {
		port := cfg.Port
		if port == 0 {
			port = 1433
		}
		if port < 1 || port > 65535 {
			return connErr(ErrInvalidConfig, fmt.Sprintf("port %d out of range 1-65535", port), nil)
		}
	}
	if cfg.Auth != "sqlserver" && cfg.Auth != "windows" {
		return connErr(ErrInvalidConfig, fmt.Sprintf("auth must be 'sqlserver' or 'windows', got %q", cfg.Auth), nil)
	}
	if cfg.Auth == "sqlserver" && cfg.User == "" {
		return connErr(ErrInvalidConfig, "user must not be empty when auth=sqlserver", nil)
	}
	// Validate host is a resolvable hostname or IP — reject obviously malformed values.
	if net.ParseIP(cfg.Host) == nil {
		// Not an IP; check it looks like a valid hostname (basic sanity only —
		// full DNS resolution happens at connect time).
		if len(cfg.Host) > 253 {
			return connErr(ErrInvalidConfig, "host exceeds maximum hostname length", nil)
		}
	}
	return nil
}

// buildDSN constructs the mssqldb connection string with the password redacted in logs.
//
// Credentials are URL-encoded to handle special characters (!, @, #, etc).
// Named instances use SQL Server Browser dynamic port negotiation via ?instance=.
func buildDSN(cfg Config) string {
	// Log the redacted DSN — never include the password.
	redacted := fmt.Sprintf("sqlserver://%s@%s", cfg.User, cfg.Host)
	if cfg.Instance != "" {
		redacted += `\` + cfg.Instance
	}
	redacted += "/" + cfg.Database
	slog.Default().Info("sqlserver: connecting", "dsn", redacted)

	q := url.Values{}
	q.Set("database", cfg.Database)
	if cfg.Instance != "" {
		q.Set("instance", cfg.Instance)
	}

	if cfg.Auth == "windows" {
		u := &url.URL{Scheme: "sqlserver", Host: cfg.Host, RawQuery: q.Encode()}
		if cfg.Instance == "" {
			port := cfg.Port
			if port == 0 {
				port = 1433
			}
			u.Host = fmt.Sprintf("%s:%d", cfg.Host, port)
		}
		q.Set("integrated security", "true")
		u.RawQuery = q.Encode()
		return u.String()
	}

	u := &url.URL{
		Scheme:   "sqlserver",
		User:     url.UserPassword(cfg.User, cfg.Password),
		Host:     cfg.Host,
		RawQuery: q.Encode(),
	}
	if cfg.Instance == "" {
		port := cfg.Port
		if port == 0 {
			port = 1433
		}
		u.Host = fmt.Sprintf("%s:%d", cfg.Host, port)
	}
	return u.String()
}

// retryWithBackoff runs fn, retrying on error with exponential backoff.
// Caps at 60 s. Logs each retry attempt.
func retryWithBackoff(ctx context.Context, log *slog.Logger, fn func() error) error {
	delays := []time.Duration{1, 2, 4, 8, 16, 32, 60}
	start := time.Now()
	var err error
	for i, d := range append([]time.Duration{0}, delays...) {
		if i > 0 {
			log.Warn("sqlserver: retrying after error",
				"attempt", i, "backoff_s", d, "elapsed", time.Since(start).Round(time.Millisecond),
				"error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d * time.Second):
			}
		}
		err = fn()
		if err == nil {
			return nil
		}
	}
	return err
}
