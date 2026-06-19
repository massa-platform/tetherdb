// listener.go — sink-side inbound WebSocket connection acceptor.
package transport

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/gorilla/websocket"
)

// ConnHandler is called once per accepted, handshaked connection.
//
// The handler receives batches via conn.Recv and must send Ack or Nack for each.
// It runs in its own goroutine and should return when ctx is cancelled or the
// connection is closed.
type ConnHandler func(ctx context.Context, nodeName string, conn *Conn)

// ListenerConfig holds parameters for a sink-side inbound listener.
type ListenerConfig struct {
	// Address is the host:port to listen on (e.g. "0.0.0.0:8443").
	Address string
	// Published is the set of tables this node exposes. Used to validate Hello.
	Published map[string]bool
	// Handler is called for each validated inbound connection.
	Handler ConnHandler
	// TLSCert and TLSKey are optional. Empty means plain ws:// (Traefik terminates upstream).
	TLSCert string
	TLSKey  string
}

// Listener accepts inbound WebSocket connections from source nodes.
//
// Each accepted connection runs through a Hello/HelloAck handshake. Connections
// subscribing to tables not in Published are rejected immediately. Valid connections
// are dispatched to the configured Handler.
//
// Example:
//
//	l := NewListener(ListenerConfig{Address: "0.0.0.0:8443", Published: published, Handler: h})
//	if err := l.Start(ctx); err != nil { ... }
type Listener struct {
	cfg      ListenerConfig
	log      *slog.Logger
	upgrader websocket.Upgrader

	// onReady is called with the actual listen address after the server starts.
	// Used in tests to discover the ephemeral port.
	onReady func(addr string)
}

// NewListener creates a Listener ready to Start.
//
// Example:
//
//	l := NewListener(ListenerConfig{Address: "0.0.0.0:8443", Published: map[string]bool{"dbo.Orders": true}, Handler: h})
func NewListener(cfg ListenerConfig) *Listener {
	return &Listener{
		cfg: cfg,
		log: slog.Default(),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}
}

// Start begins accepting WebSocket connections. Blocks until ctx is cancelled.
//
// If TLSCert and TLSKey are non-empty, the listener uses TLS. Otherwise it
// listens for plain WebSocket (Traefik terminates TLS upstream).
//
// Returns a non-nil error only if the listener cannot bind to the address.
//
// Example:
//
//	if err := l.Start(ctx); err != nil { log.Fatal(err) }
func (l *Listener) Start(ctx context.Context) error {
	ln, err := net.Listen("tcp", l.cfg.Address)
	if err != nil {
		return fmt.Errorf("transport: listen %s: %w", l.cfg.Address, err)
	}

	if l.onReady != nil {
		l.onReady(ln.Addr().String())
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", l.handleWS)
	srv := &http.Server{Handler: mux}

	// Shut down the HTTP server when ctx is cancelled.
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	l.log.Info("transport: listener started", "address", ln.Addr().String())

	var serveErr error
	if l.cfg.TLSCert != "" && l.cfg.TLSKey != "" {
		serveErr = srv.ServeTLS(ln, l.cfg.TLSCert, l.cfg.TLSKey)
	} else {
		serveErr = srv.Serve(ln)
	}

	// http.ErrServerClosed is the expected error when ctx is cancelled.
	if serveErr != nil && !isServerClosed(serveErr) {
		return fmt.Errorf("transport: listener: %w", serveErr)
	}
	return nil
}

// handleWS upgrades an HTTP connection to WebSocket, runs the handshake,
// and dispatches to the configured handler.
func (l *Listener) handleWS(w http.ResponseWriter, r *http.Request) {
	ws, err := l.upgrader.Upgrade(w, r, nil)
	if err != nil {
		l.log.Warn("transport: websocket upgrade failed", "error", err)
		return
	}
	conn := NewConn(ws)

	ctx := r.Context()
	msg, err := conn.Recv(ctx)
	if err != nil {
		l.log.Warn("transport: recv hello failed", "error", err)
		conn.Close()
		return
	}
	hello, ok := msg.(Hello)
	if !ok {
		l.log.Warn("transport: expected Hello, got unexpected type", "type", fmt.Sprintf("%T", msg))
		_ = conn.Send(ctx, HelloAck{Type: "hello_ack", Accepted: false, Reason: "expected Hello as first message"})
		conn.Close()
		return
	}

	// Validate every subscribed table is published by this node.
	for _, tbl := range hello.Subscribe {
		if !l.cfg.Published[tbl] {
			reason := "table not published: " + tbl
			l.log.Warn("transport: rejecting connection", "node", hello.NodeName, "reason", reason)
			_ = conn.Send(ctx, HelloAck{Type: "hello_ack", Accepted: false, Reason: reason})
			conn.Close()
			return
		}
	}

	if err := conn.Send(ctx, HelloAck{Type: "hello_ack", Accepted: true}); err != nil {
		l.log.Warn("transport: send hello_ack failed", "error", err)
		conn.Close()
		return
	}
	l.log.Info("transport: connection accepted", "node", hello.NodeName)

	if l.cfg.Handler != nil {
		l.cfg.Handler(ctx, hello.NodeName, conn)
	}
}

// isServerClosed reports whether the error is the expected shutdown error from http.Server.Close.
func isServerClosed(err error) bool {
	return err != nil && strings.Contains(err.Error(), "Server closed")
}
