// Package renderer: vertical conversion, subtitles, banner and slicing into clips.
package renderer

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/reporter"
	"terex/proto/telemetry"
)

const stage = "renderer"

type Service struct {
	Store  domain.ObjectStore
	Media  domain.Media
	Broker domain.Broker
	State  domain.JobState
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
	o := msg.Options
	rect := o.Shape == "rectangle"

	if err = os.MkdirAll(s.TmpDir, 0o755); err != nil {
		return err
	}
	dir, err := os.MkdirTemp(s.TmpDir, msg.JobID+"-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	cur := filepath.Join(dir, "src.mp4")
	if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketDownloads, Object: msg.JobID + ".mp4"}, cur); err != nil {
		return err
	}

	if o.Vertical {
		spec := domain.VerticalSpec{Width: 1080, Blur: 40, Circle: o.Shape == "circle"}
		if o.BackgroundID != "" {
			spec.BgPath = filepath.Join(dir, "bg"+path.Ext(o.BackgroundID))
			if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketBackgrounds, Object: o.BackgroundID}, spec.BgPath); err != nil {
				return err
			}
		}
		next := filepath.Join(dir, "vertical.mp4")
		if err = s.Media.Vertical(ctx, cur, next, spec); err != nil {
			return fmt.Errorf("vertical: %w", err)
		}
		cur = next
	}

	if o.Subtitles {
		assPath := filepath.Join(dir, "subs.ass")
		if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketTranscripts, Object: msg.JobID + "/subs.ass"}, assPath); err != nil {
			return err
		}
		if rect {
			if assPath, err = patchASSRect(assPath, 640); err != nil {
				return err
			}
		}
		next := filepath.Join(dir, "subtitled.mp4")
		if err = s.Media.BurnSubtitles(ctx, cur, assPath, next); err != nil {
			return fmt.Errorf("subtitles: %w", err)
		}
		cur = next
	}

	ivPath := filepath.Join(dir, "intervals.json")
	if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketTranscripts, Object: msg.JobID + "/intervals.json"}, ivPath); err != nil {
		return err
	}
	raw, err := os.ReadFile(ivPath)
	if err != nil {
		return err
	}
	var intervals []domain.Interval
	if err = json.Unmarshal(raw, &intervals); err != nil {
		return err
	}

	variants := o.BannerIDs
	if len(variants) == 0 {
		variants = []string{""}
	}
	var clips []domain.Clip
	for vi, banner := range variants {
		base := cur
		if banner != "" {
			bp := filepath.Join(dir, fmt.Sprintf("banner%d%s", vi, path.Ext(banner)))
			if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketBanners, Object: banner}, bp); err != nil {
				return err
			}
			base = filepath.Join(dir, fmt.Sprintf("variant%d.mp4", vi))
			if err = s.Media.Banner(ctx, cur, bp, base, rect); err != nil {
				return fmt.Errorf("banner: %w", err)
			}
		}
		for _, iv := range intervals {
			if iv.End <= iv.Start {
				continue
			}
			out := filepath.Join(dir, fmt.Sprintf("clip_%d_%d.mp4", vi, iv.ID))
			if err = s.Media.Cut(ctx, base, out, iv.Start, iv.End); err != nil {
				return fmt.Errorf("cut %d: %w", iv.ID, err)
			}
			ref := domain.ObjectRef{Bucket: domain.BucketClips, Object: fmt.Sprintf("%s/%d/%d.mp4", msg.JobID, vi, iv.ID)}
			if err = s.Store.Upload(ctx, ref, out); err != nil {
				return err
			}
			clips = append(clips, domain.Clip{
				ClipID: fmt.Sprintf("%s-%d-%d", msg.JobID, vi, iv.ID), JobID: msg.JobID, Object: ref, Banner: banner,
				Start: iv.Start, End: iv.End, Title: iv.Title, Hashtags: iv.Tags,
			})
		}
		os.Remove(base) // free disk early; the unbannered source is kept in cur
	}

	if err = s.State.SaveClips(ctx, msg.JobID, clips); err != nil {
		return err
	}
	s.Rep.Report(ctx, msg.JobID, stage, "done", "")
	return s.Broker.Publish(ctx, domain.QueueClipsReady, domain.ClipsReady{JobID: msg.JobID, Clips: clips})
}

// patchASSRect rewrites the style for rect mode: top-center alignment, marginTop px from top.
func patchASSRect(in string, marginTop int) (string, error) {
	data, err := os.ReadFile(in)
	if err != nil {
		return "", err
	}
	lines := strings.Split(string(data), "\n")
	for i, l := range lines {
		if strings.HasPrefix(l, "Style:") {
			parts := strings.Split(l, ",")
			if len(parts) >= 22 {
				parts[18] = "8"
				parts[21] = fmt.Sprint(marginTop)
				lines[i] = strings.Join(parts, ",")
			}
		}
	}
	out := strings.TrimSuffix(in, ".ass") + "_rect.ass"
	return out, os.WriteFile(out, []byte(strings.Join(lines, "\n")), 0o644)
}
