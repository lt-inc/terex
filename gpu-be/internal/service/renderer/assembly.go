package renderer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"terex/gpu-be/internal/domain"
	"terex/proto/workflow"
)

type assembler interface {
	Assemble(context.Context, []domain.AssemblySegment, string, int) error
	Duration(context.Context, string) (float64, error)
}

// MaterializeAssembly resolves only the immutable files pinned by the control
// plane. The resulting original is unique to this fenced render attempt.
func (s *Service) MaterializeAssembly(ctx context.Context, w workflow.RenderWork) (workflow.RenderWork, error) {
	a := w.Source.Assembly
	if a == nil {
		return w, nil
	}
	// A later editor revision can trim the already assembled original. Only a
	// new composition (whose original is unset) needs its timeline rebuilt.
	if w.Source.Original.Bucket != "" && w.Source.Original.Object != "" {
		return w, nil
	}
	if a.SchemaVersion != 1 || a.Profile != "landscape_1080p30" || a.BumperDurationFrames != 30 || len(a.Items) < 3 || len(a.Items) > 10 || w.Output.Bucket != domain.BucketClips || w.Output.Object == "" {
		return w, fmt.Errorf("invalid assembly specification")
	}
	m, ok := s.Media.(assembler)
	if !ok {
		return w, fmt.Errorf("media worker does not support assemblies")
	}
	if err := os.MkdirAll(s.TmpDir, 0o755); err != nil {
		return w, err
	}
	dir, err := os.MkdirTemp(s.TmpDir, "assembly-")
	if err != nil {
		return w, err
	}
	defer os.RemoveAll(dir)
	segments := make([]domain.AssemblySegment, 0, len(a.Items))
	for i, item := range a.Items {
		if item.Original.Bucket != "imports" || !strings.HasPrefix(item.Original.Object, "verified/") || len(item.SHA256) != 64 || item.SourceClipID == "" || item.SourceRevisionID <= 0 {
			return w, fmt.Errorf("invalid pinned assembly source")
		}
		file := filepath.Join(dir, fmt.Sprintf("source-%02d.mp4", i))
		if err = s.Store.Download(ctx, item.Original, file); err != nil {
			return w, err
		}
		f, e := os.Open(file)
		if e != nil {
			return w, e
		}
		h := sha256.New()
		_, e = io.Copy(h, f)
		f.Close()
		if e != nil {
			return w, e
		}
		if hex.EncodeToString(h.Sum(nil)) != item.SHA256 {
			return w, fmt.Errorf("assembly source checksum mismatch")
		}
		segments = append(segments, domain.AssemblySegment{File: file, StartSec: item.StartSec, EndSec: item.EndSec, NextChannel: item.BroadcasterLogin})
	}
	output := filepath.Join(dir, "original.mp4")
	if err = m.Assemble(ctx, segments, output, a.BumperDurationFrames); err != nil {
		return w, err
	}
	dur, err := m.Duration(ctx, output)
	if err != nil {
		return w, err
	}
	ref := domain.ObjectRef{Bucket: "imports", Object: "assemblies/" + w.Output.Object + ".original.mp4"}
	if err = s.Store.Upload(ctx, ref, output); err != nil {
		return w, err
	}
	w.Source.Original = ref
	w.Source.DurationSec = dur
	w.Source.Options.Vertical = false
	w.Source.Options.Subtitles = false
	w.StartSec = 0
	w.EndSec = dur
	w.Cues = nil
	return w, nil
}
