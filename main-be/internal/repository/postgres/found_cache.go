package postgres

import (
	"context"
	"sync"
	"terex/proto/twitch"
	"time"
)

const foundTTL = time.Minute

// foundCache keeps per-flow source counts so the flows list does not scan
// twitch_flow_sources on every request. Reads never wait for a refresh.
type foundCache struct {
	mu         sync.Mutex
	counts     map[string]int
	at         time.Time
	refreshing bool
}

func (s *Store) fillFoundClips(flows []twitch.Flow) {
	c := &s.found
	c.mu.Lock()
	for n := range flows {
		flows[n].FoundClips = c.counts[flows[n].ID]
	}
	stale := !c.refreshing && time.Since(c.at) > foundTTL
	if stale {
		c.refreshing = true
	}
	c.mu.Unlock()
	if stale {
		go s.refreshFoundClips()
	}
}

func (s *Store) refreshFoundClips() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	counts := map[string]int{}
	rows, err := s.pool.Query(ctx, `SELECT flow_id::text,count(*) FROM twitch_flow_sources GROUP BY flow_id`)
	if err == nil {
		for rows.Next() {
			var id string
			var n int
			if err = rows.Scan(&id, &n); err != nil {
				break
			}
			counts[id] = n
		}
		rows.Close()
		if err == nil {
			err = rows.Err()
		}
	}
	c := &s.found
	c.mu.Lock()
	defer c.mu.Unlock()
	c.refreshing = false
	c.at = time.Now()
	if err == nil {
		c.counts = counts
	}
}
