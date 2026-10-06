package renderer

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"terex/gpu-be/internal/domain"
	"terex/proto/workflow"
	"testing"
)

type assemblyMedia struct {
	testMedia
	segments []domain.AssemblySegment
}

func (m *assemblyMedia) NormalizeAssemblySegment(_ context.Context, segment domain.AssemblySegment, out string) error {
	if _, err := os.Stat(segment.File); err != nil {
		return err
	}
	for _, previous := range m.segments {
		if _, err := os.Stat(previous.File); !os.IsNotExist(err) {
			return fmt.Errorf("previous original was retained: %s", previous.File)
		}
	}
	m.segments = append(m.segments, segment)
	return os.WriteFile(out, []byte("normalized"), 0o600)
}

func TestV2AssemblyUsesVerifiedMediaWithoutShortsAndScratchBudget(t *testing.T) {
	s := Service{Store: &testStore{}, Media: &assemblyMedia{}, TmpDir: t.TempDir(), AssemblyTmpDir: t.TempDir(), AssemblyBudgetBytes: 4096}
	stale := filepath.Join(s.AssemblyTmpDir, "attempt-stale")
	if err := os.Mkdir(stale, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareAssemblyScratch(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatal("stale scratch was retained")
	}
	ctx, release, err := s.BeginAssembly(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	w := assemblyWork("v2/attempt.mp4")
	w.Source.Assembly.SchemaVersion = 2
	first := w.Source.Assembly.Items[0]
	w.Source.Assembly.Items = nil
	for i := 0; i < 60; i++ {
		item := first
		item.MediaID = fmt.Sprintf("media-%d", i)
		item.TwitchSourceID = fmt.Sprintf("source-%d", i)
		item.SourceClipID, item.SourceRevisionID = "", 0
		w.Source.Assembly.Items = append(w.Source.Assembly.Items, item)
	}
	w, err = s.MaterializeAssembly(ctx, w)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.RenderRevision(ctx, w); err != nil {
		t.Fatal(err)
	}
	dir := s.revisionTmp(ctx)
	if err = os.WriteFile(filepath.Join(dir, "oversized"), make([]byte, 4097), 0o600); err != nil {
		t.Fatal(err)
	}
	if err = CheckAssemblyBudget(ctx); !errors.Is(err, ErrAssemblyBudget) {
		t.Fatalf("missing budget error: %v", err)
	}
}
func (m *assemblyMedia) AssemblyTransition(_ context.Context, _ string, out string) error {
	return os.WriteFile(out, []byte("card"), 0o600)
}
func (m *assemblyMedia) ConcatAssembly(_ context.Context, _ []string, out string) error {
	return os.WriteFile(out, []byte("assembled"), 0o600)
}
func (m *assemblyMedia) Duration(context.Context, string) (float64, error) { return 32, nil }
func assemblyWork(output string) workflow.RenderWork {
	a := &workflow.AssemblySpec{SchemaVersion: 1, Profile: "landscape_1080p30", BumperDurationFrames: 30}
	for i := 0; i < 3; i++ {
		a.Items = append(a.Items, workflow.AssemblyItem{SourceClipID: fmt.Sprintf("clip%d", i), SourceRevisionID: int64(i + 1), Original: domain.ObjectRef{Bucket: "imports", Object: fmt.Sprintf("verified/import%d/source.mp4", i)}, SHA256: fmt.Sprintf("%x", sha256.Sum256([]byte("media"))), StartSec: 0, EndSec: 10, BroadcasterLogin: "channel"})
	}
	return workflow.RenderWork{RevisionID: 4, Output: domain.ObjectRef{Bucket: "clips", Object: output}, Source: workflow.SourceManifest{Assembly: a, DurationSec: 32}, EndSec: 32}
}
func TestAssemblyUsesOriginalsAndAttemptSpecificObject(t *testing.T) {
	st := &testStore{}
	m := &assemblyMedia{}
	s := Service{Store: st, Media: m, TmpDir: t.TempDir()}
	first, err := s.MaterializeAssembly(context.Background(), assemblyWork("revision4/attempt1.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.MaterializeAssembly(context.Background(), assemblyWork("revision4/attempt2.mp4"))
	if err != nil {
		t.Fatal(err)
	}
	if first.Source.Original == second.Source.Original || first.Source.Assembly == nil || first.Source.DurationSec != 32 || len(m.segments) != 6 || first.Source.Options.Vertical {
		t.Fatalf("incorrect resolution %+v %+v", first, second)
	}
	bad := assemblyWork("bad.mp4")
	bad.Source.Assembly.Items[1].SHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	before := len(st.uploaded)
	if _, err = s.MaterializeAssembly(context.Background(), bad); err == nil || len(st.uploaded) != before {
		t.Fatal("corrupt assembly source accepted")
	}
}
