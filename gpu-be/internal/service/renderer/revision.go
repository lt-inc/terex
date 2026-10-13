package renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"path"
	"path/filepath"
	"strings"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/candidate"
	"terex/gpu-be/internal/service/mediatmp"
	"terex/proto/workflow"
)

type renderDigestKey struct{}

func (s *Service) uploadRevision(ctx context.Context, ref domain.ObjectRef, file string) error {
	if p, ok := ctx.Value(renderDigestKey{}).(*PreparedRender); ok {
		f, err := os.Open(file)
		if err != nil {
			return err
		}
		h := sha256.New()
		size, err := io.Copy(h, f)
		_ = f.Close()
		if err != nil {
			return err
		}
		if err = ctx.Err(); err != nil {
			return err
		}
		p.MediaSHA256 = hex.EncodeToString(h.Sum(nil))
		p.MediaSizeBytes = size
	}
	return s.Store.Upload(ctx, ref, file)
}

func Cues(segs []domain.Segment) []workflow.Cue {
	var out []workflow.Cue
	for i, s := range segs {
		if len(s.Words) == 0 {
			if s.End > s.Start && strings.TrimSpace(s.Text) != "" {
				out = append(out, workflow.Cue{ID: fmt.Sprintf("s%d", i), Start: s.Start, End: s.End, Text: strings.TrimSpace(s.Text)})
			}
			continue
		}
		for j, w := range s.Words {
			if w.End > w.Start && strings.TrimSpace(w.Word) != "" {
				out = append(out, workflow.Cue{ID: fmt.Sprintf("s%dw%d", i, j), Start: w.Start, End: w.End, Text: strings.TrimSpace(w.Word)})
			}
		}
	}
	return out
}

// SelectCues keeps an editor's payload bounded by the selected clip. The full
// structured transcript stays in object storage, rather than every queue event.
func SelectCues(cues []workflow.Cue, start, end float64) []workflow.Cue {
	out := make([]workflow.Cue, 0)
	for _, c := range cues {
		if c.End > start && c.Start < end {
			out = append(out, c)
		}
	}
	return out
}

// RelativeCues clips edited absolute timestamps to the selected source interval.
// Passing an empty slice intentionally produces a clip without subtitle events.
func RelativeCues(cues []workflow.Cue, start, end float64) []domain.Segment {
	var out []domain.Segment
	for _, c := range cues {
		if c.End <= start || c.Start >= end || strings.TrimSpace(c.Text) == "" {
			continue
		}
		a, b := math.Max(c.Start, start)-start, math.Min(c.End, end)-start
		if b <= a {
			continue
		}
		text := strings.NewReplacer("{", "｛", "}", "｝", "\\", "＼", "\r", " ", "\n", " ").Replace(c.Text)
		out = append(out, domain.Segment{ID: len(out), Start: a, End: b, Text: text, Words: []domain.Word{{Word: text, Start: a, End: b}}})
	}
	return out
}

// RenderRevision cuts before any expensive transforms: only the selected clip is
// rebuilt, from the original media, using its immutable revision's edited cues.
func (s *Service) RenderRevision(ctx context.Context, w workflow.RenderWork) (domain.ObjectRef, error) {
	return s.renderRevision(ctx, w, "", false)
}

func (s *Service) fastPathReason(w workflow.RenderWork) string {
	if !s.TwitchFastPath {
		return "disabled"
	}
	if !w.Twitch || w.Source.Assembly != nil {
		return "not_twitch_short"
	}
	o := w.Source.Options
	if !o.Vertical {
		return "not_vertical"
	}
	if o.Shape != "" && o.Shape != "rectangle" {
		return "shape"
	}
	if o.Subtitles {
		return "subtitles"
	}
	if o.BackgroundID != "" {
		return "custom_background"
	}
	if w.Source.Banner != "" {
		ext := strings.ToLower(path.Ext(w.Source.Banner))
		if ext != ".png" && ext != ".jpg" && ext != ".jpeg" {
			return "animated_or_unsupported_banner"
		}
	}
	if _, ok := s.Media.(domain.ShortRenderer); !ok {
		return "media_capability"
	}
	return ""
}

