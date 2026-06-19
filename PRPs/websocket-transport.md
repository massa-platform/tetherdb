# PRP: WebSocket Transport Layer

**Status:** approved
**Spec ref:** §5.6, implementation plan step 3
**Branch:** TBD

---

## Goal

Implement node-to-node communication over a persistent WebSocket connection.
The source node dials the sink, performs a subscription handshake, then streams
`ChangeBatch` messages. The sink applies each batch and sends back an `Ack` or
`Nack`. The source never advances its cursor until it receives an `Ack`.

Phase 3 scope: the connection machinery, wire protocol, and reconnect loop only.
The pipeline engine (step 5) wires the transport to Reader/Writer — that is out of
scope here.

---

## Wire Protocol

All messages are newline-delimited JSON. Each message is a JSON object with a
`type` field that identifies the message kind. One message per WebSocket text frame.

### Message types

```go
// Hello is sent by the source immediately after the WebSocket connection opens.
// It identifies the source node and declares which tables it wants to stream.
type Hello struct {
    Type      string   `json:"type"`       // "hello"
    NodeName  string   `json:"node_name"`
    Subscribe []string `json:"subscribe"`  // table names
}

// HelloAck is sent by the sink in response to Hello.
// Rejected connections include a human-readable reason and the sink closes
// the connection immediately after sending.
type HelloAck struct {
    Type     string `json:"type"`              // "hello_ack"
    Accepted bool   `json:"accepted"`
    Reason   string `json:"reason,omitempty"`  // non-empty on rejection
}

// ChangeBatch carries a slice of changes from source to sink.
// BatchID is a monotonically increasing uint64, unique per connection.
type ChangeBatch struct {
    Type    string             `json:"type"`     // "change_batch"
    BatchID uint64             `json:"batch_id"`
    Changes []connector.Change `json:"changes"`
}

// Ack is sent by the sink after successfully applying a ChangeBatch.
type Ack struct {
    Type    string `json:"type"`     // "ack"
    BatchID uint64 `json:"batch_id"`
}

// Nack is sent by the sink when a ChangeBatch could not be applied.
// The source must retry the same batch (same BatchID, same changes).
type Nack struct {
    Type    string `json:"type"`     // "nack"
    BatchID uint64 `json:"batch_id"`
    Reason  string `json:"reason"`
}
```

### Connection lifecycle

```
source                              sink
  │                                  │
  ├── WebSocket dial ───────────────►│
  ├── Hello{subscribe: [...]} ──────►│
  │                           validate subscribe ⊆ published
  │◄── HelloAck{accepted: true} ─────┤
  │                                  │
  ├── ChangeBatch{id: 1} ───────────►│ apply batch
  │◄── Ack{id: 1} ───────────────────┤
  ├── advance cursor                 │
  │                                  │
  ├── ChangeBatch{id: 2} ───────────►│ apply fails
  │◄── Nack{id: 2, reason: "..."} ───┤
  ├── retry same batch               │
  │                                  │
  │  [connection drops]              │
  ├── reconnect with backoff         │
  ├── Hello{subscribe: [...]} ──────►│  (new connection, same handshake)
  │◄── HelloAck{accepted: true} ─────┤
  ├── replay from last ACKed cursor  │  (cursor not advanced for id:2)
```

On reconnect, the source replays immediately from the last ACKed cursor after the
handshake completes. No explicit cursor exchange in the handshake — the source owns
its cursor, the sink's idempotent apply handles duplicate delivery (spec §5.6).

---

## Package layout

```
internal/transport/
    messages.go        — Hello, HelloAck, ChangeBatch, Ack, Nack types + encode/decode
    conn.go            — Conn: wraps gorilla/websocket, send/receive typed messages
    dialer.go          — Dialer: source side — dial, handshake, send batches, receive ACKs, reconnect loop
    listener.go        — Listener: sink side — HTTP handler, accept connections, handshake, dispatch
    transport_test.go  — unit tests (in-process pipe, no real network)
```

No file may exceed 300 lines.

---

## Dialer (source side)

```go
// DialerConfig holds parameters for a source-side outbound connection.
type DialerConfig struct {
    // NodeName is this node's identity, sent in Hello.
    NodeName string
    // Address is the sink node's host:port (e.g. "tetherdb.dafifi.net:443").
    Address string
    // Subscribe is the list of tables to request from the sink.
    Subscribe []string
    // TLSSkipVerify disables TLS certificate verification. Never true in production.
    TLSSkipVerify bool
}

// Dialer manages one persistent outbound connection to a sink node.
//
// Call Run to start the reconnect loop. Send batches via SendBatch.
// Acks and Nacks are delivered via the Acks and Nacks channels.
type Dialer struct { ... }

// Run starts the connect-handshake-stream loop. Blocks until ctx is cancelled.
// On any error (dial failure, handshake rejection, read error), it waits
// with exponential backoff before reconnecting. Backoff: 1s, 2s, 4s, 8s, 16s, 32s, cap 60s.
func (d *Dialer) Run(ctx context.Context) error

// SendBatch enqueues a batch for delivery. Blocks if the connection is not yet
// established. Returns when the batch has been written to the WebSocket — not
// when it has been ACKed. The caller waits for an Ack via the Acks channel.
func (d *Dialer) SendBatch(ctx context.Context, batch ChangeBatch) error
```

