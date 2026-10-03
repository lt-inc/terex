package amqpx

import (
	"context"
	"encoding/json"
	"fmt"
	amqp "github.com/rabbitmq/amqp091-go"
	"os"
	"testing"
	"time"
)

func TestConfirmedOutboxDelivery(t *testing.T) {
	url := os.Getenv("TEST_AMQP_URL")
	if url == "" {
		t.Skip("TEST_AMQP_URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	b := New(url)
	if err := b.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	q := fmt.Sprintf("terex.workflow.test.%d", time.Now().UnixNano())
	conn, err := amqp.Dial(url)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		t.Fatal(err)
	}
	defer ch.Close()
	defer func() { _, _ = ch.QueueDelete(q, false, false, false) }()
	value := map[string]any{"publication_id": 42, "attempt_id": "same-command"}
	// Duplicates are deliberately possible after an ambiguous DB write. Both
	// notifications must survive, leaving deduplication to the coordinator.
	for i := 0; i < 2; i++ {
		if err = b.PublishConfirmed(ctx, q, value); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 2; i++ {
		d, ok, err := ch.Get(q, false)
		if err != nil || !ok {
			t.Fatalf("delivery %d: ok=%v err=%v", i, ok, err)
		}
		if d.DeliveryMode != amqp.Persistent {
			t.Fatal("outbox command is not persistent")
		}
		var got map[string]any
		if err = json.Unmarshal(d.Body, &got); err != nil {
			t.Fatal(err)
		}
		if got["attempt_id"] != "same-command" {
			t.Fatalf("wrong payload %s", d.Body)
		}
		if err = d.Ack(false); err != nil {
			t.Fatal(err)
		}
	}
}
