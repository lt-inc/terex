package ffmpeg

import (
	"context"
	"errors"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
)

func TestMediaSlotsBoundVerificationAndReleaseAfterCancellation(t *testing.T) {
	c := New(false)
	c.VerificationWait = time.Millisecond
	release, err := c.acquire(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.acquire(context.Background(), true); !errors.Is(err, domain.ErrMediaResourceWait) {
		t.Fatalf("busy verification is not retryable: %v", err)
	}
	// Verification remains independent of rendering; cancellation cannot take
	// or leak the occupied encoding slot.
	encode, err := c.AcquireEncoding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = c.AcquireEncoding(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	encode()
	release()
	encode, err = c.AcquireEncoding(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	encode()
}
