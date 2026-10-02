package monitor

import (
	"context"
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
func (f *fakeChannels) TouchChannel(context.Context, int64, time.Time) error { f.touched++; return nil }

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
