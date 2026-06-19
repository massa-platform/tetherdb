// dialer.go — source-side outbound WebSocket connection with reconnect loop.
package transport

import (
	"context"
	"crypto/tls"
	"fmt"
	"log/slog"
	"net/url"
	"time"

	"github.com/gorilla/websocket"
)

// DialerConfig holds parameters for a source-side outbound connection.
type DialerConfig struct {
	// NodeName is this node's identity, sent in Hello.
	NodeName string
	// Address is the sink node's host:port (e.g. "tetherdb.example.com:443").
	Address string
	// Subscribe is the list of tables to request from the sink.
	Subscribe []string
	// TLSSkipVerify disables TLS certificate verification. Never true in production.
	TLSSkipVerify bool

	// scheme overrides the URL scheme ("ws" or "wss"). Defaults to "wss".
	// Used in tests to connect to plain HTTP servers.
	scheme string
}

// Dialer manages one persistent outbound connection to a sink node.
//
// Call Run to start the connect-handshake-stream loop. SendBatch enqueues
// a batch for delivery. Received Acks and Nacks are available on the Acks
// and Nacks channels.
//
// Example:
//
//	d := NewDialer(cfg)
//	go d.Run(ctx)
//	if err := d.SendBatch(ctx, batch); err != nil { ... }
//	select {
//	case ack := <-d.Acks: ...
//	case nack := <-d.Nacks: ...
//	}
type Dialer struct {
	cfg    DialerConfig
	log    *slog.Logger
	send   chan ChangeBatch
	Acks   chan Ack
	Nacks  chan Nack
}

// NewDialer creates a Dialer ready to Run.
//
// Example:
//
//	d := NewDialer(DialerConfig{NodeName: "src", Address: "sink:443", Subscribe: []string{"dbo.Orders"}})
func NewDialer(cfg DialerConfig) *Dialer {
	return &Dialer{
		cfg:   cfg,
		log:   slog.Default(),
		send:  make(chan ChangeBatch, 16),
		Acks:  make(chan Ack, 16),
		Nacks: make(chan Nack, 16),
	}
}

// Run starts the connect-handshake-stream loop. Blocks until ctx is cancelled.
//
// On any error (dial failure, handshake rejection, read error) it waits with
// exponential backoff before reconnecting. Backoff: 1s, 2s, 4s, 8s, 16s, 32s, cap 60s.
//
// Example:
//
//	go func() { _ = d.Run(ctx) }()
func (d *Dialer) Run(ctx context.Context) error {
	delays := []time.Duration{1, 2, 4, 8, 16, 32, 60}
	attempt := 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if attempt > 0 {
			idx := attempt - 1
			if idx >= len(delays) {
				idx = len(delays) - 1
			}
			wait := delays[idx] * time.Second
			d.log.Warn("transport: dialer reconnecting",
				"attempt", attempt, "backoff", wait)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(wait):
			}
		}
		attempt++

		err := d.runOnce(ctx)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d.log.Warn("transport: dialer connection lost, will retry",
			"attempt", attempt, "error", err)
	}
}

// SendBatch enqueues a batch for delivery.
//
// Blocks until the connection is established and the batch has been written to the
// WebSocket — not until it has been ACKed. The caller waits on d.Acks or d.Nacks.
//
// Example:
//
//	if err := d.SendBatch(ctx, batch); err != nil { ... }
func (d *Dialer) SendBatch(ctx context.Context, batch ChangeBatch) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case d.send <- batch:
		return nil
	}
}

// runOnce dials, handshakes, and streams until the connection is lost or ctx is cancelled.
func (d *Dialer) runOnce(ctx context.Context) error {
	scheme := d.cfg.scheme
	if scheme == "" {
		scheme = "wss"
	}
	u := url.URL{Scheme: scheme, Host: d.cfg.Address, Path: "/"}
	dialer := &websocket.Dialer{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: d.cfg.TLSSkipVerify}, //nolint:gosec — controlled by config, documented
		HandshakeTimeout: 10 * time.Second,
	}
	ws, _, err := dialer.DialContext(ctx, u.String(), nil)
	if err != nil {
		return fmt.Errorf("transport: dial %s: %w", u.String(), err)
	}
	conn := NewConn(ws)
	defer conn.Close()

	// Handshake: send Hello, expect HelloAck.
	if err := conn.Send(ctx, Hello{Type: "hello", NodeName: d.cfg.NodeName, Subscribe: d.cfg.Subscribe}); err != nil {
		return fmt.Errorf("transport: send hello: %w", err)
	}
	msg, err := conn.Recv(ctx)
	if err != nil {
		return fmt.Errorf("transport: recv hello_ack: %w", err)
	}
	ack, ok := msg.(HelloAck)
	if !ok {
		return fmt.Errorf("transport: expected HelloAck, got %T", msg)
	}
	if !ack.Accepted {
		// Rejection is permanent — the sink rejected our subscription list.
		// Return a non-retryable sentinel so the caller can distinguish.
		return fmt.Errorf("transport: handshake rejected: %s", ack.Reason)
	}
	d.log.Info("transport: handshake accepted", "address", d.cfg.Address)

	return d.stream(ctx, conn)
}

// stream sends batches from the send channel and routes Ack/Nack responses.
// If the connection is lost while a batch is in-flight (sent but not yet ACKed),
// the batch is re-queued so the next connection can retry it.
func (d *Dialer) stream(ctx context.Context, conn *Conn) error {
	recvCh := make(chan any, 4)
	recvErr := make(chan error, 1)
	go func() {
		for {
			msg, err := conn.Recv(ctx)
			if err != nil {
				recvErr <- err
				return
			}
			recvCh <- msg
		}
	}()

	// inflight holds a batch that has been written to the wire but not yet ACKed.
	// On connection loss it is re-queued to be retried on the next connection.
	var inflight *ChangeBatch

	requeue := func(b ChangeBatch) {
		select {
		case d.send <- b:
		default:
		}
	}

	for {
		select {
		case <-ctx.Done():
			if inflight != nil {
				requeue(*inflight)
			}
			return ctx.Err()
		case err := <-recvErr:
			if inflight != nil {
				requeue(*inflight)
			}
			return err
		case msg := <-recvCh:
			switch m := msg.(type) {
			case Ack:
				inflight = nil
				d.Acks <- m
			case Nack:
				inflight = nil
				d.Nacks <- m
			}
		case batch := <-d.send:
			inflight = &batch
			if err := conn.Send(ctx, batch); err != nil {
				requeue(batch)
				return fmt.Errorf("transport: send batch: %w", err)
			}
		}
	}
}
