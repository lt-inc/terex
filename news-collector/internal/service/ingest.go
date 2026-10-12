package service

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/metrics"
	"terex/news-collector/internal/repository/extract"
	"terex/news-collector/internal/repository/feed"
)

func feedURL(src domain.Source) string {
	if src.Kind == domain.SourceGNews {
		return feed.GNewsURL(src.Query, src.Lang)
	}
	return src.URL
}

// fetchSource downloads one feed and stores unseen items as new articles. Errors are
// recorded on the source so the panel can show them.
func (s *Service) fetchSource(ctx context.Context, src domain.Source) (int, error) {
	f, err := s.feeds.Fetch(ctx, feedURL(src))
	if err != nil {
		metrics.FeedFetches.WithLabelValues(src.Kind, "error").Inc()
		_ = s.store.MarkFetched(ctx, src.ID, err.Error())
		s.log.Warn("feed fetch failed", "source", src.ID, "err", err)
		return 0, err
	}
	metrics.FeedFetches.WithLabelValues(src.Kind, "ok").Inc()
	cutoff := s.now().Add(-s.cfg.MaxArticleAge)
	added := 0
	for i, it := range f.Items {
		if i >= 100 {
			break
		}
		published := it.Published
		if published.IsZero() {
			published = s.now()
		}
		if published.Before(cutoff) {
			continue
		}
		link := it.Link
		publisher := it.Publisher
		summary := it.Summary
		if src.Kind == domain.SourceGNews {
			summary = "" // Google's summary is just the headline and outlet again
			if real := decodeGNewsLink(link); real != "" {
				link = real
			}
		} else if publisher == "" {
			publisher = src.Name
		}
		ok, err := s.store.InsertArticle(ctx, domain.Article{
			SourceID: src.ID, URL: link, Title: it.Title, Summary: truncateRunes(summary, 4000),
			Publisher: publisher, Lang: src.Lang, PublishedAt: published, ImageURL: it.Image,
		}, urlHash(link))
		if err != nil {
			_ = s.store.MarkFetched(ctx, src.ID, "storing articles failed")
			return added, err
		}
		if ok {
			added++
		}
	}
	_ = s.store.MarkFetched(ctx, src.ID, "")
	metrics.ArticlesIngested.WithLabelValues(src.Kind).Add(float64(added))
	if added > 0 {
		s.log.Info("feed fetched", "source", src.ID, "new_articles", added)
	}
	return added, nil
}

type extracted struct {
	article domain.Article
	text    string
	image   string
}

// processNew pulls the article page, picks the best text, and drops near-identical copies.
func (s *Service) processNew(ctx context.Context) error {
	arts, err := s.store.ArticlesByStatus(ctx, "new", s.cfg.MaxExtractPerTick)
	if err != nil || len(arts) == 0 {
		return err
	}
	sources, err := s.store.Sources(ctx)
	if err != nil {
		return err
	}
	kinds := map[int64]string{}
	for _, src := range sources {
		kinds[src.ID] = src.Kind
	}
	results := make([]extracted, len(arts))
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(1, s.cfg.ExtractWorkers))
	for i, a := range arts {
		results[i] = extracted{article: a}
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			text := ""
			// Google News links that could not be resolved to a publisher page lead nowhere useful.
			if kinds[a.SourceID] == domain.SourceGNews && strings.Contains(a.URL, "news.google.com") {
				metrics.PageFetches.WithLabelValues("skipped").Inc()
			} else {
				pctx, cancel := contextWithTimeout(ctx, 20)
				page, err := s.pages.Fetch(pctx, a.URL)
				text, results[i].image = page.Text, page.Image
				cancel()
				switch {
				case err != nil:
					metrics.PageFetches.WithLabelValues("error").Inc()
				case text == "":
					metrics.PageFetches.WithLabelValues("empty").Inc()
				default:
					metrics.PageFetches.WithLabelValues("ok").Inc()
				}
			}
			if len([]rune(text)) < 300 && len([]rune(a.Summary)) > len([]rune(text)) {
				text = a.Summary
			}
			results[i].text = text
		}()
	}
	wg.Wait()

	hashes, err := s.store.RecentHashes(ctx, s.now().Add(-s.cfg.MaxArticleAge))
	if err != nil {
		return err
	}
	for _, r := range results {
		a := r.article
		text := truncateRunes(r.text, extract.MaxTextChars)
		lang := detectLang(a.Title + " " + text)
		var sim uint64
		if len([]rune(text)) >= 300 { // a headline alone is too short for a stable fingerprint
			sim = simhash(truncateRunes(a.Title+" "+text, 4000))
			if dup := nearDuplicate(hashes, a, sim, s.cfg.DupSimHashDistance); dup != 0 {
				if err = s.store.MarkDuplicate(ctx, a.ID, dup); err != nil {
					return err
				}
				metrics.ArticlesProcessed.WithLabelValues("duplicate_simhash").Inc()
				continue
			}
		}
		if err = s.store.SetExtracted(ctx, a.ID, text, lang, sim, r.image); err != nil {
			return err
		}
		metrics.ArticlesProcessed.WithLabelValues("extracted").Inc()
		if sim != 0 {
			hashes = append(hashes, postgresHash(a.ID, a.SourceID, sim))
		}
	}
	return nil
}

// embedPending embeds extracted articles and drops semantic duplicates (reprints).
func (s *Service) embedPending(ctx context.Context) error {
	if !s.llm.Ready() {
		return nil
	}
	arts, err := s.store.ArticlesByStatus(ctx, "extracted", s.cfg.EmbedBatch*2)
	if err != nil || len(arts) == 0 {
		return err
	}
	known, err := s.store.RecentEmbeddings(ctx, s.now().Add(-s.cfg.MaxArticleAge))
	if err != nil {
		return err
	}
	for start := 0; start < len(arts); start += s.cfg.EmbedBatch {
		batch := arts[start:min(start+s.cfg.EmbedBatch, len(arts))]
		inputs := make([]string, len(batch))
		for i, a := range batch {
			inputs[i] = embeddingText(a)
		}
		vectors, err := s.llm.Embed(ctx, inputs)
		if err != nil {
			return fmt.Errorf("embeddings: %w", err)
		}
		for i, a := range batch {
			v := vectors[i]
			dup := int64(0)
			for _, k := range known {
				if cosine(k.Vec, v) >= s.cfg.DupEmbedding {
					dup = k.ID
					break
				}
			}
			if dup != 0 {
				err = s.store.MarkDuplicate(ctx, a.ID, dup)
				metrics.ArticlesProcessed.WithLabelValues("duplicate_embedding").Inc()
			} else {
				err = s.store.SetEmbedding(ctx, a.ID, v)
				known = append(known, vecOf(a.ID, v))
				metrics.ArticlesProcessed.WithLabelValues("embedded").Inc()
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

func embeddingText(a domain.Article) string {
	body := a.Text
	if body == "" {
		body = a.Summary
	}
	return a.Title + "\n" + truncateRunes(body, 800)
}