---

## Listener (sink side)

```go
// ListenerConfig holds parameters for a sink-side inbound listener.
type ListenerConfig struct {
    // Address is the host:port to listen on (e.g. "0.0.0.0:8443").
    Address string
    // Published is the set of tables this node exposes. Used to validate Hello.
    Published map[string]bool
    // Handler is called for each validated inbound connection.
    Handler ConnHandler
    // TLSCert and TLSKey are optional. Empty = no TLS (Traefik terminates upstream).
    TLSCert string
    TLSKey  string
}

// ConnHandler is called once per accepted, handshaked connection.
// It receives batches via RecvBatch and must send Ack or Nack for each.
type ConnHandler func(ctx context.Context, nodeName string, conn *Conn)

// Listener accepts inbound WebSocket connections from source nodes.
type Listener struct { ... }

// Start begins accepting connections. Blocks until ctx is cancelled.
func (l *Listener) Start(ctx context.Context) error
```

---

## Conn

```go
// Conn wraps a gorilla/websocket connection with typed message send/receive.
//
// All methods are safe to call from a single goroutine only — callers must
// not call Send and Recv concurrently on the same Conn.
type Conn struct { ... }

func (c *Conn) Send(ctx context.Context, msg any) error
func (c *Conn) Recv(ctx context.Context) (any, error)  // returns typed message or error
func (c *Conn) Close() error
```

`Recv` deserialises the `type` field first, then unmarshals into the concrete type.
Returns a typed value (`Hello`, `HelloAck`, `ChangeBatch`, `Ack`, or `Nack`).
Unknown `type` values return an error.

---

## Reconnect backoff

Dialer uses the same `retryWithBackoff` pattern as the SQL Server connector:
delays `[]time.Duration{1, 2, 4, 8, 16, 32, 60}` seconds. Each attempt logs
the error, the attempt number, and the next backoff duration. Context cancellation
exits the loop immediately.

---

## TLS

The Dialer always dials with TLS (the sink is behind Traefik which terminates it,
so the Dialer dials `wss://`). `TLSSkipVerify` is available for local dev/testing
only and must never be set to true in production config.

The Listener's TLS is optional — empty `TLSCert`/`TLSKey` means plain `ws://`
(Traefik terminates upstream, consistent with the existing `[listen]` no-TLS mode
added in the Docker PRP).

---

## Tests to write (TDD order)

All tests use an in-process `net.Pipe()` — no real network or TLS required.

1. `TestConn_SendRecvHello` — encode Hello, decode on other end → correct fields
2. `TestConn_SendRecvChangeBatch` — encode ChangeBatch with 2 changes → correct decode
3. `TestConn_SendRecvAck` — encode Ack → correct decode
4. `TestConn_SendRecvNack` — encode Nack → correct decode
5. `TestConn_UnknownMessageType` — unknown type field → Recv returns error
6. `TestHandshake_Accepted` — Hello with valid subscribe → HelloAck{accepted: true}
7. `TestHandshake_Rejected` — Hello subscribes to unpublished table → HelloAck{accepted: false}
8. `TestDialer_SendBatchAck` — source sends batch, sink responds Ack → Ack delivered to caller
9. `TestDialer_SendBatchNack` — sink responds Nack → Nack delivered to caller
10. `TestDialer_ReconnectsOnDrop` — connection drops mid-stream → Dialer reconnects and resumes
11. `TestDialer_BackoffOnDialFailure` — sink not listening → Dialer retries with backoff, doesn't spin
12. `TestListener_MultipleConnections` — two source nodes connect simultaneously → both handled

---

## Open decisions logged

- **DECISION-012 (open):** Should `ChangeBatch` have a maximum size cap (e.g. 1000 changes
  or 4 MB) to prevent a single oversized batch from blocking the connection? Defer to
  pipeline engine PRP where batch assembly logic lives.

---

## Out of scope for this PRP

- Pipeline engine wiring (Reader → Dialer, Listener → Writer)
- SQLite state layer (cursor persistence)
- Pub/sub validation beyond the handshake table check
- Config propagation messages
- Management API connection status
- Fan-out (multiple outbound connections) — that's the pipeline engine
