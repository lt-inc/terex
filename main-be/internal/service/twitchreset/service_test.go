package twitchreset

import (
	"context"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"terex/main-be/internal/domain"
	"terex/proto/twitch"
	"terex/proto/workflow"
)

type resetRepo struct {
	domain.TwitchResetRepo
	control           twitch.ResetStatus
	fullReads, failed int
}

func (r *resetRepo) TwitchResetControl(context.Context) (twitch.ResetStatus, error) {
	return r.control, nil
}
func (r *resetRepo) TwitchReset(context.Context) (twitch.ResetStatus, error) {
	r.fullReads++
	return r.control, nil
}
func (r *resetRepo) FailTwitchReset(context.Context, string, string, time.Time) error {
	r.failed++
	return nil
}

type resetGPU struct {
	err   error
	calls []workflow.TwitchMaintenanceRequest
}

func (g *resetGPU) SetTwitchMaintenance(_ context.Context, q *workflow.TwitchMaintenanceRequest) (*workflow.TwitchMaintenanceResult, error) {
	g.calls = append(g.calls, *q)
	return &workflow.TwitchMaintenanceResult{Drained: true}, g.err
}

func TestIdleSyncBackoffStillForcesEpochAndActiveReset(t *testing.T) {
	now := time.Unix(1000, 0)
	r := &resetRepo{control: twitch.ResetStatus{Epoch: 1, State: "idle"}}
	g := &resetGPU{err: status.Error(codes.Unavailable, "GPU node offline")}
	s := New(r, nil, g)
	s.Now = func() time.Time { return now }
	for _, elapsed := range []time.Duration{0, 5 * time.Second, 15 * time.Second, 40 * time.Second, 45 * time.Second, 100 * time.Second, 105 * time.Second} {
		now = time.Unix(1000, 0).Add(elapsed)
		if err := s.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if len(g.calls) != 4 || r.fullReads != 0 {
		t.Fatalf("idle backoff calls=%d, heavy reads=%d", len(g.calls), r.fullReads)
	}
	// A new epoch cannot wait for the prior offline backoff, and reconnect
	// must actually deliver that epoch before the GPU gate can open.
	r.control.Epoch = 2
	g.err = nil
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(g.calls) != 5 || g.calls[4].Epoch != 2 || g.calls[4].Active {
		t.Fatal("new epoch was not synchronized")
	}
	r.control.State, r.control.ID = "stopping", "reset-2"
	g.err = status.Error(codes.Unavailable, "offline during active reset")
	if err := s.Tick(context.Background()); status.Code(err) != codes.Unavailable {
		t.Fatalf("active reset error hidden: %v", err)
	}
	if len(g.calls) != 6 || !g.calls[5].Active || r.failed != 1 {
		t.Fatal("active reset must immediately fence and retain failure")
	}
}
