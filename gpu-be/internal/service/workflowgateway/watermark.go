package workflowgateway

import (
	"context"
	"fmt"
	"os"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/mediatmp"
	"terex/gpu-be/internal/service/watermark"
	"terex/proto/workflow"
)

// PreviewDownloader fetches the start of a YouTube video (no audio, capped resolution) into dir.
type PreviewDownloader interface {
	DownloadPreview(ctx context.Context, url, dir string) (string, error)
}

const (
	defaultWatermarkFrames = 6
	maxWatermarkFrames     = 12
)

// WatermarkFrames gives the region editor what it needs: stills of the video to draw on, and the areas the
// automatic detector would blur, as a starting point. Nothing is stored: the stills go back inline.
func (s *Service) WatermarkFrames(ctx context.Context, q *workflow.WatermarkFramesRequest) (*workflow.WatermarkFramesResponse, error) {
	if s.Previews == nil {
		return nil, fmt.Errorf("watermark frames are not configured")
	}
	media, ok := s.Media.(domain.WatermarkMedia)
	if !ok {
		return nil, fmt.Errorf("watermark frames: media client cannot sample frames")
	}
	link, err := youtubeURL(q.URL)
	if err != nil {
		return nil, err
	}
	count := q.Count
	if count <= 0 {
		count = defaultWatermarkFrames
	}
	count = min(count, maxWatermarkFrames)
	ctx, release, err := mediatmp.Begin(ctx, s.Resources)
	if err != nil {
		return nil, err
	}
	defer release()
	dir, cleanup, err := mediatmp.MkdirTemp(ctx, s.Resources, s.TmpDir, "watermark-")
	if err != nil {
		return nil, err
	}
	defer cleanup()
	file, err := s.Previews.DownloadPreview(ctx, link, dir)
	if err != nil {
		return nil, err
	}
	probe, ok := s.Media.(interface {
		Probe(context.Context, string) (domain.MediaProbe, error)
	})
	if !ok {
		return nil, fmt.Errorf("watermark frames: media client cannot probe")
	}
	p, err := probe.Probe(ctx, file)
	if err != nil {
		return nil, err
	}
	stills, err := media.StillFrames(ctx, file, dir, count)
	if err != nil {
		return nil, err
	}
	if len(stills) == 0 {
		return nil, fmt.Errorf("watermark frames: no frames could be read from the video")
	}
	suggested, err := watermark.DetectFile(ctx, media, file)
	if err != nil {
		return nil, err
	}
	resp := &workflow.WatermarkFramesResponse{Width: p.Width, Height: p.Height, Suggested: suggested}
	if resp.Suggested == nil {
		resp.Suggested = []domain.WatermarkRegion{}
	}
	for _, f := range stills {
		resp.Frames = append(resp.Frames, workflow.WatermarkFrame{TimeSec: f.TimeSec, JPEG: f.JPEG})
	}
	_ = os.Remove(file)
	return resp, nil
}
