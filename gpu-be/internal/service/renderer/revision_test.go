package renderer

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"strings"
	"testing"
	"time"

	"terex/gpu-be/internal/domain"
	"terex/gpu-be/internal/service/reporter"
	"terex/proto/workflow"
)

type testStore struct {
	uploaded  []domain.ObjectRef
	downloads []domain.ObjectRef
}

func (s *testStore) Download(_ context.Context, ref domain.ObjectRef, p string) error {
	s.downloads = append(s.downloads, ref)
	b := []byte("media")
	if strings.HasSuffix(ref.Object, "intervals.json") {
		b = []byte(`[{"id":0,"start":1,"end":3,"title":"Test"}]`)
	}
	return os.WriteFile(p, b, 0o600)
}
func (s *testStore) Upload(_ context.Context, ref domain.ObjectRef, p string) error {
	if _, e := os.Stat(p); e != nil {
		return e
	}
	s.uploaded = append(s.uploaded, ref)
	return nil
}
func (s *testStore) Delete(context.Context, domain.ObjectRef) error                { return nil }
func (s *testStore) Put(context.Context, domain.ObjectRef, io.Reader, int64) error { return nil }
func (s *testStore) PresignGet(context.Context, domain.ObjectRef, time.Duration) (string, error) {
	return "", nil
}
func (s *testStore) PresignPut(context.Context, domain.ObjectRef, time.Duration) (string, error) {
	return "", nil
}

type testMedia struct {
	calls      []string
	ass        string
	start, end float64
}

func (m *testMedia) copy(op, in, out string) error {
	m.calls = append(m.calls, op)
	b, e := os.ReadFile(in)
	if e != nil {
		return e
	}
	return os.WriteFile(out, b, 0o600)
}
func (m *testMedia) VideoProxy(_ context.Context, in, out string, _ int) error {
	return m.copy("proxy", in, out)
}
func (m *testMedia) AudioProxy(_ context.Context, in, out string) error {
	return m.copy("audio", in, out)
}
func (m *testMedia) Vertical(_ context.Context, in, out string, _ domain.VerticalSpec) error {
	return m.copy("vertical", in, out)
}
func (m *testMedia) BurnSubtitles(_ context.Context, in, ass, out string) error {
	b, e := os.ReadFile(ass)
	if e != nil {
		return e
	}
	m.ass = string(b)
	return m.copy("subtitles", in, out)
}
func (m *testMedia) Banner(_ context.Context, in, _, out string, _ bool) error {
	return m.copy("banner", in, out)
}
func (m *testMedia) Cut(_ context.Context, in, out string, a, b float64) error {
	m.start = a
	m.end = b
	return m.copy("cut", in, out)
}

type testState struct{ clips []domain.Clip }

func (s *testState) SetStatus(context.Context, domain.JobStatus) error { return nil }
func (s *testState) GetStatus(context.Context, string) (domain.JobStatus, time.Time, error) {
	return domain.JobStatus{}, time.Time{}, nil
}
func (s *testState) SaveClips(_ context.Context, _ string, c []domain.Clip) error {
	s.clips = c
	return nil
}
func (s *testState) ListClips(context.Context, string) ([]domain.Clip, error) { return s.clips, nil }

type testBroker struct{}

func (testBroker) Publish(context.Context, string, any) error { return nil }

func TestMixedBannerVariantsPreserveUnbanneredSource(t *testing.T) {
	for _, variants := range [][]string{{"", "banner.png"}, {"banner.png", ""}, {"", ""}} {
		t.Run(strings.Join(variants, ","), func(t *testing.T) {
			store := &testStore{}
			state := &testState{}
			broker := testBroker{}
			s := &Service{Store: store, Media: &testMedia{}, State: state, Broker: broker, Rep: &reporter.Reporter{State: state, Broker: broker}, TmpDir: t.TempDir()}
			if err := s.Handle(context.Background(), domain.ProcessVideo{JobID: "job", Options: domain.JobOptions{BannerIDs: variants}}); err != nil {
				t.Fatal(err)
			}
			if len(state.clips) != 2 {
				t.Fatalf("clips=%d", len(state.clips))
			}
			var source workflow.SourceManifest
			if err := json.Unmarshal(state.clips[0].SourceManifest, &source); err != nil || source.Original.Object != "job.mp4" {
				t.Fatalf("manifest=%+v err=%v", source, err)
			}
		})
	}
}

