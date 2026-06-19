// Package transport implements the node-to-node WebSocket wire protocol.
//
// Messages flow from source to sink as ChangeBatch frames; the sink replies
// with Ack or Nack per batch. A Hello/HelloAck handshake runs once when
// each connection opens to identify the source and validate subscriptions.
//
// Depends on: connector
// Used by: pipeline engine
package transport

import (
	"encoding/json"
	"fmt"

	"github.com/massa-platform/tetherdb/internal/connector"
)

// Hello is sent by the source immediately after the WebSocket connection opens.
// It identifies the source node and declares which tables it wants to stream.
//
// Example:
//
//	msg := Hello{Type: "hello", NodeName: "node-a", Subscribe: []string{"dbo.Orders"}}
type Hello struct {
	Type      string   `json:"type"`
	NodeName  string   `json:"node_name"`
	Subscribe []string `json:"subscribe"`
}

// HelloAck is sent by the sink in response to Hello.
// Rejected connections include a human-readable reason; the sink closes
// the connection immediately after sending a rejection.
//
// Example:
//
//	ack := HelloAck{Type: "hello_ack", Accepted: true}
type HelloAck struct {
	Type     string `json:"type"`
	Accepted bool   `json:"accepted"`
	Reason   string `json:"reason,omitempty"`
}

// ChangeBatch carries a slice of changes from source to sink.
// BatchID is a monotonically increasing uint64, unique per connection.
//
// Example:
//
//	batch := ChangeBatch{Type: "change_batch", BatchID: 1, Changes: changes}
type ChangeBatch struct {
	Type    string             `json:"type"`
	BatchID uint64             `json:"batch_id"`
	Changes []connector.Change `json:"changes"`
}

// Ack is sent by the sink after successfully applying a ChangeBatch.
//
// Example:
//
//	ack := Ack{Type: "ack", BatchID: 1}
type Ack struct {
	Type    string `json:"type"`
	BatchID uint64 `json:"batch_id"`
}

// Nack is sent by the sink when a ChangeBatch could not be applied.
// The source must retry the same batch (same BatchID, same changes).
//
// Example:
//
//	nack := Nack{Type: "nack", BatchID: 1, Reason: "upsert failed: ..."}
type Nack struct {
	Type    string `json:"type"`
	BatchID uint64 `json:"batch_id"`
	Reason  string `json:"reason"`
}

// typeEnvelope is used to peek the type field before full unmarshalling.
type typeEnvelope struct {
	Type string `json:"type"`
}

// decode parses raw JSON into the concrete message type identified by the "type" field.
// Returns an error if the type field is missing, unknown, or the payload is malformed.
func decode(data []byte) (any, error) {
	var env typeEnvelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("transport: decode envelope: %w", err)
	}
	switch env.Type {
	case "hello":
		var m Hello
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("transport: decode hello: %w", err)
		}
		return m, nil
	case "hello_ack":
		var m HelloAck
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("transport: decode hello_ack: %w", err)
		}
		return m, nil
	case "change_batch":
		var m ChangeBatch
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("transport: decode change_batch: %w", err)
		}
		return m, nil
	case "ack":
		var m Ack
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("transport: decode ack: %w", err)
		}
		return m, nil
	case "nack":
		var m Nack
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, fmt.Errorf("transport: decode nack: %w", err)
		}
		return m, nil
	default:
		return nil, fmt.Errorf("transport: unknown message type %q", env.Type)
	}
}
