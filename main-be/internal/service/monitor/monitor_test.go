package monitor

import (
	"context"
	"net/http"
	"testing"
	"time"

	"terex/main-be/internal/domain"
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
