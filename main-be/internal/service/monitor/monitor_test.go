package monitor

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"terex/main-be/internal/domain"
	"terex/proto/msg"
)

type fakeChannels struct {
	domain.ChannelRepo
	chans   []domain.Channel
	seen    map[string]bool
	touched int
}

func (f *fakeChannels) ListChannels(context.Context) ([]domain.Channel, error) { return f.chans, nil }
func (f *fakeChannels) MarkSeen(_ context.Context, _ int64, ids []string) ([]string, error) {
	var fresh []string
	for _, id := range ids {
		if !f.seen[id] {
			f.seen[id] = true
			fresh = append(fresh, id)
		}
	}
	return fresh, nil
}
func (f *fakeChannels) TouchChannel(_ context.Context, id int64, at time.Time) error {
	f.touched++
	for i := range f.chans {
		if f.chans[i].ID == id {
			f.chans[i].LastCheckedAt = &at
			f.chans[i].FeedErrorCode, f.chans[i].LastFeedError = "", ""
			f.chans[i].NextFeedCheckAt, f.chans[i].FeedFailureCount = nil, 0
		}
	}
	return nil
}

func (f *fakeChannels) RecordChannelFeedFailure(_ context.Context, id int64, code, message string, next time.Time) error {
	for i := range f.chans {
		if f.chans[i].ID == id {
			f.chans[i].FeedErrorCode, f.chans[i].LastFeedError = code, message
			f.chans[i].FeedFailureCount++
			f.chans[i].NextFeedCheckAt = &next
		}
	}
	return nil
}

type fakeFeed struct{ vids []domain.FeedVideo }

func (f fakeFeed) ResolveChannel(context.Context, string) (string, error) { return "UC", nil }
func (f fakeFeed) Latest(context.Context, string) ([]domain.FeedVideo, error) {
	return f.vids, nil
}

type fakeJobs struct{ cards []domain.VideoCard }

func (f *fakeJobs) Create(_ context.Context, c domain.VideoCard, _ *int64) (string, error) {
	f.cards = append(f.cards, c)
	return "job", nil
}

func TestTickBaselinesThenCreatesJobsForNewVideos(t *testing.T) {
	now := time.Now()
	ch := &fakeChannels{seen: map[string]bool{}, chans: []domain.Channel{
		{ID: 1, Enabled: true, YTChannelID: "UC", ApproveMode: domain.ApproveAuto, Spec: domain.VideoCard{MakeVertical: true}},
	}}
	feed := fakeFeed{vids: []domain.FeedVideo{{ID: "v2", URL: "u2"}, {ID: "v1", URL: "u1"}}} // newest first
	jobs := &fakeJobs{}
	s := &Service{Channels: ch, Feed: feed, Jobs: jobs}

	// first tick: never checked -> baseline only, no jobs
	if err := s.Tick(context.Background()); err != nil || len(jobs.cards) != 0 {
		t.Fatalf("baseline must not create jobs: %v %v", err, jobs.cards)
	}

	// second tick: a new video appears
	ch.chans[0].LastCheckedAt = &now
	feed.vids = append([]domain.FeedVideo{{ID: "v3", URL: "u3"}}, feed.vids...)
	s.Feed = feed
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(jobs.cards) != 1 || jobs.cards[0].YoutubeURL != "u3" || jobs.cards[0].ApproveMode != domain.ApproveAuto || !jobs.cards[0].MakeVertical {
		t.Fatalf("unexpected jobs: %+v", jobs.cards)
	}

	// third tick: nothing new -> no duplicates
	if err := s.Tick(context.Background()); err != nil || len(jobs.cards) != 1 {
		t.Fatalf("duplicate job created: %v", jobs.cards)
	}
}

func TestTickSkipsDisabled(t *testing.T) {
	now := time.Now()
	ch := &fakeChannels{seen: map[string]bool{}, chans: []domain.Channel{{ID: 1, Enabled: false, LastCheckedAt: &now}}}
	jobs := &fakeJobs{}
	s := &Service{Channels: ch, Feed: fakeFeed{vids: []domain.FeedVideo{{ID: "x"}}}, Jobs: jobs}
	_ = s.Tick(context.Background())
	if len(jobs.cards) != 0 || ch.touched != 0 {
		t.Fatal("disabled channel must be ignored")
	}
}

type failingFeed struct {
	fakeFeed
	calls int
	err   error
}

func (f *failingFeed) Latest(context.Context, string) ([]domain.FeedVideo, error) {
	f.calls++
	return f.vids, f.err
}

