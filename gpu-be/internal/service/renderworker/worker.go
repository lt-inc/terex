package renderworker

import (
	"context"
	"errors"
	"fmt"
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
}

var (
	errRenderLeaseDenied      = errors.New("render lease denied")
	errRenderLeaseRenewFailed = errors.New("renew render lease")
)

// A failed renewal must stop rendering immediately, but retain the dependency
// error so a cancelled ffmpeg process does not hide why the work was stopped.
func (w *Worker) renewRenderLease(ctx context.Context, cancel context.CancelCauseFunc, q *workflow.LeaseRequest) bool {
	p, err := w.Coordinator.Renew(ctx, q)
	if err != nil {
		cancel(fmt.Errorf("%w: %w", errRenderLeaseRenewFailed, err))
		return false
	}
	if p == nil {
		cancel(fmt.Errorf("%w: coordinator returned no permit", errRenderLeaseRenewFailed))
		return false
	}
	if !p.Allowed {
		cancel(errRenderLeaseDenied)
		return false
	}
	return true
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
	go func() {
		defer close(done)
		ticker := time.NewTicker(10 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !w.renewRenderLease(ctx, cancel, &q) {
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
		r, e := w.Coordinator.Complete(ctx, &workflow.CompleteRequest{LeaseRequest: q, Checkpoint: workflow.Checkpoint{State: "rendered"}, Media: media, Source: source})
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
