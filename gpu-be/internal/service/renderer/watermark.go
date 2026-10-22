package renderer

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/watermark"
	"terex/proto/msg"
)

// maskWatermark blurs the watermark of the video at in and returns the file the next step must use: the
// regions the operator drew (manual) or the ones the detector finds in this very video (auto). In auto
// mode a video without an overlay is a normal outcome and passes through untouched. A job that asks for
// the blur but cannot get it fails: publishing the watermark the operator wanted gone is the worse result.
func (s *Service) maskWatermark(ctx context.Context, dir, in string, opts *domain.WatermarkOptions) (string, error) {
	if opts == nil {
		return in, nil
	}
	opts, err := opts.Normalize()
	if err != nil {
		return "", err
	}
	if opts == nil {
		return in, nil
	}
	media, ok := s.Media.(domain.WatermarkMedia)
	if !ok {
		return "", fmt.Errorf("watermark: media client cannot blur regions")
	}
	regions := opts.Regions
	if opts.Mode == msg.WatermarkAuto {
		if regions, err = watermark.DetectFile(ctx, media, in); err != nil {
			return "", err
		}
		if len(regions) == 0 {
			slog.InfoContext(ctx, "watermark not found, video left as it is", append(domain.MediaLogAttrs(ctx), "mode", opts.Mode)...)
			return in, nil
		}
	}
	out := filepath.Join(dir, "unmarked.mp4")
	if err = media.BlurRegions(ctx, in, out, regions); err != nil {
		return "", fmt.Errorf("watermark: %w", err)
	}
	slog.InfoContext(ctx, "watermark blurred", append(domain.MediaLogAttrs(ctx), "mode", opts.Mode, "regions", len(regions))...)
	_ = os.Remove(in) // the unblurred copy is not needed again; scratch space is budgeted
	return out, nil
}
