package metrics

import (
	"context"
	"strconv"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// Snapshot is the database-derived state exported as gauges.
type Snapshot struct {
	PostsByStatus       map[string]int
	ArticlesByStatus    map[string]int
	SourcesByState      map[string]int // ok | failing | disabled
	ChannelsByStatus    map[string]int
	FlowsTotal          int
	FlowsEnabled        int
	OpenStories         map[int64]int
	OldestPendingAge    float64 // seconds, 0 when nothing waits for review
	LastPublishedAt     map[int64]time.Time
	OldestUnembeddedAge float64
}

// Collector reads a Snapshot on each scrape. Results are cached briefly so several scrapers
// (or a stuck database) cannot pile queries up.
type Collector struct {
	Load    func(context.Context) (Snapshot, error)
	Flags   func() (llmReady, secretKeyReady bool)
	TTL     time.Duration
	Timeout time.Duration

	mu     sync.Mutex
	at     time.Time
	cached Snapshot
	err    error
}

var (
	dPosts    = prometheus.NewDesc("news_posts", "Posts by status.", []string{"status"}, nil)
	dArticles = prometheus.NewDesc("news_articles", "Stored articles by processing status.", []string{"status"}, nil)
	dSources  = prometheus.NewDesc("news_sources", "Sources by state (ok|failing|disabled).", []string{"state"}, nil)
	dChannels = prometheus.NewDesc("news_channels", "Telegram channels by verification status.", []string{"status"}, nil)
	dFlows    = prometheus.NewDesc("news_flows", "Flows by state (total|enabled).", []string{"state"}, nil)
	dStories  = prometheus.NewDesc("news_open_stories", "Open stories waiting to be posted, by flow.", []string{"flow"}, nil)
	dOldest   = prometheus.NewDesc("news_oldest_pending_post_age_seconds", "Age of the oldest draft waiting for review.", nil, nil)
	dUnembed  = prometheus.NewDesc("news_oldest_unembedded_article_age_seconds", "Age of the oldest article still waiting for an embedding.", nil, nil)
	dLastPub  = prometheus.NewDesc("news_last_post_published_timestamp_seconds", "Unix time of the latest published post, by flow.", []string{"flow"}, nil)
	dDBUp     = prometheus.NewDesc("news_db_up", "1 when the state gauges could be read from the database.", nil, nil)
	dLLMReady = prometheus.NewDesc("news_llm_ready", "1 when OPENROUTER_API_KEY is configured.", nil, nil)
	dKeyReady = prometheus.NewDesc("news_secret_key_ready", "1 when NEWS_SECRET_KEY is configured.", nil, nil)
)

func (c *Collector) Describe(chan<- *prometheus.Desc) {} // unchecked collector

func (c *Collector) snapshot() (Snapshot, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ttl := c.TTL
	if ttl == 0 {
		ttl = 10 * time.Second
	}
	if !c.at.IsZero() && time.Since(c.at) < ttl {
		return c.cached, c.err
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	c.cached, c.err = c.Load(ctx)
	c.at = time.Now()
	return c.cached, c.err
}

func b2f(b bool) float64 {
	if b {
		return 1
	}
	return 0
}

func (c *Collector) Collect(ch chan<- prometheus.Metric) {
	if c.Flags != nil {
		llm, key := c.Flags()
		ch <- prometheus.MustNewConstMetric(dLLMReady, prometheus.GaugeValue, b2f(llm))
		ch <- prometheus.MustNewConstMetric(dKeyReady, prometheus.GaugeValue, b2f(key))
	}
	s, err := c.snapshot()
	ch <- prometheus.MustNewConstMetric(dDBUp, prometheus.GaugeValue, b2f(err == nil))
	if err != nil {
		return
	}
	// every known status is exported, including zeros, so rates and alerts never see a vanished series
	emit := func(d *prometheus.Desc, known []string, got map[string]int) {
		for _, k := range known {
			ch <- prometheus.MustNewConstMetric(d, prometheus.GaugeValue, float64(got[k]), k)
		}
	}
	emit(dPosts, []string{"pending", "approved", "sending", "published", "failed", "rejected", "expired"}, s.PostsByStatus)
	emit(dArticles, []string{"new", "extracted", "ready", "duplicate"}, s.ArticlesByStatus)
	emit(dSources, []string{"ok", "failing", "disabled"}, s.SourcesByState)
	emit(dChannels, []string{"ok", "error", "unverified"}, s.ChannelsByStatus)
	ch <- prometheus.MustNewConstMetric(dFlows, prometheus.GaugeValue, float64(s.FlowsTotal), "total")
	ch <- prometheus.MustNewConstMetric(dFlows, prometheus.GaugeValue, float64(s.FlowsEnabled), "enabled")
	for flow, n := range s.OpenStories {
		ch <- prometheus.MustNewConstMetric(dStories, prometheus.GaugeValue, float64(n), strconv.FormatInt(flow, 10))
	}
	for flow, t := range s.LastPublishedAt {
		ch <- prometheus.MustNewConstMetric(dLastPub, prometheus.GaugeValue, float64(t.Unix()), strconv.FormatInt(flow, 10))
	}
	ch <- prometheus.MustNewConstMetric(dOldest, prometheus.GaugeValue, s.OldestPendingAge)
	ch <- prometheus.MustNewConstMetric(dUnembed, prometheus.GaugeValue, s.OldestUnembeddedAge)
}