func (s *Service) renderRevision(ctx context.Context, w workflow.RenderWork, localOriginal string, localAssembly bool) (ref domain.ObjectRef, err error) {
	format := "short"
	if w.Source.Assembly != nil {
		format = "compilation"
	}
	ctx = domain.WithMediaIdentity(ctx, w.JobID, w.ClipID, format, w.RevisionID)
	ctx, release, err := mediatmp.Begin(ctx, s.Resources)
	if err != nil {
		return domain.ObjectRef{}, err
	}
	defer release()
	defer func() { err = mediatmp.Cause(ctx, err) }()
	if w.Source.Original.Bucket == "" || w.Source.Original.Object == "" || w.Output.Bucket != domain.BucketClips {
		return domain.ObjectRef{}, fmt.Errorf("revision lacks original or clip output")
	}
	if math.IsNaN(w.StartSec) || math.IsNaN(w.EndSec) || math.IsInf(w.StartSec, 0) || math.IsInf(w.EndSec, 0) || w.StartSec < 0 || w.EndSec <= w.StartSec || (w.Source.DurationSec > 0 && w.EndSec > w.Source.DurationSec+0.05) {
		return domain.ObjectRef{}, fmt.Errorf("invalid clip bounds")
	}
	for _, cue := range w.Cues {
		if math.IsNaN(cue.Start) || math.IsNaN(cue.End) || math.IsInf(cue.Start, 0) || math.IsInf(cue.End, 0) || cue.Start < 0 || cue.End <= cue.Start || (w.Source.DurationSec > 0 && cue.End > w.Source.DurationSec+0.05) {
			return domain.ObjectRef{}, fmt.Errorf("invalid subtitle cue bounds")
		}
	}
	tmp := s.revisionTmp(ctx)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return domain.ObjectRef{}, err
	}
	dir, cleanup, err := mediatmp.MkdirTemp(ctx, s.Resources, tmp, "revision-")
	if err != nil {
		return domain.ObjectRef{}, err
	}
	defer cleanup()
	src := localOriginal
	if src == "" {
		src = filepath.Join(dir, "original.mp4")
		if err = s.Store.Download(ctx, w.Source.Original, src); err != nil {
			return domain.ObjectRef{}, fmt.Errorf("source unavailable: %w", err)
		}
	}
	// A newly assembled original is already normalized to the final profile.
	// This provenance is local and cannot be supplied by an API/queue request.
	if localAssembly && w.StartSec == 0 && w.EndSec == w.Source.DurationSec && !w.Source.Options.Vertical && !w.Source.Options.Subtitles {
		out := src
		if w.Source.Banner != "" {
			bp := filepath.Join(dir, "banner"+path.Ext(w.Source.Banner))
			if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketBanners, Object: w.Source.Banner}, bp); err != nil {
				return domain.ObjectRef{}, err
			}
			out = filepath.Join(dir, "bannered.mp4")
			if err = s.Media.Banner(ctx, src, bp, out, w.Source.Options.Shape == "rectangle"); err != nil {
				return domain.ObjectRef{}, err
			}
		}
		slog.InfoContext(ctx, "render profile selected", append(domain.MediaLogAttrs(ctx), "profile", "assembly_local", "original_download_skipped", true, "full_range_cut_skipped", true)...)
		if err = s.uploadRevision(ctx, w.Output, out); err != nil {
			return domain.ObjectRef{}, err
		}
		return w.Output, nil
	}
	if reason := s.fastPathReason(w); reason == "" {
		banner := ""
		if w.Source.Banner != "" {
			banner = filepath.Join(dir, "banner"+path.Ext(w.Source.Banner))
			if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketBanners, Object: w.Source.Banner}, banner); err != nil {
				return domain.ObjectRef{}, err
			}
		}
		out := filepath.Join(dir, "short.mp4")
		slog.InfoContext(ctx, "render profile selected", append(domain.MediaLogAttrs(ctx), "profile", "short_single_pass")...)
		if err = s.Media.(domain.ShortRenderer).RenderShort(ctx, domain.ShortRenderSpec{Source: src, Banner: banner, StartSec: w.StartSec, EndSec: w.EndSec, Rectangle: true}, out); err != nil {
			return domain.ObjectRef{}, err
		}
		if err = s.uploadRevision(ctx, w.Output, out); err != nil {
			return domain.ObjectRef{}, err
		}
		return w.Output, nil
	} else {
		slog.InfoContext(ctx, "render profile selected", append(domain.MediaLogAttrs(ctx), "profile", "legacy", "fallback_reason", reason)...)
	}
	cur := filepath.Join(dir, "cut.mp4")
	if err = s.Media.Cut(ctx, src, cur, w.StartSec, w.EndSec); err != nil {
		return domain.ObjectRef{}, err
	}
	o := w.Source.Options
	if o.Vertical {
		spec := domain.VerticalSpec{Width: 1080, Blur: 40, Circle: o.Shape == "circle"}
		if o.BackgroundID != "" {
			spec.BgPath = filepath.Join(dir, "background"+path.Ext(o.BackgroundID))
			if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketBackgrounds, Object: o.BackgroundID}, spec.BgPath); err != nil {
				return domain.ObjectRef{}, err
			}
		}
		next := filepath.Join(dir, "vertical.mp4")
		if err = s.Media.Vertical(ctx, cur, next, spec); err != nil {
			return domain.ObjectRef{}, err
		}
		cur = next
	}
	if o.Subtitles {
		ass := filepath.Join(dir, "edited.ass")
		if err = os.WriteFile(ass, []byte(candidate.ASS(RelativeCues(w.Cues, w.StartSec, w.EndSec), 4)), 0o600); err != nil {
			return domain.ObjectRef{}, err
		}
		if o.Shape == "rectangle" {
			ass, err = patchASSRect(ass, 640)
			if err != nil {
				return domain.ObjectRef{}, err
			}
		}
		next := filepath.Join(dir, "subtitled.mp4")
		if err = s.Media.BurnSubtitles(ctx, cur, ass, next); err != nil {
			return domain.ObjectRef{}, err
		}
		cur = next
	}
	if w.Source.Banner != "" {
		bp := filepath.Join(dir, "banner"+path.Ext(w.Source.Banner))
		if err = s.Store.Download(ctx, domain.ObjectRef{Bucket: domain.BucketBanners, Object: w.Source.Banner}, bp); err != nil {
			return domain.ObjectRef{}, err
		}
		next := filepath.Join(dir, "bannered.mp4")
		if err = s.Media.Banner(ctx, cur, bp, next, o.Shape == "rectangle"); err != nil {
			return domain.ObjectRef{}, err
		}
		cur = next
	}
	if err = s.uploadRevision(ctx, w.Output, cur); err != nil {
		return domain.ObjectRef{}, err
	}
	return w.Output, nil
}
