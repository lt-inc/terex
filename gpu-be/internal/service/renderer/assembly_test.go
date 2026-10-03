package renderer

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"terex/gpu-be/internal/domain"
	"terex/proto/workflow"
	"testing"
)

type assemblyMedia struct {
	testMedia
	segments []domain.AssemblySegment
}

func (m *assemblyMedia) Assemble(_ context.Context, segments []domain.AssemblySegment, out string, _ int) error {
	m.segments = segments
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
	if first.Source.Original == second.Source.Original || first.Source.Assembly == nil || first.Source.DurationSec != 32 || len(m.segments) != 3 || first.Source.Options.Vertical {
		t.Fatalf("incorrect resolution %+v %+v", first, second)
	}
	bad := assemblyWork("bad.mp4")
	bad.Source.Assembly.Items[1].SHA256 = "ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"
	before := len(st.uploaded)
	if _, err = s.MaterializeAssembly(context.Background(), bad); err == nil || len(st.uploaded) != before {
		t.Fatal("corrupt assembly source accepted")
	}
}