func TestFeed404RetrySurvivesServiceRestartAndRecovers(t *testing.T) {
	now := time.Unix(1000, 0)
	ch := &fakeChannels{seen: map[string]bool{}, chans: []domain.Channel{{ID: 1, Enabled: true, YTChannelID: "UC"}}}
	feed := &failingFeed{err: &domain.FeedHTTPError{StatusCode: http.StatusNotFound}}
	s := &Service{Channels: ch, Feed: feed, Jobs: &fakeJobs{}, Now: func() time.Time { return now }}
	for _, delay := range []time.Duration{5 * time.Minute, 15 * time.Minute, time.Hour} {
		if err := s.Tick(context.Background()); err != nil {
			t.Fatal(err)
		}
		if ch.chans[0].NextFeedCheckAt == nil || ch.chans[0].NextFeedCheckAt.Sub(now) != delay {
			t.Fatalf("wrong backoff: %+v", ch.chans[0])
		}
		calls := feed.calls
		// Persisted retry time, rather than process memory, controls a restart.
		s = &Service{Channels: ch, Feed: feed, Jobs: &fakeJobs{}, Now: func() time.Time { return now }}
		if err := s.Tick(context.Background()); err != nil || feed.calls != calls {
			t.Fatal("early retry after restart")
		}
		now = *ch.chans[0].NextFeedCheckAt
	}
	feed.err = nil
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !ch.chans[0].Enabled || ch.chans[0].LastCheckedAt == nil || ch.chans[0].FeedErrorCode != "" || ch.chans[0].NextFeedCheckAt != nil {
		t.Fatalf("recovery failed: %+v", ch.chans[0])
	}
}

// storingChannels records what Add and Update hand to the repository.
type storingChannels struct {
	fakeChannels
	created []domain.Channel
	updates []domain.ChannelUpdate
}

func (f *storingChannels) CreateChannel(_ context.Context, c domain.Channel) (domain.Channel, error) {
	c.ID = 7
	f.created = append(f.created, c)
	return c, nil
}
func (f *storingChannels) UpdateChannel(_ context.Context, _ int64, u domain.ChannelUpdate) (domain.Channel, error) {
	f.updates = append(f.updates, u)
	return domain.Channel{ID: 7}, nil
}
func (f *storingChannels) GetChannel(context.Context, int64) (domain.Channel, error) {
	return domain.Channel{ID: 7}, nil
}

func newStoring() (*Service, *storingChannels) {
	ch := &storingChannels{fakeChannels: fakeChannels{seen: map[string]bool{}}}
	return &Service{Channels: ch, Feed: fakeFeed{}, Jobs: &fakeJobs{}}, ch
}

func TestChannelTemplateWatermarkIsValidatedAndStoredCanonical(t *testing.T) {
	region := msg.WatermarkRegion{X: 0.7, Y: 0.05, W: 0.25, H: 0.1}
	s, ch := newStoring()
	// auto drops stray regions, "off" disappears altogether
	if _, err := s.Add(context.Background(), domain.Channel{URL: "u", Spec: domain.VideoCard{Watermark: &msg.WatermarkOptions{Mode: msg.WatermarkAuto, Regions: []msg.WatermarkRegion{region}}}}); err != nil {
		t.Fatal(err)
	}
	if w := ch.created[0].Spec.Watermark; w == nil || w.Mode != msg.WatermarkAuto || len(w.Regions) != 0 {
		t.Fatalf("stored %+v", w)
	}
	if _, err := s.Add(context.Background(), domain.Channel{URL: "u", Spec: domain.VideoCard{Watermark: &msg.WatermarkOptions{Mode: "off"}}}); err != nil {
		t.Fatal(err)
	}
	if ch.created[1].Spec.Watermark != nil {
		t.Fatalf("off must be stored as nothing, got %+v", ch.created[1].Spec.Watermark)
	}
	// a patch is validated the same way
	manual := &domain.VideoCard{Watermark: &msg.WatermarkOptions{Mode: msg.WatermarkManual, Regions: []msg.WatermarkRegion{region}}}
	if _, err := s.Update(context.Background(), 7, domain.ChannelUpdate{Spec: manual}); err != nil {
		t.Fatal(err)
	}
	if len(ch.updates) != 1 || ch.updates[0].Spec.Watermark.Regions[0] != region {
		t.Fatalf("updates %+v", ch.updates)
	}
}

func TestChannelTemplateWithBrokenWatermarkIsRejected(t *testing.T) {
	s, ch := newStoring()
	broken := &msg.WatermarkOptions{Mode: msg.WatermarkManual}
	if _, err := s.Add(context.Background(), domain.Channel{URL: "u", Spec: domain.VideoCard{Watermark: broken}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Add: err %v, want ErrInvalid", err)
	}
	if _, err := s.Update(context.Background(), 7, domain.ChannelUpdate{Spec: &domain.VideoCard{Watermark: broken}}); !errors.Is(err, domain.ErrInvalid) {
		t.Fatalf("Update: err %v, want ErrInvalid", err)
	}
	if len(ch.created) != 0 || len(ch.updates) != 0 {
		t.Fatal("a channel with broken watermark settings reached the repository")
	}
}

func TestTickPassesTheWatermarkToNewJobs(t *testing.T) {
	now := time.Now()
	wm := &msg.WatermarkOptions{Mode: msg.WatermarkManual, Regions: []msg.WatermarkRegion{{X: 0.7, Y: 0.05, W: 0.25, H: 0.1}}}
	ch := &fakeChannels{seen: map[string]bool{}, chans: []domain.Channel{{ID: 1, Enabled: true, YTChannelID: "UC", LastCheckedAt: &now, Spec: domain.VideoCard{Watermark: wm}}}}
	jobs := &fakeJobs{}
	s := &Service{Channels: ch, Feed: fakeFeed{vids: []domain.FeedVideo{{ID: "v1", URL: "u1"}}}, Jobs: jobs}
	if err := s.Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(jobs.cards) != 1 || jobs.cards[0].Watermark != wm {
		t.Fatalf("job cards %+v must carry the channel's watermark template", jobs.cards)
	}
}
