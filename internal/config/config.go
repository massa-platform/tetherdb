// Package config loads and validates the tetherdb node configuration from a TOML file.
//
// Environment variable interpolation: any string value containing ${VAR_NAME} is
// replaced with os.Getenv("VAR_NAME") before validation. Only alphanumeric + underscore
// variable names are supported.
//
// Depends on: BurntSushi/toml
// Used by: cmd/tetherdb
package config

import (
	"fmt"
	"net"
	"os"
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// envVarPattern matches ${VAR_NAME} with alphanumeric + underscore names only.
var envVarPattern = regexp.MustCompile(`\$\{([A-Za-z_][A-Za-z0-9_]*)\}`)

// Config is the top-level configuration for a tetherdb node.
//
// All fields are populated from a TOML file via Load. At least one of Connector
// or Listen must be non-nil after loading — a node with neither does nothing.
type Config struct {
	Node        NodeConfig         `toml:"node"`
	Listen      *ListenConfig      `toml:"listen"`
	Management  ManagementConfig   `toml:"management"`
	Connector   *ConnectorConfig   `toml:"connector"`
	Sink        *SinkConfig        `toml:"sink"`
	Connections []ConnectionConfig `toml:"connections"`
}

// SinkConfig holds target database connector configuration for sink nodes.
//
// Present only on nodes that write to a database. Either DSN or explicit
// fields (host, port, database, username, password) must be provided.
type SinkConfig struct {
	// Driver identifies the database type. Only "postgres" is supported in v1.
	Driver string `toml:"driver"`
	// DSN is a raw postgres:// connection string. Used when explicit fields are empty.
	DSN string `toml:"dsn"`
	// Host is the Postgres hostname or IP.
	Host string `toml:"host"`
	// Port is the Postgres TCP port. Defaults to 5432.
	Port int `toml:"port"`
	// Database is the target database name.
	Database string `toml:"database"`
	// Username is the login name.
	Username string `toml:"username"`
	// Password is the login password. Never logged.
	Password string `toml:"password"`
	// SSLMode controls TLS behaviour. Defaults to "require".
	SSLMode string `toml:"sslmode"`
}

// NodeConfig holds identity and local state configuration.
type NodeConfig struct {
	// Name is the unique identifier for this node in the network.
	// Must be non-empty and contain only alphanumeric characters and hyphens.
	Name string `toml:"name"`
	// DataDir is the directory where SQLite state (cursors, sync progress) is stored.
	DataDir string `toml:"data_dir"`
}

// ListenConfig holds inbound WebSocket listener configuration.
//
// Present only on nodes that accept inbound connections (sink or relay nodes).
type ListenConfig struct {
	// Address is the host:port to listen on for inbound WebSocket connections.
	Address string `toml:"address"`
	// TLSCert is the path to the TLS certificate file.
	TLSCert string `toml:"tls_cert"`
	// TLSKey is the path to the TLS private key file.
	TLSKey string `toml:"tls_key"`
}

// ManagementConfig holds local HTTP management API configuration.
type ManagementConfig struct {
	// Address is the host:port for the local management API.
	// Must not be 0.0.0.0 — localhost only by policy.
	Address string `toml:"address"`
}

// ConnectorConfig holds source database connector configuration.
//
// Present only on nodes that read from a database (source or relay nodes).
type ConnectorConfig struct {
	// Driver identifies the database type. Only "sqlserver" is supported in v1.
	Driver string `toml:"driver"`
	// Host is the database server hostname or IP.
	Host string `toml:"host"`
	// Instance is the named SQL Server instance (e.g. "PRIMAVERAV10").
	// Leave empty for the default instance. When set, Port is ignored.
	Instance string `toml:"instance"`
	// Port is the database server TCP port. Defaults to 1433 for SQL Server.
	// Ignored when Instance is set.
	Port int `toml:"port"`
	// Database is the target database name.
	Database string `toml:"database"`
	// Auth is the authentication method: "sqlserver" or "windows".
	Auth string `toml:"auth"`
	// Username is the login name. Required when Auth is "sqlserver".
	Username string `toml:"username"`
	// Password is the login password. Never logged.
	Password string `toml:"password"`
	// Publish lists the tables this node makes available to downstream nodes.
	Publish PublishConfig `toml:"publish"`
}

// PublishConfig declares which tables the connector exposes to downstream nodes.
type PublishConfig struct {
	// Tables is the list of table names (schema.table format) this node publishes.
	Tables []string `toml:"tables"`
}

// ConnectionConfig describes one outbound connection to a downstream node.
type ConnectionConfig struct {
	// Name is a unique identifier for this connection.
	Name string `toml:"name"`
	// Address is the host:port of the downstream node.
	Address string `toml:"address"`
	// Subscribe is the list of tables this connection requests from the upstream node.
	// Every entry must appear in the upstream connector's publish.tables.
	Subscribe []string `toml:"subscribe"`
}

// Load reads a TOML config file from path, interpolates environment variables,
// and validates the result.
//
// Returns a descriptive error if the file is missing, contains invalid TOML,
// or fails any validation rule.
//
// Example:
//
//	cfg, err := config.Load("./tetherdb.toml")
//	if err != nil {
//	    log.Fatalf("config: %v", err)
//	}
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: open %s: %w", path, err)
	}

	interpolated := interpolate(string(data))

	var cfg Config
	if _, err := toml.Decode(interpolated, &cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// interpolate replaces ${VAR_NAME} patterns in s with os.Getenv values.
//
// Only alphanumeric + underscore variable names are expanded. Malformed patterns
// (e.g. ${}) are left as-is. Undefined variables become empty strings.
func interpolate(s string) string {
	return envVarPattern.ReplaceAllStringFunc(s, func(match string) string {
		sub := envVarPattern.FindStringSubmatch(match)
		if len(sub) < 2 {
			return match
		}
		return os.Getenv(sub[1])
	})
}

// Validate checks all fields according to the rules in the PRP.
//
// Returns the first validation error encountered, naming the failing field.
//
// Example:
//
//	if err := cfg.Validate(); err != nil {
//	    return fmt.Errorf("invalid config: %w", err)
//	}
func (c *Config) Validate() error {
	if err := validateNode(c.Node); err != nil {
		return err
	}
	if err := validateManagement(c.Management); err != nil {
		return err
	}
	if c.Listen != nil {
		if err := validateListen(*c.Listen); err != nil {
			return err
		}
	}
	if c.Connector != nil {
		if err := validateConnector(*c.Connector); err != nil {
			return err
		}
	}
	if c.Sink != nil {
		if err := validateSink(*c.Sink); err != nil {
			return err
		}
	}
	if err := validateConnections(c.Connections, c.Connector); err != nil {
		return err
	}
	// Cross-section: at least one of connector or listen must be present.
	if c.Connector == nil && c.Listen == nil {
		return fmt.Errorf("config: at least one of [connector] or [listen] must be present")
	}
	return nil
}

// nodeNamePattern allows alphanumeric characters and hyphens only.
var nodeNamePattern = regexp.MustCompile(`^[a-zA-Z0-9-]+$`)

func validateNode(n NodeConfig) error {
	if n.Name == "" {
		return fmt.Errorf("config: node.name must not be empty")
	}
	if !nodeNamePattern.MatchString(n.Name) {
		return fmt.Errorf("config: node.name %q must contain only alphanumeric characters and hyphens", n.Name)
	}
	if n.DataDir == "" {
		return fmt.Errorf("config: node.data_dir must not be empty")
	}
	return nil
}

func validateManagement(m ManagementConfig) error {
	if m.Address == "" {
		return fmt.Errorf("config: management.address must not be empty")
	}
	host, _, err := net.SplitHostPort(m.Address)
	if err != nil {
		return fmt.Errorf("config: management.address %q is not a valid host:port: %w", m.Address, err)
	}
	if host == "0.0.0.0" {
		return fmt.Errorf("config: management.address must not bind to 0.0.0.0 — use 127.0.0.1 to restrict to localhost")
	}
	return nil
}

func validateListen(l ListenConfig) error {
	if l.Address == "" {
		return fmt.Errorf("config: listen.address must not be empty")
	}
	if _, _, err := net.SplitHostPort(l.Address); err != nil {
		return fmt.Errorf("config: listen.address %q is not a valid host:port: %w", l.Address, err)
	}
	// Both empty → no-TLS mode; Traefik (or similar) terminates TLS upstream.
	if l.TLSCert == "" && l.TLSKey == "" {
		return nil
	}
	// Partial TLS config is always an error.
	if l.TLSCert == "" {
		return fmt.Errorf("config: listen.tls_cert must not be empty when listen.tls_key is set")
	}
	if l.TLSKey == "" {
		return fmt.Errorf("config: listen.tls_key must not be empty when listen.tls_cert is set")
	}
	if _, err := os.Stat(l.TLSCert); err != nil {
		return fmt.Errorf("config: listen.tls_cert %q not found: %w", l.TLSCert, err)
	}
	if _, err := os.Stat(l.TLSKey); err != nil {
		return fmt.Errorf("config: listen.tls_key %q not found: %w", l.TLSKey, err)
	}
	return nil
}

func validateConnector(c ConnectorConfig) error {
	if c.Driver != "sqlserver" {
		return fmt.Errorf("config: connector.driver %q is not supported; only \"sqlserver\" is supported in v1", c.Driver)
	}
	if c.Host == "" {
		return fmt.Errorf("config: connector.host must not be empty")
	}
	port := c.Port
	if port == 0 {
		port = 1433
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("config: connector.port %d is out of range 1-65535", port)
	}
	if c.Auth != "sqlserver" && c.Auth != "windows" {
		return fmt.Errorf("config: connector.auth %q must be \"sqlserver\" or \"windows\"", c.Auth)
	}
	if c.Auth == "sqlserver" && c.Username == "" {
		return fmt.Errorf("config: connector.username must not be empty when connector.auth is \"sqlserver\"")
	}
	if len(c.Publish.Tables) == 0 {
		return fmt.Errorf("config: connector.publish.tables must contain at least one table")
	}
	return nil
}

func validateConnections(conns []ConnectionConfig, connector *ConnectorConfig) error {
	// Cross-section: connections require a connector to read from.
	if len(conns) > 0 && connector == nil {
		return fmt.Errorf("config: [[connections]] requires [connector] — a node cannot forward changes it does not read")
	}

	published := map[string]bool{}
	if connector != nil {
		for _, t := range connector.Publish.Tables {
			published[t] = true
		}
	}

	seen := map[string]bool{}
	for i, conn := range conns {
		if conn.Name == "" {
			return fmt.Errorf("config: connections[%d].name must not be empty", i)
		}
		if seen[conn.Name] {
			return fmt.Errorf("config: connections[%d].name %q is a duplicate — connection names must be unique", i, conn.Name)
		}
		seen[conn.Name] = true

		if conn.Address == "" {
			return fmt.Errorf("config: connections[%d] %q: address must not be empty", i, conn.Name)
		}
		if _, _, err := net.SplitHostPort(conn.Address); err != nil {
			return fmt.Errorf("config: connections[%d] %q: address %q is not a valid host:port: %w", i, conn.Name, conn.Address, err)
		}
		if len(conn.Subscribe) == 0 {
			return fmt.Errorf("config: connections[%d] %q: subscribe must contain at least one table", i, conn.Name)
		}
		for _, table := range conn.Subscribe {
			if !published[table] {
				return fmt.Errorf("config: connections[%d] %q: subscribes to table %q which is not in connector.publish.tables", i, conn.Name, table)
			}
		}
	}
	return nil
}

// HasConnector reports whether this node has a source database connector configured.
//
// Example:
//
//	if cfg.HasConnector() {
//	    // start SQL Server reader
//	}
func (c *Config) HasConnector() bool { return c.Connector != nil }

// HasListen reports whether this node accepts inbound WebSocket connections.
//
// Example:
//
//	if cfg.HasListen() {
//	    // start TLS listener
//	}
func (c *Config) HasListen() bool { return c.Listen != nil }

// RedactedConnectorDSN returns a loggable connection string with the password omitted.
//
// Example:
//
//	log.Info("connecting", "dsn", cfg.RedactedConnectorDSN())
func (c *Config) RedactedConnectorDSN() string {
	if c.Connector == nil {
		return ""
	}
	return fmt.Sprintf("sqlserver://%s@%s/%s",
		c.Connector.Username, c.Connector.Host, c.Connector.Database)
}

// PublishedTableSet returns the set of tables the connector publishes, keyed for O(1) lookup.
//
// Example:
//
//	published := cfg.PublishedTableSet()
//	if published["dbo.Orders"] { ... }
func (c *Config) PublishedTableSet() map[string]bool {
	if c.Connector == nil {
		return nil
	}
	set := make(map[string]bool, len(c.Connector.Publish.Tables))
	for _, t := range c.Connector.Publish.Tables {
		set[t] = true
	}
	return set
}

// TableNames returns the connector's publish table list as a slice, for use with the connector.
//
// Example:
//
//	tables := cfg.TableNames()
func (c *Config) TableNames() []string {
	if c.Connector == nil {
		return nil
	}
	out := make([]string, len(c.Connector.Publish.Tables))
	copy(out, c.Connector.Publish.Tables)
	return out
}

// ConnectorPassword returns the connector password without logging it.
// Callers must ensure the return value is never passed to a logger.
//
// Example:
//
//	pass := cfg.ConnectorPassword()
func (c *Config) ConnectorPassword() string {
	if c.Connector == nil {
		return ""
	}
	return c.Connector.Password
}

// IsSourceNode reports whether this node reads from a database and forwards changes downstream.
//
// Example:
//
//	if cfg.IsSourceNode() { ... }
func (c *Config) IsSourceNode() bool {
	return c.Connector != nil && len(c.Connections) > 0
}

// IsSinkNode reports whether this node accepts inbound changes only (no connector).
//
// Example:
//
//	if cfg.IsSinkNode() { ... }
func (c *Config) IsSinkNode() bool {
	return c.Connector == nil && c.Listen != nil
}

// IsRelayNode reports whether this node both accepts inbound and forwards outbound changes.
//
// Example:
//
//	if cfg.IsRelayNode() { ... }
func (c *Config) IsRelayNode() bool {
	return c.Connector != nil && c.Listen != nil
}

// validateSink checks that the sink config has a supported driver and either
// a raw DSN or the required explicit fields.
func validateSink(s SinkConfig) error {
	if s.Driver != "postgres" {
		return fmt.Errorf("config: sink.driver %q is not supported; only \"postgres\" is supported in v1", s.Driver)
	}
	if s.DSN != "" {
		return nil
	}
	if s.Host == "" {
		return fmt.Errorf("config: sink.host must not be empty when sink.dsn is not set")
	}
	if s.Database == "" {
		return fmt.Errorf("config: sink.database must not be empty when sink.dsn is not set")
	}
	if s.Username == "" {
		return fmt.Errorf("config: sink.username must not be empty when sink.dsn is not set")
	}
	return nil
}

// HasSink reports whether this node has a target database sink configured.
//
// Example:
//
//	if cfg.HasSink() {
//	    // start Postgres writer
//	}
func (c *Config) HasSink() bool { return c.Sink != nil }

// SinkPassword returns the sink password without logging it.
// Callers must ensure the return value is never passed to a logger.
//
// Example:
//
//	pass := cfg.SinkPassword()
func (c *Config) SinkPassword() string {
	if c.Sink == nil {
		return ""
	}
	return c.Sink.Password
}

// RedactedSinkDSN returns a loggable sink connection string with the password omitted.
//
// Example:
//
//	log.Info("connecting sink", "dsn", cfg.RedactedSinkDSN())
func (c *Config) RedactedSinkDSN() string {
	if c.Sink == nil {
		return ""
	}
	if c.Sink.DSN != "" {
		// Redact the password from the raw DSN by replacing the userinfo segment.
		return "(raw DSN — redacted)"
	}
	return fmt.Sprintf("postgres://%s@%s/%s", c.Sink.Username, c.Sink.Host, c.Sink.Database)
}

// redactedAddress strips the password from a connection string for logging.
func redactedAddress(addr string) string {
	return strings.TrimRight(addr, "/")
}
