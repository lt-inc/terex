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
	"sync"

	"go.opentelemetry.io/otel/attribute"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/mediatmp"
	"terex/gpu-be/internal/service/reporter"
	"terex/proto/telemetry"
	"terex/proto/workflow"
)

const stage = "renderer"

type Service struct {
	Store               domain.ObjectStore
	Media               domain.Media
	Broker              domain.Broker
	State               domain.JobState
	Rep                 *reporter.Reporter
	TmpDir              string
	AssemblyTmpDir      string
	AssemblyBudgetBytes int64
	assemblyOnce        sync.Once
	assemblyPermit      chan struct{}
	Resources           domain.MediaResources
	TwitchFastPath      bool
	AssemblyLocalReuse  bool
}

func (s *Service) Handle(ctx context.Context, msg domain.ProcessVideo) (err error) {
	ctx = domain.WithMediaIdentity(ctx, msg.JobID, "", "legacy", 0)
	ctx, release, err := mediatmp.Begin(ctx, s.Resources)
	if err != nil {
		return err
	}
	defer release()
	defer func() { err = mediatmp.Cause(ctx, err) }()
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
	dir, cleanup, err := mediatmp.MkdirTemp(ctx, s.Resources, s.TmpDir, msg.JobID+"-")
	if err != nil {
		return err
	}
	defer cleanup()

	cur := filepath.Join(dir, "src.mp4")
	if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketDownloads, Object: msg.JobID + ".mp4"}, cur); err != nil {
		return err
	}

	// the watermark goes first: every later transform (scaling, subtitles, banner) would smear it or
	// move it away from the regions chosen on the source frame
	if cur, err = s.maskWatermark(ctx, dir, cur, o.Watermark); err != nil {
		return err
	}

	if o.Brand != nil {
		next, e := s.brand(ctx, msg.JobID, dir, cur, o)
		if e != nil {
			return e
		}
		cur = next
		rect = true // the banner goes to the very bottom, as in rectangle mode
	} else if o.Vertical {
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

	if o.Subtitles && o.Brand == nil {
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
	manifest := workflow.SourceManifest{Original: domain.ObjectRef{Bucket: domain.BucketDownloads, Object: msg.JobID + ".mp4"}, Proxy: domain.ObjectRef{Bucket: domain.BucketProxy, Object: msg.JobID + ".mp4"}, Transcript: domain.ObjectRef{Bucket: domain.BucketTranscripts, Object: msg.JobID + "/transcript.json"}, Options: o}
	if reader, ok := s.Store.(interface {
		Read(context.Context, domain.ObjectRef) ([]byte, error)
	}); ok {
		if data, e := reader.Read(ctx, domain.ObjectRef{Bucket: domain.BucketTranscripts, Object: msg.JobID + "/source.json"}); e == nil {
			_ = json.Unmarshal(data, &manifest)
		}
		if data, e := reader.Read(ctx, manifest.Transcript); e == nil {
			var segs []domain.Segment
			if json.Unmarshal(data, &segs) == nil {
				manifest.Cues = Cues(segs)
			}
		}
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
			ref := domain.ObjectRef{Bucket: domain.BucketClips, Object: fmt.Sprintf("%s/initial/%s/%d/%d.mp4", msg.JobID, filepath.Base(dir), vi, iv.ID)}
			if err = s.Store.Upload(ctx, ref, out); err != nil {
				return err
			}
			clipManifest := manifest
			clipManifest.Banner = banner
			clipManifest.Cues = SelectCues(manifest.Cues, iv.Start, iv.End)
			sourceJSON, _ := json.Marshal(clipManifest)
			clips = append(clips, domain.Clip{
				ClipID: fmt.Sprintf("%s-%d-%d", msg.JobID, vi, iv.ID), JobID: msg.JobID, Object: ref, Banner: banner,
				Start: iv.Start, End: iv.End, Title: iv.Title, Hashtags: iv.Tags,
				SourceManifest: sourceJSON,
			})
		}
		if base != cur {
			os.Remove(base)
		} // free each banner variant after its cuts
	}

	if err = s.State.SaveClips(ctx, msg.JobID, clips); err != nil {
		return err
	}
	s.Rep.Report(ctx, msg.JobID, stage, "done", "")
	return s.Broker.Publish(ctx, domain.QueueClipsReady, domain.ClipsReady{JobID: msg.JobID, Clips: clips})
}

// brand renders the branded layout: background, shrunk video on top, RU+EN subtitles, shifted pitch.
func (s *Service) brand(ctx context.Context, jobID, dir, in string, o domain.JobOptions) (string, error) {
	br, ok := s.Media.(domain.BrandRenderer)
	if !ok {
		return "", fmt.Errorf("brand: media client cannot render the branded layout")
	}
	spec := domain.BrandSpec{VideoScale: o.Brand.VideoScale, PitchSemitones: o.Brand.PitchSemitones}
	if !o.Brand.NoSubtitles {
		spec.ASSPath = filepath.Join(dir, "brand.ass")
		if err := s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketTranscripts, Object: jobID + "/" + domain.BrandSubsObject}, spec.ASSPath); err != nil {
			return "", fmt.Errorf("brand subtitles: %w", err)
		}
		// The ASS file was written before the source size was known: put the text right under the video as it is.
		if p, ok := s.Media.(interface {
			Probe(context.Context, string) (domain.MediaProbe, error)
		}); ok {
			if pr, err := p.Probe(ctx, in); err == nil {
				if spec.ASSPath, err = patchASSRect(spec.ASSPath, domain.BrandVideoHeight(o.Brand.VideoScale, pr.Width, pr.Height)); err != nil {
					return "", fmt.Errorf("brand subtitles: %w", err)
				}
			}
		}
	}
	if o.BackgroundID != "" {
		spec.BgPath = filepath.Join(dir, "bg"+path.Ext(o.BackgroundID))
		if err := s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketBackgrounds, Object: o.BackgroundID}, spec.BgPath); err != nil {
			return "", err
		}
	}
	out := filepath.Join(dir, "branded.mp4")
	if err := br.RenderBrand(ctx, in, out, spec); err != nil {
		return "", fmt.Errorf("brand: %w", err)
	}
	return out, nil
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
