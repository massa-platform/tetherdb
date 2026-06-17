// Command tetherdb is the main entrypoint for a tetherdb node.
//
// Run with no arguments to print help. Use --config to specify a TOML
// config file. Subcommands install, uninstall, start, stop, and run manage
// the node as a system service via kardianos/service.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/kardianos/service"
	"github.com/massa-platform/tetherdb/internal/config"
	"github.com/massa-platform/tetherdb/internal/connector/sqlserver"
)

// version is injected at build time: -ldflags "-X main.version=v1.2.3"
var version = "dev"

const helpText = `tetherdb — data mesh sync node

Usage:
  tetherdb [--config PATH] [SUBCOMMAND]

Subcommands:
  run         Start the node in the foreground
  install     Install tetherdb as a system service
  uninstall   Remove the system service
  start       Start the installed service
  stop        Stop the running service
  version     Print version and exit

Flags:
  --config PATH   Path to TOML config file (default: ./tetherdb.toml)
  --version       Print version and exit

Examples:
  tetherdb --config /etc/tetherdb/tetherdb.toml run
  tetherdb install
  tetherdb start
`

func main() {
	fs := flag.NewFlagSet("tetherdb", flag.ContinueOnError)
	fs.Usage = func() { fmt.Fprint(os.Stdout, helpText) }

	configPath := fs.String("config", "./tetherdb.toml", "path to TOML config file")
	showVersion := fs.Bool("version", false, "print version and exit")

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	if *showVersion {
		fmt.Println("tetherdb", version)
		os.Exit(0)
	}

	args := fs.Args()
	if len(args) == 0 {
		fmt.Fprint(os.Stdout, helpText)
		os.Exit(0)
	}

	sub := args[0]

	if sub == "version" {
		fmt.Println("tetherdb", version)
		os.Exit(0)
	}

	svcConfig := &service.Config{
		Name:        "tetherdb",
		DisplayName: "tetherdb sync node",
		Description: "tetherdb data mesh synchronisation node",
	}

	prg := &program{configPath: *configPath}
	svc, err := service.New(prg, svcConfig)
	if err != nil {
		slog.Error("failed to create service", "error", err)
		os.Exit(1)
	}

	switch sub {
	case "run":
		if err := svc.Run(); err != nil {
			slog.Error("node exited with error", "error", err)
			os.Exit(1)
		}
	case "install":
		if err := svc.Install(); err != nil {
			slog.Error("install failed", "error", err)
			os.Exit(1)
		}
		fmt.Println("tetherdb service installed")
	case "uninstall":
		if err := svc.Uninstall(); err != nil {
			slog.Error("uninstall failed", "error", err)
			os.Exit(1)
		}
		fmt.Println("tetherdb service uninstalled")
	case "start":
		if err := svc.Start(); err != nil {
			slog.Error("start failed", "error", err)
			os.Exit(1)
		}
		fmt.Println("tetherdb service started")
	case "stop":
		if err := svc.Stop(); err != nil {
			slog.Error("stop failed", "error", err)
			os.Exit(1)
		}
		fmt.Println("tetherdb service stopped")
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n", sub)
		fmt.Fprint(os.Stderr, helpText)
		os.Exit(2)
	}
}

// program implements service.Interface for kardianos/service.
type program struct {
	configPath string
	cancel     context.CancelFunc
}

// Start is called by the service manager when the service starts.
//
// It loads config, probes the connector (if configured), logs the startup
// line, and returns immediately — the node runs in the background goroutine
// until Stop is called.
func (p *program) Start(s service.Service) error {
	cfg, err := config.Load(p.configPath)
	if err != nil {
		return fmt.Errorf("main: load config: %w", err)
	}

	slog.Info("tetherdb starting",
		"version", version,
		"node", cfg.Node.Name,
		"config", p.configPath,
	)

	if cfg.HasConnector() {
		slog.Info("probing connector",
			"dsn", cfg.RedactedConnectorDSN())

		ctx := context.Background()
		conn, err := sqlserver.New(ctx, sqlserver.Config{
			Host:     cfg.Connector.Host,
			Port:     cfg.Connector.Port,
			Database: cfg.Connector.Database,
			Auth:     cfg.Connector.Auth,
			User:     cfg.Connector.Username,
			Password: cfg.ConnectorPassword(),
			Tables:   cfg.TableNames(),
		})
		if err != nil {
			return fmt.Errorf("main: create connector: %w", err)
		}
		defer conn.Close()

		if err := conn.Probe(ctx); err != nil {
			return fmt.Errorf("main: connector probe: %w", err)
		}
		slog.Info("connector probe succeeded")
	}

	ctx, cancel := context.WithCancel(context.Background())
	p.cancel = cancel

	go p.run(ctx)
	return nil
}

// Stop is called by the service manager when the service stops.
//
// It cancels the node's context, causing run() to return.
func (p *program) Stop(_ service.Service) error {
	slog.Info("tetherdb stopping")
	if p.cancel != nil {
		p.cancel()
	}
	return nil
}

// run blocks until ctx is cancelled. Future PRPs will wire the pipeline engine here.
func (p *program) run(ctx context.Context) {
	<-ctx.Done()
	slog.Info("tetherdb stopped")
}
