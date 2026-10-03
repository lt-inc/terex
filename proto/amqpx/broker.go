package amqpx

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"terex/proto/telemetry"
)

// headerCarrier lets the OTel propagator read/write trace context in AMQP headers.
type headerCarrier amqp.Table

func (c headerCarrier) Get(k string) string {
	if v, ok := c[k].(string); ok {
		return v
	}
	return ""
}
func (c headerCarrier) Set(k, v string) { c[k] = v }
func (c headerCarrier) Keys() []string {
	keys := make([]string, 0, len(c))
	for k := range c {
		keys = append(keys, k)
	}
	return keys
}

// Broker owns a connection with auto-reconnect, publishes JSON and runs consumers.
type Broker struct {
	url string

	mu        sync.Mutex
	conn      *amqp.Connection
	pub       *amqp.Channel
	confirmed *amqp.Channel
	returns   <-chan amqp.Return
}

func New(url string) *Broker { return &Broker{url: url} }

func (b *Broker) connect() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil && !b.conn.IsClosed() && b.pub != nil && !b.pub.IsClosed() {
		return nil
	}
	conn, err := amqp.Dial(b.url)
	if err != nil {
		return err
	}
	ch, err := conn.Channel()
	if err != nil {
		conn.Close()
		return err
	}
	b.conn, b.pub = conn, ch
	return nil
}

// Connect retries until connected or ctx is done.
func (b *Broker) Connect(ctx context.Context) error {
	for {
		if err := b.connect(); err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

func (b *Broker) Declare(queue string) error {
	if err := b.connect(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	_, err := b.pub.QueueDeclare(queue, true, false, false, false, nil)
	return err
}

func (b *Broker) Publish(ctx context.Context, queue string, msg any) (err error) {
	ctx, span := otel.Tracer("terex").Start(ctx, "publish "+queue, trace.WithSpanKind(trace.SpanKindProducer),
		trace.WithAttributes(attribute.String("messaging.system", "rabbitmq"), attribute.String("messaging.destination.name", queue)))
	defer func() { telemetry.End(span, err) }()

	body, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	if err = b.connect(); err != nil {
		return err
	}
	headers := amqp.Table{}
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier(headers))

	b.mu.Lock()
	defer b.mu.Unlock()
	err = b.pub.PublishWithContext(ctx, "", queue, false, false, amqp.Publishing{
		ContentType:  "application/json",
		DeliveryMode: amqp.Persistent,
		Headers:      headers,
		Body:         body,
	})
	if err == nil {
		slog.DebugContext(ctx, "message published", "queue", queue, "bytes", len(body))
	}
	return err
}

// PublishConfirmed is used by the workflow outbox. Delivery is recorded in
// PostgreSQL only after RabbitMQ confirms the durable message. An ambiguous
// failure may produce another notification; the coordinator deduplicates it.
func (b *Broker) PublishConfirmed(ctx context.Context, queue string, value any) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if err = b.connect(); err != nil {
		return err
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.confirmed == nil || b.confirmed.IsClosed() {
		b.confirmed, err = b.conn.Channel()
		if err != nil {
			return err
		}
		if err = b.confirmed.Confirm(false); err != nil {
			_ = b.confirmed.Close()
			return err
		}
		b.returns = b.confirmed.NotifyReturn(make(chan amqp.Return, 1))
	}
	// Declare on the same channel so a missing route cannot silently lose work.
	if _, err = b.confirmed.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return err
	}
	headers := amqp.Table{}
	otel.GetTextMapPropagator().Inject(ctx, headerCarrier(headers))
	confirmation, err := b.confirmed.PublishWithDeferredConfirmWithContext(ctx, "", queue, true, false, amqp.Publishing{
		ContentType: "application/json", DeliveryMode: amqp.Persistent, Headers: headers, Body: body,
	})
	if err != nil {
		return err
	}
	acked, err := confirmation.WaitContext(ctx)
	if err != nil {
		_ = b.confirmed.Close()
		return err
	}
	if !acked {
		return fmt.Errorf("broker rejected workflow command")
	}
	select {
	case returned := <-b.returns:
		return fmt.Errorf("workflow command unroutable: %d %s", returned.ReplyCode, returned.ReplyText)
	default:
		return nil
	}
}

// Handler returns nil to ack; error -> nack without requeue (dead-letter / drop). Use
// ErrRetry-like wrapping if requeue is wanted.
type Handler func(ctx context.Context, body []byte) error

// Consume blocks, reconnecting every 5s, with `workers` concurrent handlers (prefetch = workers).
func (b *Broker) Consume(ctx context.Context, queue string, workers int, h Handler) error {
	for ctx.Err() == nil {
		if err := b.consumeOnce(ctx, queue, workers, h); err != nil && ctx.Err() == nil {
			slog.Warn("consumer stopped, reconnecting", "queue", queue, "err", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(5 * time.Second):
		}
	}
	return ctx.Err()
}

func (b *Broker) consumeOnce(ctx context.Context, queue string, workers int, h Handler) error {
	conn, err := amqp.Dial(b.url)
	if err != nil {
		return err
	}
	defer conn.Close()
	ch, err := conn.Channel()
	if err != nil {
		return err
	}
	if _, err := ch.QueueDeclare(queue, true, false, false, false, nil); err != nil {
		return err
	}
	if err := ch.Qos(workers, 0, false); err != nil {
		return err
	}
	msgs, err := ch.Consume(queue, "", false, false, false, false, nil)
	if err != nil {
		return err
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	defer wg.Wait()
	for {
		select {
		case <-ctx.Done():
			return nil
		case d, ok := <-msgs:
			if !ok {
				return fmt.Errorf("delivery channel closed")
			}
			sem <- struct{}{}
			wg.Add(1)
			go func(d amqp.Delivery) {
				defer func() { <-sem; wg.Done() }()
				// continue the producer's trace: trace context travels in the AMQP headers
				mctx := otel.GetTextMapPropagator().Extract(ctx, headerCarrier(d.Headers))
				mctx, span := otel.Tracer("terex").Start(mctx, "consume "+queue, trace.WithSpanKind(trace.SpanKindConsumer),
					trace.WithAttributes(attribute.String("messaging.system", "rabbitmq"), attribute.String("messaging.destination.name", queue)))
				err := h(mctx, d.Body)
				telemetry.End(span, err)
				if err != nil {
					_ = d.Nack(false, false)
					return
				}
				_ = d.Ack(false)
			}(d)
		}
	}
}

func (b *Broker) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.conn != nil {
		b.conn.Close()
	}
}
