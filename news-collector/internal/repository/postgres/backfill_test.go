package postgres

import (
	"context"
	"os"
	"testing"
	"time"

	"terex/news-collector/internal/domain"
)

func TestInsertArticleBackfillsImage(t *testing.T) {
	ctx := context.Background()
	s, err := New(ctx, os.Getenv("NEWS_TEST_DATABASE_URL"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_ = s.Exec(ctx, `DROP SCHEMA IF EXISTS news_collector CASCADE`)
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	src, _ := s.CreateSource(ctx, domain.Source{Name: "s", Kind: "rss", URL: "https://x/rss", Lang: "ru", Enabled: true, FetchIntervalMin: 30})
	a := domain.Article{SourceID: src.ID, URL: "https://x/1", Title: "t", PublishedAt: time.Now()}
	if ok, err := s.InsertArticle(ctx, a, "h1"); err != nil || !ok {
		t.Fatalf("first insert: %v %v", ok, err)
	}
	if ok, err := s.InsertArticle(ctx, a, "h1"); err != nil || ok {
		t.Fatalf("a repeat is not new: %v %v", ok, err)
	}
	a.ImageURL = "https://img/1.jpg"
	if ok, err := s.InsertArticle(ctx, a, "h1"); err != nil || ok {
		t.Fatalf("an image backfill is not a new article: %v %v", ok, err)
	}
	arts, _ := s.RecentArticles(ctx, 0, 5)
	if len(arts) != 1 || arts[0].ImageURL != "https://img/1.jpg" {
		t.Fatalf("the image must be filled in: %+v", arts)
	}
	a.ImageURL = "https://img/other.jpg"
	_, _ = s.InsertArticle(ctx, a, "h1")
	if arts, _ = s.RecentArticles(ctx, 0, 5); arts[0].ImageURL != "https://img/1.jpg" {
		t.Fatalf("an existing image must not be overwritten: %+v", arts)
	}
}
