package renderworker

import (
	"context"
	"errors"
	"fmt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"log/slog"
	"terex/gpu-be/internal/domain"
	"time"

	"terex/gpu-be/internal/service/renderer"
	"terex/gpu-be/internal/service/resetgate"
	"terex/proto/workflow"
)

type Worker struct {
	Reset       *resetgate.Gate
	Coordinator workflow.Coordinator
	Renderer    *renderer.Service
	WorkerID    string
	leaseNow    func() time.Time
	leaseWait   func(context.Context, time.Duration) error
}

var (
	errRenderLeaseDenied      = errors.New("render lease denied")
	errRenderLeaseRenewFailed = errors.New("renew render lease")
)

// Retry transport failures only inside the last confirmed lease. The safety
// margin also bounds the RPC itself, so a hung renewal cannot outlive its fence.
func (w *Worker) renewRenderLease(ctx context.Context, cancel context.CancelCauseFunc, q *workflow.LeaseRequest, confirmed ...*time.Time) bool {
	now := time.Now
	if w.leaseNow != nil {
		now = w.leaseNow
	}
	wait := func(ctx context.Context, d time.Duration) error {
		t := time.NewTimer(d)
		defer t.Stop()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			return nil
		}
	}
	if w.leaseWait != nil {
		wait = w.leaseWait
	}
	var expiry *time.Time
	if len(confirmed) > 0 {
		expiry = confirmed[0]
	}
	delays := []time.Duration{time.Second, 2 * time.Second, 5 * time.Second}
	var lastErr error
	for attempt := 0; ; attempt++ {
		if ctx.Err() != nil {
			return false
		}
		callCtx := ctx
		callCancel := func() {}
		if expiry != nil {
			remaining := expiry.Sub(now()) - 5*time.Second
			if remaining <= 0 {
				if lastErr == nil {
					lastErr = errors.New("last confirmed render lease expired")
				}
				cancel(fmt.Errorf("%w: %w", errRenderLeaseRenewFailed, lastErr))
				return false
			}
			callCtx, callCancel = context.WithTimeout(ctx, min(5*time.Second, remaining))
		}
		p, err := w.Coordinator.Renew(callCtx, q)
		callCancel()
		if ctx.Err() != nil {
			return false
		}
		if err == nil && p != nil && !p.Allowed {
			cancel(errRenderLeaseDenied)
			return false
		}
		if err == nil && p != nil && p.Allowed {
			if expiry != nil {
				if !p.ExpiresAt.After(now().Add(5 * time.Second)) {
					cancel(fmt.Errorf("%w: invalid renewed expiry", errRenderLeaseRenewFailed))
					return false
				}
				*expiry = p.ExpiresAt
			}
			return true
		}
		if err == nil {
			cancel(fmt.Errorf("%w: coordinator returned no permit", errRenderLeaseRenewFailed))
			return false
		}
		lastErr = err
		c := status.Code(err)
		// Unknown includes dependency failures returned by older coordinators.
		// Permanent protocol errors must not consume the remaining lease.
		transient := c == codes.Unavailable || c == codes.DeadlineExceeded || c == codes.ResourceExhausted || c == codes.Unknown
		if expiry == nil || !transient {
			cancel(fmt.Errorf("%w: %w", errRenderLeaseRenewFailed, err))
			return false
		}
		delay := delays[min(attempt, len(delays)-1)]
		remaining := expiry.Sub(now()) - 5*time.Second
		if remaining <= 0 {
			cancel(fmt.Errorf("%w: %w", errRenderLeaseRenewFailed, err))
			return false
		}
		slog.WarnContext(ctx, "render lease renewal retry", "attempt_id", q.AttemptID, "retry_after_ms", delay.Milliseconds(), "confirmed_expiry", *expiry, "err", err)
		if e := wait(ctx, min(delay, remaining)); e != nil {
			return false
		}
	}
}

func renderCancellationError(ctx context.Context) error {
	cause := context.Cause(ctx)
	if errors.Is(cause, errRenderLeaseRenewFailed) {
		return cause
	}
	// Parent shutdown and an explicitly denied lease remain expected ACKs. The
	// first cancellation cause also survives a later parent shutdown.
	return nil
}

