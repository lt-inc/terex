package resetgate

import (
	"context"
	"sync"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"terex/proto/workflow"
)

// Gate tracks entire Twitch writer lifetimes, including cancelled ffmpeg and
// uploads until their callers actually return. It starts closed after a GPU
// restart, and the control plane synchronizes its durable epoch before opening.
type Gate struct {
	mu          sync.Mutex
	epoch       int64
	active      bool
	initialized bool
	next        uint64
	writers     map[uint64]context.CancelFunc
}

func New() *Gate { return &Gate{active: true, writers: map[uint64]context.CancelFunc{}} }
func (g *Gate) Begin(ctx context.Context, epoch int64) (context.Context, func(), error) {
	if g == nil {
		return ctx, func() {}, nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.initialized || g.active || epoch != g.epoch {
		return nil, nil, status.Error(codes.FailedPrecondition, "Twitch writer is fenced by reset maintenance or an old epoch")
	}
	ctx, cancel := context.WithCancel(ctx)
	g.next++
	id := g.next
	g.writers[id] = cancel
	var once sync.Once
	release := func() { once.Do(func() { cancel(); g.mu.Lock(); delete(g.writers, id); g.mu.Unlock() }) }
	return ctx, release, nil
}
func (g *Gate) SetTwitchMaintenance(ctx context.Context, q *workflow.TwitchMaintenanceRequest) (*workflow.TwitchMaintenanceResult, error) {
	if q == nil || q.Epoch < 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid reset epoch")
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.initialized && q.Epoch < g.epoch {
		return nil, status.Error(codes.FailedPrecondition, "stale reset epoch")
	}
	if q.Active || q.Epoch != g.epoch {
		for _, cancel := range g.writers {
			cancel()
		}
	}
	// Never reopen while a cancelled writer is still unwinding. The next tick
	// repeats this idempotent synchronization after all writers have returned.
	g.epoch = q.Epoch
	g.initialized = true
	g.active = q.Active || len(g.writers) > 0
	return &workflow.TwitchMaintenanceResult{Drained: len(g.writers) == 0, ActiveWriters: len(g.writers)}, nil
}
