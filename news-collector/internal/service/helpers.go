package service

import (
	"context"
	"time"

	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/repository/postgres"
)

func contextWithTimeout(ctx context.Context, seconds int) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Duration(seconds)*time.Second)
}

func postgresHash(id, source int64, sim uint64) postgres.Hash {
	return postgres.Hash{ID: id, SourceID: source, SimHash: sim}
}

func vecOf(id int64, v []float32) postgres.Vec { return postgres.Vec{ID: id, Vec: v} }

// nearDuplicate returns the id of a known article whose fingerprint is within the distance.
func nearDuplicate(known []postgres.Hash, a domain.Article, sim uint64, distance int) int64 {
	if sim == 0 {
		return 0
	}
	for _, h := range known {
		if h.ID != a.ID && h.SimHash != 0 && hamming(h.SimHash, sim) <= distance {
			return h.ID
		}
	}
	return 0
}