func (w *Worker) Handle(ctx context.Context, c workflow.Command) error {
	l, err := w.Coordinator.Claim(ctx, &workflow.ClaimRequest{Command: c, WorkerID: w.WorkerID})
	if err != nil {
		return err
	}
	if !l.Granted {
		return nil
	}
	if l.Render == nil {
		return fmt.Errorf("coordinator returned no render revision")
	}
	if l.Render.Twitch {
		var release func()
		ctx, release, err = w.Reset.Begin(ctx, l.Render.Epoch)
		if err != nil {
			return err
		}
		defer release()
	}
	q := workflow.LeaseRequest{AttemptID: l.AttemptID, LeaseToken: l.LeaseToken, ExpectedVersion: l.ExpectedVersion}
	ctx = domain.WithMediaAttempt(ctx, l.AttemptID, l.ExpectedVersion)
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	done := make(chan struct{})
	confirmedExpiry := l.ExpiresAt
	go func() {
		defer close(done)
		for {
			delay := min(10*time.Second, time.Until(confirmedExpiry)-5*time.Second)
			if delay < 0 {
				delay = 0
			}
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				return
			case <-timer.C:
				if !w.renewRenderLease(ctx, cancel, &q, &confirmedExpiry) {
					return
				}
			}
		}
	}()
	renderCtx := ctx
	if l.Render.Source.Assembly != nil {
		var release func()
		renderCtx, release, err = w.Renderer.BeginAssembly(ctx)
		if err == nil {
			defer release()
		} else {
			renderCtx = ctx
		}
	}
	work := *l.Render
	var prepared *renderer.PreparedRender
	if err == nil {
		prepared, err = w.Renderer.PrepareRender(renderCtx, work)
		if err == nil {
			work = prepared.Work
			defer prepared.Close()
		}
	}
	var media workflow.ObjectRef
	if err == nil {
		media, err = w.Renderer.RenderPrepared(renderCtx, prepared)
	}
	if budgetErr := renderer.CheckAssemblyBudget(renderCtx); budgetErr != nil {
		err = budgetErr
	}
	if ctx.Err() != nil {
		<-done
		return renderCancellationError(ctx)
	}
	if err == nil {
		var source *workflow.SourceManifest
		if work.Source.Assembly != nil && work.Source.Original != l.Render.Source.Original {
			source = &work.Source
		}
		r, e := w.Coordinator.Complete(ctx, &workflow.CompleteRequest{LeaseRequest: q, Checkpoint: workflow.Checkpoint{State: "rendered"}, Media: media, Source: source, MediaSHA256: prepared.MediaSHA256, MediaSizeBytes: prepared.MediaSizeBytes})
		err = e
		if e == nil && !r.Accepted {
			err = fmt.Errorf("render lease expired before completion")
		}
	} else {
		code := "render_failed"
		retryable := errors.Is(err, domain.ErrMediaResourceWait)
		if retryable {
			code = "resource_wait"
		}
		if errors.Is(err, renderer.ErrAssemblyBudget) {
			code = "assembly_budget_exceeded"
			retryable = true
		}
		slog.ErrorContext(ctx, "revision render failed", "stage", "render", "reason", code, "clip_id", work.ClipID, "revision_id", work.RevisionID, "err", err)
		state := "failed"
		if retryable {
			state = "retry_wait"
		}
		_, reportErr := w.Coordinator.Fail(ctx, &workflow.FailRequest{LeaseRequest: q, State: state, Code: code, Message: err.Error(), Retryable: retryable, RetryAfterSeconds: 60})
		if reportErr != nil {
			err = errors.Join(err, fmt.Errorf("report render failure: %w", reportErr))
		} else {
			// The terminal failure was persisted; preserve the existing ACK while
			// the log above retains the original render error.
			err = nil
		}
	}
	cancel(nil)
	<-done
	if renewalErr := renderCancellationError(ctx); renewalErr != nil {
		return errors.Join(err, renewalErr)
	}
	return err
}
