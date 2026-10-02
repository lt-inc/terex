// Package preproc: download the YouTube video and create video/audio proxies.
package preproc

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/reporter"
	"terex/proto/telemetry"
)

const stage = "preproc"

type Service struct {
	Store  domain.ObjectStore
	Dl     domain.Downloader
	Media  domain.Media
	Broker domain.Broker
	Rep    *reporter.Reporter
	TmpDir string
}

func (s *Service) Handle(ctx context.Context, msg domain.ProcessVideo) (err error) {
	ctx, span := telemetry.Start(ctx, stage+" handle", attribute.String("job.id", msg.JobID))
	defer func() { telemetry.End(span, err) }()
	s.Rep.Report(ctx, msg.JobID, stage, "running", "")
	defer func() {
		if err != nil {
			s.Rep.Report(ctx, msg.JobID, stage, "failed", stage)
		}
	}()

	if err = os.MkdirAll(s.TmpDir, 0o755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(s.TmpDir, msg.JobID+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	src := filepath.Join(dir, msg.JobID+".mp4")
	if err = s.Dl.Download(ctx, msg.YTURL, src); err != nil {
		return fmt.Errorf("download: %w", err)
	}
	if err = s.Store.Upload(ctx, domain.ObjectRef{Bucket: domain.BucketDownloads, Object: msg.JobID + ".mp4"}, src); err != nil {
		return err
	}

	pv := filepath.Join(dir, "proxy.mp4")
	if err = s.Media.VideoProxy(ctx, src, pv, 480); err != nil {
		return err
	}
	if err = s.Store.Upload(ctx, domain.ObjectRef{Bucket: domain.BucketProxy, Object: msg.JobID + ".mp4"}, pv); err != nil {
		return err
	}

	pa := filepath.Join(dir, "proxy.m4a")
	if err = s.Media.AudioProxy(ctx, src, pa); err != nil {
		return err
	}
	if err = s.Store.Upload(ctx, domain.ObjectRef{Bucket: domain.BucketProxy, Object: msg.JobID + ".m4a"}, pa); err != nil {
		return err
	}

	s.Rep.Report(ctx, msg.JobID, stage, "done", "")
	return s.Broker.Publish(ctx, domain.QueueCandidate, msg)
}
