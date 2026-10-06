package renderworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"terex/gpu-be/internal/service/renderer"
	"terex/proto/workflow"
)

type Worker struct {
	Coordinator workflow.Coordinator
	Renderer    *renderer.Service
	WorkerID    string
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
	q := workflow.LeaseRequest{AttemptID: l.AttemptID, LeaseToken: l.LeaseToken, ExpectedVersion: l.ExpectedVersion}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
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
				p, e := w.Coordinator.Renew(ctx, &q)
				if e != nil || !p.Allowed {
					cancel()
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
	if err == nil {
		work, err = w.Renderer.MaterializeAssembly(renderCtx, work)
	}
	var media workflow.ObjectRef
	if err == nil {
		media, err = w.Renderer.RenderRevision(renderCtx, work)
	}
	if budgetErr := renderer.CheckAssemblyBudget(renderCtx); budgetErr != nil {
		err = budgetErr
	}
	if ctx.Err() != nil {
		cancel()
		<-done
		return nil
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
		if errors.Is(err, renderer.ErrAssemblyBudget) {
			code = "assembly_budget_exceeded"
		}
		slog.ErrorContext(ctx, "revision render failed", "stage", "render", "reason", code, "clip_id", work.ClipID, "revision_id", work.RevisionID, "err", err)
		_, err = w.Coordinator.Fail(ctx, &workflow.FailRequest{LeaseRequest: q, State: "failed", Code: code, Message: err.Error(), Retryable: false})
	}
	cancel()
	<-done
	return err
}
