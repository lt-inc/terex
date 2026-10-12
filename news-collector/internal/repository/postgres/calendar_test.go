package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"terex/news-collector/internal/domain"
)

func TestCalendarRangeAndHold(t *testing.T) {
	dsn := os.Getenv("NEWS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEWS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	s, err := New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Exec(ctx, `DROP SCHEMA IF EXISTS news_collector CASCADE`)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	f, err := s.SaveFlow(ctx, domain.Flow{Name: "f", Language: "ru", ApproveMode: "by_approve", PostsPerDay: 3, WindowStart: 9, WindowEnd: 22, Timezone: "UTC",
		MinIntervalMin: 60, MinSources: 1, MinRelevance: 0.25, WindowHours: 24, MaxPending: 5, MaxChars: 900, ImageMode: "auto", SourceIDs: []int64{}})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().UTC().Truncate(time.Hour).Add(-2 * time.Hour) // MarkPublished stamps the real clock, so the range must surround it
	mk := func(status string, at *time.Time) int64 {
		id, err := s.CreatePost(ctx, domain.Post{FlowID: f.ID, Title: status, Text: "t", Links: []domain.Link{}, Status: status, ScheduledAt: at})
		if err != nil {
			t.Fatal(err)
		}
		return id
	}
	in1, in2, out := base, base.Add(26*time.Hour), base.Add(10*24*time.Hour)
	queued := mk("approved", &in1)
	_ = mk("approved", &in2)
	_ = mk("approved", &out)
	_ = mk("pending", nil)
	from, to := base.Add(-time.Hour), base.Add(3*24*time.Hour)
	rows, err := s.ListPosts(ctx, 0, "", &from, &to, 100)
	if err != nil || len(rows) != 2 || rows[0].ID != queued || !rows[0].ScheduledAt.Before(*rows[1].ScheduledAt) {
		t.Fatalf("range must hold the two queued posts in time order, not the far one or the undated draft: %v %+v", err, rows)
	}
	if err = s.MarkPublished(ctx, queued, 77); err != nil {
		t.Fatal(err)
	}
	if rows, _ = s.ListPosts(ctx, 0, "published", &from, &to, 100); len(rows) != 1 {
		t.Fatalf("a published post sits at its publication time: %+v", rows)
	}
	other := mk("approved", &in1)
	if err = s.Hold(ctx, other); err != nil {
		t.Fatal(err)
	}
	p, _ := s.Post(ctx, other)
	if p.Status != "pending" || p.ScheduledAt != nil {
		t.Fatalf("hold must return the post to review without a time: %+v", p)
	}
	if err = s.Hold(ctx, other); err == nil {
		t.Fatal("a post that is not queued cannot be held")
	}
	all, _ := s.ListPosts(ctx, 0, "", nil, nil, 100)
	if len(all) != 5 || all[0].ID < all[len(all)-1].ID {
		t.Fatalf("without a range posts are newest first: %d", len(all))
	}
}
