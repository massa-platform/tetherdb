package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeConfig writes content to a temp file and returns its path.
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "tetherdb.toml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

// writeCerts writes stub cert and key files to a temp dir and returns their paths.
func writeCerts(t *testing.T) (cert, key string) {
	t.Helper()
	dir := t.TempDir()
	cert = filepath.Join(dir, "cert.pem")
	key = filepath.Join(dir, "key.pem")
	os.WriteFile(cert, []byte("cert"), 0o600)
	os.WriteFile(key, []byte("key"), 0o600)
	return cert, key
}

const sourceNodeTOML = `
[node]
name     = "erp-node"
data_dir = "/var/lib/tetherdb"

[management]
address = "127.0.0.1:8080"

[connector]
driver   = "sqlserver"
host     = "sqlserver.internal"
port     = 1433
database = "erp"
auth     = "sqlserver"
username = "syncuser"
password = "secret"

[connector.publish]
tables = ["orders", "customers"]

[[connections]]
name      = "primary-sink"
address   = "sink.internal:443"
subscribe = ["orders"]
`

func TestLoad_ValidSourceNode(t *testing.T) {
	path := writeConfig(t, sourceNodeTOML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Node.Name != "erp-node" {
		t.Errorf("node.name: got %q", cfg.Node.Name)
	}
	if cfg.Connector == nil {
		t.Fatal("connector should be present")
	}
	if cfg.Connector.Driver != "sqlserver" {
		t.Errorf("connector.driver: got %q", cfg.Connector.Driver)
	}
	if len(cfg.Connections) != 1 {
		t.Errorf("expected 1 connection, got %d", len(cfg.Connections))
	}
}

func TestLoad_ValidSinkNode(t *testing.T) {
	cert, key := writeCerts(t)
	content := `
[node]
name     = "pg-sink"
data_dir = "/var/lib/tetherdb"

[management]
address = "127.0.0.1:8080"

[listen]
address  = "0.0.0.0:443"
tls_cert = "` + cert + `"
tls_key  = "` + key + `"
`
	path := writeConfig(t, content)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Listen == nil {
		t.Fatal("listen should be present")
	}
	if cfg.Connector != nil {
		t.Fatal("connector should be absent for sink node")
	}
}

func TestLoad_FileNotFound(t *testing.T) {
	_, err := Load("/nonexistent/tetherdb.toml")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
	if !strings.Contains(err.Error(), "/nonexistent/tetherdb.toml") {
		t.Errorf("error should mention path, got: %v", err)
	}
}

func TestLoad_InvalidTOML(t *testing.T) {
	path := writeConfig(t, "this is not [ valid toml !!!!")
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected parse error for invalid TOML")
	}
}

func TestLoad_EnvVarInterpolation(t *testing.T) {
	t.Setenv("TEST_SS_USER", "myuser")
	t.Setenv("TEST_SS_PASS", "mypass")

	content := `
[node]
name     = "test-node"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[connector]
driver   = "sqlserver"
host     = "host"
port     = 1433
database = "db"
auth     = "sqlserver"
username = "${TEST_SS_USER}"
password = "${TEST_SS_PASS}"

[connector.publish]
tables = ["orders"]
`
	cfg, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Connector.Username != "myuser" {
		t.Errorf("username interpolation: got %q", cfg.Connector.Username)
	}
	if cfg.Connector.Password != "mypass" {
		t.Errorf("password interpolation: got %q", cfg.Connector.Password)
	}
}

func TestLoad_EnvVarMissing(t *testing.T) {
	os.Unsetenv("TETHERDB_MISSING_VAR")
	content := `
[node]
name     = "test-node"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[connector]
driver   = "sqlserver"
host     = "host"
port     = 1433
database = "db"
auth     = "sqlserver"
username = "${TETHERDB_MISSING_VAR}"
password = "pass"

[connector.publish]
tables = ["orders"]
`
	// Undefined env var becomes empty string; validation then catches the required-field violation.
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected validation error when required env var is unset")
	}
	if !strings.Contains(err.Error(), "connector.username") {
		t.Errorf("error should mention connector.username, got: %v", err)
	}
}

func TestValidate_MissingNodeName(t *testing.T) {
	content := `
[node]
name     = ""
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[connector]
driver   = "sqlserver"
host     = "host"
port     = 1433
database = "db"
auth     = "sqlserver"
username = "user"
password = "pass"

[connector.publish]
tables = ["orders"]
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for empty node.name")
	}
	if !strings.Contains(err.Error(), "node.name") {
		t.Errorf("error should mention node.name, got: %v", err)
	}
}

func TestValidate_InvalidManagementAddress(t *testing.T) {
	content := `
[node]
name     = "n"
data_dir = "/tmp"

[management]
address = "0.0.0.0:8080"

[connector]
driver   = "sqlserver"
host     = "host"
port     = 1433
database = "db"
auth     = "sqlserver"
username = "user"
password = "pass"

[connector.publish]
tables = ["orders"]
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for 0.0.0.0 management address")
	}
	if !strings.Contains(err.Error(), "management.address") {
		t.Errorf("error should mention management.address, got: %v", err)
	}
}

