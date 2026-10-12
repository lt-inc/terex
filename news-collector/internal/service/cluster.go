package service

import (
	"context"
	"fmt"
	"math"
	"sort"
	"time"

	"terex/news-collector/internal/domain"
)

// assign evaluates new articles for one flow: off-topic ones are remembered and skipped,
// the rest join the closest open story or start a new one.
func (s *Service) assign(ctx context.Context, f domain.Flow) error {
	if !s.llm.Ready() {
		return nil
	}
	if f.Topic != "" && len(f.TopicEmbedding) == 0 {
		v, err := s.llm.Embed(ctx, []string{f.Topic})
		if err != nil {
			return fmt.Errorf("topic embedding: %w", err)
		}
		if err = s.store.SetTopicEmbedding(ctx, f.ID, f.Topic, v[0]); err != nil {
			return err
		}
		f.TopicEmbedding = v[0]
	}
	since := s.now().Add(-time.Duration(f.WindowHours) * time.Hour)
	arts, err := s.store.UnassignedArticles(ctx, f.ID, since, 300)
	if err != nil || len(arts) == 0 {
		return err
	}
	clusters, err := s.store.OpenClusters(ctx, f.ID, since)
	if err != nil {
		return err
	}
	for _, a := range arts {
		rel := 1.0
		if len(f.TopicEmbedding) > 0 {
			rel = cosine(a.Embedding, f.TopicEmbedding)
		}
		if rel < f.MinRelevance {
			if err = s.store.RecordIrrelevant(ctx, f.ID, a.ID, rel); err != nil {
				return err
			}
			continue
		}
		best, bestSim := -1, 0.0
		for i, c := range clusters {
			if sim := cosine(c.Centroid, a.Embedding); sim > bestSim {
				best, bestSim = i, sim
			}
		}
		if best >= 0 && bestSim >= s.cfg.ClusterThreshold {
			c := &clusters[best]
			c.Centroid = meanUpdate(c.Centroid, c.Articles, a.Embedding)
			c.Articles++
			if a.PublishedAt.After(c.LastAt) {
				c.LastAt = a.PublishedAt
			}
			if err = s.store.AddToCluster(ctx, f.ID, c.ID, a, rel, c.Centroid); err != nil {
				return err
			}
			continue
		}
		id, err := s.store.CreateCluster(ctx, f.ID, a, rel)
		if err != nil {
			return err
		}
		clusters = append(clusters, domain.Cluster{ID: id, FlowID: f.ID, Title: a.Title, Status: "open", Centroid: a.Embedding, Relevance: rel, Articles: 1, FirstAt: a.PublishedAt, LastAt: a.PublishedAt})
	}
	return nil
}

// score ranks stories: several independent outlets matter most, then topical fit, then freshness.
func score(c domain.Cluster, now time.Time) float64 {
	ageHours := now.Sub(c.LastAt).Hours()
	if ageHours < 0 {
		ageHours = 0
	}
	return 2*math.Log(1+float64(c.Sources)) + 3*c.Relevance + math.Exp(-ageHours/12)
}

// Candidates lists the flow's open stories ranked by score (also shown in the panel).
func (s *Service) Candidates(ctx context.Context, f domain.Flow) ([]domain.Cluster, error) {
	since := s.now().Add(-time.Duration(f.WindowHours) * time.Hour)
	clusters, err := s.store.OpenClusters(ctx, f.ID, since)
	if err != nil {
		return nil, err
	}
	for i := range clusters {
		clusters[i].Score = score(clusters[i], s.now())
	}
	sort.Slice(clusters, func(i, j int) bool { return clusters[i].Score > clusters[j].Score })
	return clusters, nil
}
