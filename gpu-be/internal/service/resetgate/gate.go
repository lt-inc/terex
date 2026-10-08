package resetgate

import (
	"context"
	"log/slog"
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
	draining    bool
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
	reason := ""
	switch {
	case !g.initialized:
		reason = "control_plane_not_synchronized"
	case epoch != g.epoch:
		reason = "epoch_mismatch"
	case g.draining:
		reason = "writers_draining"
	case g.active:
		reason = "reset_maintenance"
	}
	if reason != "" {
		slog.WarnContext(ctx, "twitch writer fenced", "reason", reason, "requested_epoch", epoch, "current_epoch", g.epoch, "gate_closed", g.active, "active_writers", len(g.writers))
		return nil, nil, status.Errorf(codes.FailedPrecondition, "Twitch writer fenced: reason=%s requested_epoch=%d current_epoch=%d active_writers=%d", reason, epoch, g.epoch, len(g.writers))
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
		slog.WarnContext(ctx, "twitch reset synchronization rejected", "reason", "stale_epoch", "requested_epoch", q.Epoch, "current_epoch", g.epoch)
		return nil, status.Errorf(codes.FailedPrecondition, "stale reset epoch: requested_epoch=%d current_epoch=%d", q.Epoch, g.epoch)
	}
	previousEpoch, previousActive, previousDraining, initialized := g.epoch, g.active, g.draining, g.initialized
	if q.Active || q.Epoch != g.epoch {
		for _, cancel := range g.writers {
			cancel()
		}
		g.draining = len(g.writers) > 0
	} else if len(g.writers) == 0 {
		g.draining = false
	}
	// Never reopen while a cancelled writer is still unwinding. The next tick
	// repeats this idempotent synchronization after all writers have returned.
	// Healthy writers during an inactive heartbeat do not close the gate.
	g.epoch = q.Epoch
	g.initialized = true
	g.active = q.Active || g.draining
	if !initialized || previousEpoch != g.epoch || previousActive != g.active || previousDraining != g.draining {
		slog.InfoContext(ctx, "twitch reset gate synchronized", "epoch", g.epoch, "maintenance_active", q.Active, "gate_closed", g.active, "writers_draining", g.draining, "active_writers", len(g.writers))
	}
	return &workflow.TwitchMaintenanceResult{Drained: len(g.writers) == 0, ActiveWriters: len(g.writers)}, nil
}