func TestValidate_UnknownDriver(t *testing.T) {
	content := `
[node]
name     = "n"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[connector]
driver   = "mysql"
host     = "host"
port     = 1433
database = "db"
auth     = "sqlserver"
username = "user"
password = "pass"

[connector.publish]
tables = ["orders"]
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for unknown driver")
	}
	if !strings.Contains(err.Error(), "connector.driver") {
		t.Errorf("error should mention connector.driver, got: %v", err)
	}
}

func TestValidate_SubscribeNotPublished(t *testing.T) {
	content := `
[node]
name     = "n"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[connector]
driver   = "sqlserver"
host     = "host"
port     = 1433
database = "db"
auth     = "sqlserver"
username = "user"
password = "pass"

[connector.publish]
tables = ["orders"]

[[connections]]
name      = "sink"
address   = "sink:443"
subscribe = ["orders", "invoices"]
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for subscribing to unpublished table")
	}
	if !strings.Contains(err.Error(), "invoices") {
		t.Errorf("error should name the unpublished table, got: %v", err)
	}
}

func TestValidate_DuplicateConnectionName(t *testing.T) {
	content := `
[node]
name     = "n"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[connector]
driver   = "sqlserver"
host     = "host"
port     = 1433
database = "db"
auth     = "sqlserver"
username = "user"
password = "pass"

[connector.publish]
tables = ["orders"]

[[connections]]
name      = "sink"
address   = "sink1:443"
subscribe = ["orders"]

[[connections]]
name      = "sink"
address   = "sink2:443"
subscribe = ["orders"]
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for duplicate connection name")
	}
	if !strings.Contains(err.Error(), "sink") {
		t.Errorf("error should mention duplicate name, got: %v", err)
	}
}

func TestValidate_MissingTLSFiles(t *testing.T) {
	content := `
[node]
name     = "n"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[listen]
address  = "0.0.0.0:443"
tls_cert = "/nonexistent/cert.pem"
tls_key  = "/nonexistent/key.pem"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for missing TLS files")
	}
}

func TestValidate_ConnectionsWithoutConnector(t *testing.T) {
	cert, key := writeCerts(t)
	content := `
[node]
name     = "n"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[listen]
address  = "0.0.0.0:443"
tls_cert = "` + cert + `"
tls_key  = "` + key + `"

[[connections]]
name      = "sink"
address   = "sink:443"
subscribe = ["orders"]
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error for connections without connector")
	}
	if !strings.Contains(err.Error(), "connector") {
		t.Errorf("error should mention connector, got: %v", err)
	}
}

func TestValidate_ListenNoTLS(t *testing.T) {
	// No tls_cert/tls_key → valid; Traefik terminates TLS externally.
	content := `
[node]
name     = "sink"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[listen]
address = "0.0.0.0:8443"
`
	_, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("expected valid config with no TLS files, got: %v", err)
	}
}

func TestValidate_ListenPartialTLS(t *testing.T) {
	// tls_cert set but tls_key empty → error.
	_, key := writeCerts(t)
	_ = key
	cert, _ := writeCerts(t)
	content := `
[node]
name     = "sink"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[listen]
address  = "0.0.0.0:8443"
tls_cert = "` + cert + `"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error when tls_cert is set but tls_key is empty")
	}
	if !strings.Contains(err.Error(), "tls_key") {
		t.Errorf("error should mention tls_key, got: %v", err)
	}
}

func TestValidate_ListenPartialTLSReverse(t *testing.T) {
	// tls_key set but tls_cert empty → error.
	_, key := writeCerts(t)
	content := `
[node]
name     = "sink"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[listen]
address  = "0.0.0.0:8443"
tls_key  = "` + key + `"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error when tls_key is set but tls_cert is empty")
	}
	if !strings.Contains(err.Error(), "tls_cert") {
		t.Errorf("error should mention tls_cert, got: %v", err)
	}
}

func TestValidate_ListenBothTLS(t *testing.T) {
	// Both cert and key present → valid (existing behaviour preserved).
	cert, key := writeCerts(t)
	content := `
[node]
name     = "sink"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"

[listen]
address  = "0.0.0.0:443"
tls_cert = "` + cert + `"
tls_key  = "` + key + `"
`
	_, err := Load(writeConfig(t, content))
	if err != nil {
		t.Fatalf("expected valid config with both TLS files, got: %v", err)
	}
}

func TestValidate_NeitherConnectorNorListen(t *testing.T) {
	content := `
[node]
name     = "n"
data_dir = "/tmp"

[management]
address = "127.0.0.1:8080"
`
	_, err := Load(writeConfig(t, content))
	if err == nil {
		t.Fatal("expected error when neither connector nor listen is present")
	}
}
