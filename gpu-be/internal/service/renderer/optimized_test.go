package renderer

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"terex/gpu-be/internal/domain"
	"terex/proto/workflow"
)

type shortMedia struct {
	testMedia
	fail error
}

func (m *shortMedia) RenderShort(_ context.Context, s domain.ShortRenderSpec, out string) error {
	m.start, m.end = s.StartSec, s.EndSec
	if m.fail != nil {
		m.calls = append(m.calls, "short")
		return m.fail
	}
	return m.copy("short", s.Source, out)
}
func TestShortFastPathAndRuntimeFailureDoNotStartLegacyRender(t *testing.T) {
	media := &shortMedia{}
	s := Service{Store: &testStore{}, Media: media, TmpDir: t.TempDir(), TwitchFastPath: true}
	w := workflow.RenderWork{Twitch: true, StartSec: 0.25, EndSec: 1.5, Source: workflow.SourceManifest{Original: domain.ObjectRef{Bucket: "imports", Object: "source"}, DurationSec: 2, Options: domain.JobOptions{Vertical: true, Shape: "rectangle"}}, Output: domain.ObjectRef{Bucket: "clips", Object: "attempt"}}
	if _, err := s.RenderRevision(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if strings.Join(media.calls, ",") != "short" || media.start != 0.25 || media.end != 1.5 {
		t.Fatal(media)
	}
	media.calls = nil
	media.fail = errors.New("encoder unavailable")
	if _, err := s.RenderRevision(context.Background(), w); err == nil || strings.Join(media.calls, ",") != "short" {
		t.Fatal("runtime fallback repeated render", err, media.calls)
	}
	media.calls = nil
	w.Source.Options.Subtitles = true
	if _, err := s.RenderRevision(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if strings.Join(media.calls, ",") != "cut,vertical,subtitles" {
		t.Fatal("unsupported template bypassed legacy", media.calls)
	}
}

func TestPreparedAssemblyRetainsOriginalUntilCompletionAndSkipsRoundTrip(t *testing.T) {
	store := &testStore{}
	media := &assemblyMedia{}
	s := Service{Store: store, Media: media, TmpDir: t.TempDir(), AssemblyLocalReuse: true}
	p, err := s.PrepareRender(context.Background(), assemblyWork("new/attempt"))
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if _, err = os.Stat(p.originalPath); err != nil {
		t.Fatal("original cleaned before final upload", err)
	}
	if _, err = s.RenderPrepared(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	if len(store.downloads) != 3 || len(store.uploaded) != 2 || len(media.calls) != 0 {
		t.Fatal("assembly redownloaded or cut", store.downloads, store.uploaded, media.calls)
	}
	if p.Work.Source.Original.Object == "" {
		t.Fatal("editor original missing")
	}
	p.Close()
	p.Close()
	if _, err = os.Stat(p.originalPath); !os.IsNotExist(err) {
		t.Fatal("attempt scratch retained")
	}
}
