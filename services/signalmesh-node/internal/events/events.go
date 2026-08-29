package events

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

// Bus is a lightweight NATS abstraction for SignalMesh events.
type Bus struct {
	conn   *nats.Conn
	logger *slog.Logger
}

// Connect connects to NATS.
func Connect(url string, logger *slog.Logger) (*Bus, error) {
	nc, err := nats.Connect(
		url,
		nats.Name("signalmesh-node"),
		nats.Timeout(3*time.Second),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(1*time.Second),
	)
	if err != nil {
		return nil, err
	}

	return &Bus{
		conn:   nc,
		logger: logger,
	}, nil
}

// Publish marshals v to JSON and publishes it to subject.
func (b *Bus) Publish(subject string, v any) error {
	if b == nil || b.conn == nil {
		return nil
	}

	data, err := json.Marshal(v)
	if err != nil {
		return err
	}

	return b.conn.Publish(subject, data)
}

// Subscribe registers a handler for a subject.
func (b *Bus) Subscribe(subject string, handler func(data []byte)) error {
	if b == nil || b.conn == nil {
		return nil
	}

	_, err := b.conn.Subscribe(subject, func(msg *nats.Msg) {
		handler(msg.Data)
	})

	return err
}

// Close closes the NATS connection.
func (b *Bus) Close() {
	if b != nil && b.conn != nil {
		b.conn.Close()
	}
}

// NewID creates a lightweight unique event ID.
func NewID(prefix string) string {
	buf := make([]byte, 8)

	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}

	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(buf))
}
