// Package reporter records stage status locally and notifies main-be via the broker.
package reporter

import (
	"context"
	"log/slog"

	"terex/gpu-be/internal/domain"
)

type Reporter struct {
	State  domain.JobState
	Broker domain.Broker
}

func (r *Reporter) Report(ctx context.Context, jobID, stage, status, failPlace string) {
	st := domain.JobStatus{JobID: jobID, Stage: stage, Status: status, FailPlace: failPlace}
	lvl := slog.LevelInfo
	attrs := []any{"job_id", jobID, "stage", stage, "status", status}
	if status == "failed" {
		lvl = slog.LevelError
		attrs = append(attrs, "fail_place", failPlace) // only meaningful (and shown) when the stage failed
	}
	slog.Log(ctx, lvl, "job stage", attrs...)
	_ = r.State.SetStatus(ctx, st)
	_ = r.Broker.Publish(ctx, domain.QueueJobStatus, st)
}