func TestRenderRevisionCutsFirstAndClipsEditedCueTimes(t *testing.T) {
	media := &testMedia{}
	store := &testStore{}
	s := &Service{Store: store, Media: media, TmpDir: t.TempDir()}
	w := workflow.RenderWork{StartSec: 10, EndSec: 20, Source: workflow.SourceManifest{Original: domain.ObjectRef{Bucket: "downloads", Object: "source.mp4"}, DurationSec: 30, Options: domain.JobOptions{Vertical: true, Subtitles: true}, Banner: "banner.png"}, Output: domain.ObjectRef{Bucket: domain.BucketClips, Object: "revisions/7.mp4"}, Cues: []workflow.Cue{{ID: "a", Start: 9, End: 11, Text: "changed {unsafe}"}, {ID: "b", Start: 19, End: 22, Text: "tail"}, {ID: "c", Start: 0, End: 1, Text: "outside"}}}
	if _, err := s.RenderRevision(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if strings.Join(media.calls, ",") != "cut,vertical,subtitles,banner" || media.start != 10 || media.end != 20 {
		t.Fatalf("render order=%v bounds=%f-%f", media.calls, media.start, media.end)
	}
	if strings.Contains(media.ass, "outside") || strings.Contains(media.ass, "{unsafe}") || !strings.Contains(media.ass, "0:00:00.00") || !strings.Contains(media.ass, "0:00:10.00") {
		t.Fatalf("incorrect edited subtitle timing %s", media.ass)
	}
	if len(store.uploaded) != 1 || store.uploaded[0].Object != "revisions/7.mp4" {
		t.Fatal("revision did not upload immutable output")
	}
}

func TestRevisionRejectsBoundsOutsideOriginal(t *testing.T) {
	s := &Service{Store: &testStore{}, Media: &testMedia{}, TmpDir: t.TempDir()}
	_, err := s.RenderRevision(context.Background(), workflow.RenderWork{StartSec: 1, EndSec: 100, Source: workflow.SourceManifest{Original: domain.ObjectRef{Bucket: "downloads", Object: "original"}, DurationSec: 30}, Output: domain.ObjectRef{Bucket: "clips", Object: "out"}})
	if err == nil {
		t.Fatal("out-of-range clip accepted")
	}
}

func TestInitialRenderRetriesUseDifferentObjectKeys(t *testing.T) {
	store := &testStore{}
	state := &testState{}
	broker := testBroker{}
	s := &Service{Store: store, Media: &testMedia{}, State: state, Broker: broker, Rep: &reporter.Reporter{State: state, Broker: broker}, TmpDir: t.TempDir()}
	for n := 0; n < 2; n++ {
		if err := s.Handle(context.Background(), domain.ProcessVideo{JobID: "same-job"}); err != nil {
			t.Fatal(err)
		}
	}
	if len(store.uploaded) != 2 || store.uploaded[0].Object == store.uploaded[1].Object {
		t.Fatal("initial retry overwrote an immutable clip", store.uploaded)
	}
}

func TestSelectedCuesKeepOriginalTimesAndExcludeOtherClips(t *testing.T) {
	cues := []workflow.Cue{{ID: "outside", Start: 0, End: 9}, {ID: "head", Start: 9, End: 11}, {ID: "tail", Start: 19, End: 22}, {ID: "outside-tail", Start: 20, End: 25}}
	selected := SelectCues(cues, 10, 20)
	if len(selected) != 2 || selected[0].Start != 9 || selected[1].End != 22 {
		t.Fatal(selected)
	}
}
