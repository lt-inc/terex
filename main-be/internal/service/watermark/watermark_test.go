package watermark

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"

	"terex/main-be/internal/domain"
	"terex/proto/msg"
	"terex/proto/workflow"
)

type fakeFeed struct {
	videos   []domain.FeedVideo
	resolved string
	err      error
}

func (f *fakeFeed) ResolveChannel(_ context.Context, raw string) (string, error) {
	f.resolved = raw
	return "UC123", f.err
}
func (f *fakeFeed) Latest(context.Context, string) ([]domain.FeedVideo, error) { return f.videos, nil }

type fakeFrames struct {
	asked *workflow.WatermarkFramesRequest
	resp  *workflow.WatermarkFramesResponse
	err   error
}

func (f *fakeFrames) WatermarkFrames(_ context.Context, q *workflow.WatermarkFramesRequest) (*workflow.WatermarkFramesResponse, error) {
	f.asked = q
	return f.resp, f.err
}

func newService() (*Service, *fakeFeed, *fakeFrames) {
	feed := &fakeFeed{videos: []domain.FeedVideo{{ID: "new", Title: "Newest", URL: "https://www.youtube.com/watch?v=new"}, {ID: "old", URL: "https://www.youtube.com/watch?v=old"}}}
	frames := &fakeFrames{resp: &workflow.WatermarkFramesResponse{Width: 1280, Height: 720,
		Frames:    []workflow.WatermarkFrame{{TimeSec: 12.5, JPEG: []byte{0xFF, 0xD8, 0xFF}}},
		Suggested: []msg.WatermarkRegion{{X: 0.7, Y: 0.05, W: 0.25, H: 0.1}}}}
	return &Service{Feed: feed, Frames: frames}, feed, frames
}

func TestPreviewOfChannelUsesTheNewestVideo(t *testing.T) {
	s, feed, frames := newService()
	got, err := s.Preview(context.Background(), " https://www.youtube.com/@somebody ")
	if err != nil {
		t.Fatal(err)
	}
	if feed.resolved != "https://www.youtube.com/@somebody" || frames.asked.URL != "https://www.youtube.com/watch?v=new" {
		t.Fatalf("resolved %q, asked %q", feed.resolved, frames.asked.URL)
	}
	if got.VideoTitle != "Newest" || got.Width != 1280 || len(got.Suggested) != 1 {
		t.Fatalf("result %+v", got)
	}
	if len(got.Frames) != 1 || got.Frames[0].TimeSec != 12.5 || got.Frames[0].Image != "data:image/jpeg;base64,/9j/" {
		t.Fatalf("frames %+v", got.Frames)
	}
}

func TestPreviewOfVideoSkipsTheFeed(t *testing.T) {
	for _, link := range []string{
		"https://www.youtube.com/watch?v=abc", "https://youtu.be/abc", "https://www.youtube.com/shorts/abc",
		"https://m.youtube.com/watch?v=abc&t=5", "https://www.youtube.com/live/abc",
	} {
		s, feed, frames := newService()
		if _, err := s.Preview(context.Background(), link); err != nil {
			t.Fatalf("%s: %v", link, err)
		}
		if feed.resolved != "" || frames.asked.URL != link {
			t.Fatalf("%s: feed used (%q) or url changed (%q)", link, feed.resolved, frames.asked.URL)
		}
	}
}

func TestPreviewNeverReturnsNullSuggestions(t *testing.T) {
	s, _, frames := newService()
	frames.resp.Suggested = nil
	got, err := s.Preview(context.Background(), "https://youtu.be/abc")
	if err != nil || got.Suggested == nil {
		t.Fatalf("suggested %#v, err %v: the FE iterates it", got.Suggested, err)
	}
}

func TestPreviewRejects(t *testing.T) {
	s, feed, frames := newService()
	for _, link := range []string{"", "not a url", "ftp://youtube.com/watch?v=a", "https://example.com/watch?v=a", "https://youtube.com.evil.test/@x", "file:///etc/passwd"} {
		if _, err := s.Preview(context.Background(), link); !errors.Is(err, domain.ErrInvalid) {
			t.Fatalf("%q: err %v, want ErrInvalid", link, err)
		}
	}
	if frames.asked != nil || feed.resolved != "" {
		t.Fatal("a rejected link reached the feed or gpu-be")
	}
	feed.videos = nil
	if _, err := s.Preview(context.Background(), "https://www.youtube.com/@empty"); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("empty channel: err %v, want ErrInvalid", err)
	}
}

func TestPreviewPropagatesGpuErrors(t *testing.T) {
	s, _, frames := newService()
	frames.err = errors.New("yt-dlp: video unavailable")
	if _, err := s.Preview(context.Background(), "https://youtu.be/abc"); err == nil || !strings.Contains(err.Error(), "video unavailable") {
		t.Fatalf("err %v", err)
	}
}

func TestIsVideo(t *testing.T) {
	cases := map[string]bool{
		"https://www.youtube.com/watch?v=a": true, "https://www.youtube.com/watch": false, "https://youtu.be/": false,
		"https://www.youtube.com/@handle": false, "https://www.youtube.com/channel/UC1": false, "https://www.youtube.com/shorts/": false,
		"https://www.youtube.com/@handle/shorts": false,
	}
	for link, want := range cases {
		u, _ := url.Parse(link)
		if got := isVideo(u); got != want {
			t.Errorf("isVideo(%s) = %v, want %v", link, got, want)
		}
	}
}
