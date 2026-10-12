// Package metrics holds the Prometheus instruments of the news collector. Event counters are
// incremented where the work happens; state gauges (queue sizes, post counts per status,
// failing sources) are read from the database when Prometheus scrapes, see Collector.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	FeedFetches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_feed_fetches_total", Help: "Feed downloads by source kind and result (ok|error)."}, []string{"kind", "result"})
	ArticlesIngested = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_articles_ingested_total", Help: "New articles stored from feeds, by source kind."}, []string{"kind"})
	PageFetches = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_page_fetches_total", Help: "Article page downloads by result (ok|empty|error|skipped)."}, []string{"result"})
	ArticlesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_articles_processed_total",
		Help: "Articles leaving a processing stage: extracted, embedded, duplicate_simhash, duplicate_embedding."}, []string{"outcome"})

	LLMRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_llm_requests_total", Help: "OpenRouter calls by kind (embedding|chat) and result (ok|error)."}, []string{"kind", "result"})
	LLMDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "news_llm_request_duration_seconds", Help: "OpenRouter call duration including retries.",
		Buckets: []float64{0.25, 0.5, 1, 2, 5, 10, 20, 45, 90, 180}}, []string{"kind"})

	PostsDrafted = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_posts_drafted_total", Help: "Posts generated, by flow and approve mode."}, []string{"flow", "mode"})
	StoriesSkipped = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_stories_skipped_total", Help: "Stories not posted: covered (already published) or model_skip."}, []string{"flow", "reason"})
	PostsPublished = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_posts_published_total", Help: "Posts delivered to Telegram."}, []string{"flow"})
	PostSendFailures = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_post_send_failures_total", Help: "Failed Telegram sends; outcome=retry (will try again) or failed (gave up)."}, []string{"flow", "outcome"})
	PostPublishDelay = promauto.NewHistogram(prometheus.HistogramOpts{
		Name: "news_post_publish_delay_seconds", Help: "Time from draft creation to publication (review latency).",
		Buckets: []float64{10, 60, 300, 900, 3600, 4 * 3600, 12 * 3600, 24 * 3600}})

	PostImages = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_post_images_total", Help: "Published posts by how the picture was delivered: photo, preview or none."}, []string{"mode"})
	ImagesSelected = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_images_selected_total", Help: "Pictures chosen for new drafts by source (article|pexels|none)."}, []string{"source"})
	ImageLookups = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_image_lookups_total", Help: "Image candidates tried: kind (article_check|pexels) and result (ok|error)."}, []string{"kind", "result"})

	TelegramRequests = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_telegram_requests_total", Help: "Bot API calls by method and result (ok|error|rate_limited)."}, []string{"method", "result"})
	TelegramDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "news_telegram_request_duration_seconds", Help: "Bot API call duration.",
		Buckets: []float64{0.1, 0.25, 0.5, 1, 2, 5, 10, 30}}, []string{"method"})

	PipelineRuns = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "news_pipeline_runs_total", Help: "Pipeline passes by stage (pipeline|publish) and result (ok|error)."}, []string{"stage", "result"})
	PipelineDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name: "news_pipeline_duration_seconds", Help: "Duration of a pipeline pass.",
		Buckets: []float64{0.5, 1, 2, 5, 10, 30, 60, 120, 300, 600}}, []string{"stage"})
	PipelineLastSuccess = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "news_pipeline_last_success_timestamp_seconds", Help: "Unix time of the last successful pass, by stage."}, []string{"stage"})
)
