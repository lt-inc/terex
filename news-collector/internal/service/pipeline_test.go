package service

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/metrics"
	"terex/news-collector/internal/repository/extract"
	"terex/news-collector/internal/repository/feed"
	"terex/news-collector/internal/repository/llm"
	"terex/news-collector/internal/repository/postgres"
	"terex/news-collector/internal/repository/safehttp"
	"terex/news-collector/internal/repository/telegram"
)

const goodToken = "123456789:AAFabcdefghijklmnopqrstuvwxyz012345"

func rssFeed(title string, items ...[3]string) string {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0"?><rss version="2.0"><channel><title>` + title + `</title>`)
	for _, it := range items {
		fmt.Fprintf(&b, `<item><title>%s</title><link>%s</link><description>%s</description><pubDate>%s</pubDate></item>`,
			it[0], it[1], it[2], time.Now().Add(-time.Hour).UTC().Format(time.RFC1123Z))
	}
	return b.String() + `</channel></rss>`
}

func page(text string) string {
	return `<html><body><article><p>` + text + `</p><p>` + text + `</p></article></body></html>`
}

// keyword embedding: [bitcoin, price, etf, football]
func fakeVector(text string) []float32 {
	text = strings.ToLower(text)
	v := make([]float32, 4)
	for i, k := range []string{"bitcoin", "price", "etf", "football"} {
		if strings.Contains(text, k) {
			v[i] = 1
		}
	}
	return v
}

// Needs a disposable PostgreSQL: it drops the news_collector schema. Never point it at production.
func TestPipelineEndToEnd(t *testing.T) {
	dsn := os.Getenv("NEWS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEWS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := postgres.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.Exec(ctx, `DROP SCHEMA IF EXISTS news_collector CASCADE`); err != nil {
		t.Fatal(err)
	}
	for range 2 { // migration must be repeatable
		if err = store.Migrate(ctx); err != nil {
			t.Fatal(err)
		}
	}

	long := strings.Repeat("Bitcoin price reached a new record today according to market data and analysts. ", 4)
	long2 := strings.Repeat("Spot bitcoin ETF approval pushed the price higher, traders said on Monday morning. ", 4)
	sport := strings.Repeat("The football club won the national cup after a dramatic penalty shootout last night. ", 4)
	pages := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a/feed":
			_, _ = w.Write([]byte(rssFeed("Outlet A", [3]string{"Bitcoin price hits record", "http://" + r.Host + "/a/1", "short"}, [3]string{"Football cup final", "http://" + r.Host + "/a/2", "short"})))
		case "/b/feed":
			_, _ = w.Write([]byte(rssFeed("Outlet B", [3]string{"Bitcoin ETF approval lifts price", "http://" + r.Host + "/b/1", "short"})))
		case "/a/1":
			_, _ = w.Write([]byte(page(long)))
		case "/a/2":
			_, _ = w.Write([]byte(page(sport)))
		case "/b/1":
			_, _ = w.Write([]byte(page(long2)))
		default:
			http.NotFound(w, r)
		}
	}))
	defer pages.Close()

	var llmCalls int
	var prompt string
	ai := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch r.URL.Path {
		case "/embeddings":
			var in struct{ Input []string }
			_ = json.Unmarshal(body, &in)
			type item struct {
				Index     int       `json:"index"`
				Embedding []float32 `json:"embedding"`
			}
			out := struct {
				Data []item `json:"data"`
			}{}
			for i, text := range in.Input {
				out.Data = append(out.Data, item{i, fakeVector(text)})
			}
			_ = json.NewEncoder(w).Encode(out)
		case "/chat/completions":
			llmCalls++
			var in struct {
				Messages []llm.Message `json:"messages"`
			}
			_ = json.Unmarshal(body, &in)
			prompt = in.Messages[len(in.Messages)-1].Content
			answer, _ := json.Marshal(map[string]any{"skip": false, "title": "**Биткоин обновил рекорд**", "text": "Цена биткоина выросла на фоне одобрения ETF.\n\nАналитики ждут продолжения роста."})
			_ = json.NewEncoder(w).Encode(map[string]any{"choices": []any{map[string]any{"message": map[string]string{"content": string(answer)}}}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer ai.Close()

	var mu sync.Mutex
	var sent []map[string]any
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/bot"+goodToken+"/") {
			w.WriteHeader(401)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
			return
		}
		var in map[string]any
		_ = json.NewDecoder(r.Body).Decode(&in)
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMe"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":777,"username":"news_bot"}}`))
		case strings.HasSuffix(r.URL.Path, "/getChat"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"id":-100500,"type":"channel","title":"My News"}}`))
		case strings.HasSuffix(r.URL.Path, "/getChatMember"):
			_, _ = w.Write([]byte(`{"ok":true,"result":{"status":"administrator","can_post_messages":true}}`))
		case strings.HasSuffix(r.URL.Path, "/sendMessage"):
			mu.Lock()
			sent = append(sent, in)
			mu.Unlock()
			_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":501}}`))
		}
	}))
	defer tg.Close()

	web := safehttp.New(10*time.Second, true) // test servers listen on loopback
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	sealer, _ := NewSealer(key)
	l := llm.New("test-key", "test-model", "test-embed")
	l.BaseURL = ai.URL
	svc := New(DefaultConfig(), store, &feed.Client{HTTP: web}, &extract.Client{HTTP: web}, l, telegram.New(tg.URL), sealer, slog.New(slog.NewTextHandler(io.Discard, nil)))

	// sources and channel
	a, err := svc.CreateSource(ctx, domain.Source{Name: "Outlet A", Kind: domain.SourceRSS, URL: pages.URL + "/a/feed", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	b, err := svc.CreateSource(ctx, domain.Source{Name: "Outlet B", Kind: domain.SourceRSS, URL: pages.URL + "/b/feed", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = svc.CreateSource(ctx, domain.Source{Name: "dup", Kind: domain.SourceRSS, URL: pages.URL + "/a/feed", Enabled: true}); err == nil {
		t.Fatal("a duplicate source must be rejected")
	}
	if _, err = svc.SaveChannel(ctx, domain.Channel{Title: "x", ChatID: "@news", BotToken: "bad"}); err == nil {
		t.Fatal("a malformed bot token must be rejected")
	}
	ch, err := svc.SaveChannel(ctx, domain.Channel{Title: "News", ChatID: "https://t.me/my_news", BotToken: goodToken})
	if err != nil {
		t.Fatal(err)
	}
	if ch.Status != "ok" || ch.BotUsername != "news_bot" || ch.ChatTitle != "My News" || !ch.HasToken || ch.ChatID != "@my_news" {
		t.Fatalf("channel not verified: %+v", ch)
	}
	if enc, _ := json.Marshal(ch); strings.Contains(string(enc), goodToken) || strings.Contains(string(enc), "bot_token\":\"") {
		t.Fatal("the bot token must never be returned")
	}
	var raw []byte
	if err = store.Exec(ctx, `SELECT 1`); err != nil {
		t.Fatal(err)
	}
	stored, _ := store.Channel(ctx, ch.ID)
	raw = stored.TokenEnc
	if strings.Contains(string(raw), "AAF") {
		t.Fatal("the bot token is stored in plain text")
	}

	// editing the title keeps the token and the verification; a new destination is re-verified
	renamed, err := svc.SaveChannel(ctx, domain.Channel{ID: ch.ID, Title: "News renamed", ChatID: "@my_news"})
	if err != nil || renamed.Title != "News renamed" || !renamed.HasToken || renamed.Status != "ok" {
		t.Fatalf("a rename must keep the token and status: %v %+v", err, renamed)
	}
	if kept, _ := store.Channel(ctx, ch.ID); string(kept.TokenEnc) != string(stored.TokenEnc) {
		t.Fatal("a rename must not touch the stored token")
	}
	moved, err := svc.SaveChannel(ctx, domain.Channel{ID: ch.ID, Title: "News renamed", ChatID: "@my_news"})
	if err != nil || moved.Status != "ok" {
		t.Fatalf("%v %+v", err, moved)
	}
	replaced, err := svc.SaveChannel(ctx, domain.Channel{ID: ch.ID, Title: "News renamed", ChatID: "@other_news", BotToken: goodToken})
	if err != nil || replaced.ChatID != "@other_news" || replaced.Status != "ok" || replaced.VerifiedAt == nil {
		t.Fatalf("a new destination must be verified again: %v %+v", err, replaced)
	}
	if _, err = svc.SaveChannel(ctx, domain.Channel{ID: ch.ID, Title: "News", ChatID: "@my_news"}); err != nil { // restore for the flow below
		t.Fatal(err)
	}

	flow, err := svc.SaveFlow(ctx, domain.Flow{Name: "Crypto", Enabled: true, Topic: "bitcoin", Language: "ru", ChannelID: &ch.ID,
		ApproveMode: domain.ApproveManual, SourceIDs: []int64{a.ID, b.ID}, PostsPerDay: 3, WindowStart: 0, WindowEnd: 24, MinSources: 2, IncludeLink: true})
	if err != nil {
		t.Fatal(err)
	}

	svc.tick(ctx) // a full scheduler pass, as in production: pipeline, then publishing
	if st, err := svc.Status(ctx); err != nil || st.LastTickError != "" || st.LastTickAt == nil {
		t.Fatalf("the pass must complete cleanly: %v %+v", err, st)
	}

	// the football article is off-topic: ingested, never clustered
	arts, _ := store.RecentArticles(ctx, flow.ID, 20)
	if len(arts) != 3 {
		t.Fatalf("expected 3 ingested articles, got %d", len(arts))
	}
	posts, _ := svc.ListPosts(ctx, flow.ID, "", nil, nil, 10)
	if len(posts) != 1 || posts[0].Status != domain.PostPending {
		t.Fatalf("by_approve flow must leave one pending draft: %+v", posts)
	}
	p := posts[0]
	if p.Title != "Биткоин обновил рекорд" || len(p.Links) != 2 || llmCalls != 1 {
		t.Fatalf("draft is wrong (markdown must be stripped, two source links): %+v calls=%d", p, llmCalls)
	}
	if !strings.Contains(prompt, "Outlet A") || !strings.Contains(prompt, "Outlet B") || strings.Contains(prompt, "football") {
		t.Errorf("the prompt must hold both stories' articles and nothing off-topic:\n%s", prompt)
	}
	if len(sent) != 0 {
		t.Fatal("nothing may be published before approval")
	}
	story, err := store.Cluster(ctx, *p.ClusterID)
	if err != nil || story.Articles != 2 || story.Sources != 2 || story.Status != "used" {
		t.Fatalf("the story must group both outlets' articles and be marked used: %v %+v", err, story)
	}
	if open, _ := svc.FlowCandidates(ctx, flow.ID); len(open) != 0 {
		t.Fatalf("a used story must not be offered again: %+v", open)
	}

	// the schedule gates a second draft; a second pass must not create one or call the model
	svc.pipeMu.Lock()
	_ = svc.pipeline(ctx)
	svc.pipeMu.Unlock()
	if again, _ := svc.ListPosts(ctx, flow.ID, "", nil, nil, 10); len(again) != 1 || llmCalls != 1 {
		t.Fatalf("the same story must not be drafted twice: %d posts, %d calls", len(again), llmCalls)
	}

	// edit, then approve: published immediately
	newText := "Исправленный текст поста."
	if _, err = svc.EditPost(ctx, p.ID, PostEdit{Text: &newText}); err != nil {
		t.Fatal(err)
	}
	done, err := svc.ApprovePost(ctx, p.ID, nil)
	if err != nil || done.Status != domain.PostPublished || done.MessageID == nil || *done.MessageID != 501 {
		t.Fatalf("approval must publish: %v %+v", err, done)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(sent) != 1 || sent[0]["parse_mode"] != "HTML" || sent[0]["chat_id"] != "@my_news" {
		t.Fatalf("telegram call: %+v", sent)
	}
	text := sent[0]["text"].(string)
	if !strings.Contains(text, "<b>Биткоин обновил рекорд</b>") || !strings.Contains(text, "Исправленный текст поста.") || !strings.Contains(text, `<a href="http://`) || !strings.Contains(text, "Источники:") {
		t.Errorf("message: %s", text)
	}
	if _, err = svc.ApprovePost(ctx, p.ID, nil); err == nil {
		t.Error("a published post cannot be approved again")
	}

	// the off-topic football article was judged under the old settings; changing the topic or the
	// threshold must make the flow look at it again, while an unrelated edit must not
	since := time.Now().Add(-72 * time.Hour)
	if left, _ := store.UnassignedArticles(ctx, flow.ID, since, 100); len(left) != 0 {
		t.Fatalf("every article has a verdict before the edit: %d", len(left))
	}
	cur, _ := svc.GetFlow(ctx, flow.ID)
	cur.StylePrompt = "коротко"
	if _, err = svc.SaveFlow(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if left, _ := store.UnassignedArticles(ctx, flow.ID, since, 100); len(left) != 0 {
		t.Fatalf("an unrelated edit must keep the verdicts: %d", len(left))
	}
	cur.MinRelevance = 0.3
	if _, err = svc.SaveFlow(ctx, cur); err != nil {
		t.Fatal(err)
	}
	if left, _ := store.UnassignedArticles(ctx, flow.ID, since, 100); len(left) != 1 {
		t.Fatalf("a new threshold must re-open the rejected article, got %d", len(left))
	}
	cur.Topic = "football"
	if _, err = svc.SaveFlow(ctx, cur); err != nil {
		t.Fatal(err)
	}

	// metrics: event counters moved, and the scrape-time gauges reflect the database
	fl := flowLabel(flow.ID)
	for name, got := range map[string]float64{
		"drafted":   testutil.ToFloat64(metrics.PostsDrafted.WithLabelValues(fl, domain.ApproveManual)),
		"published": testutil.ToFloat64(metrics.PostsPublished.WithLabelValues(fl)),
		"fetch ok":  testutil.ToFloat64(metrics.FeedFetches.WithLabelValues(domain.SourceRSS, "ok")),
		"ingested":  testutil.ToFloat64(metrics.ArticlesIngested.WithLabelValues(domain.SourceRSS)),
		"chat ok":   testutil.ToFloat64(metrics.LLMRequests.WithLabelValues("chat", "ok")),
		"tg send":   testutil.ToFloat64(metrics.TelegramRequests.WithLabelValues("sendMessage", "ok")),
		"pipeline":  testutil.ToFloat64(metrics.PipelineRuns.WithLabelValues("pipeline", "ok")),
	} {
		if got < 1 {
			t.Errorf("counter %q did not move: %v", name, got)
		}
	}
	if got := testutil.ToFloat64(metrics.PostsPublished.WithLabelValues(fl)); got != 1 {
		t.Errorf("exactly one post was published, counter says %v", got)
	}
	reg := prometheus.NewRegistry()
	reg.MustRegister(svc.MetricsCollector())
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	gauge := func(name, label, value string) float64 {
		for _, f := range families {
			if f.GetName() != name {
				continue
			}
			for _, m := range f.GetMetric() {
				if label == "" {
					return m.GetGauge().GetValue()
				}
				for _, l := range m.GetLabel() {
					if l.GetName() == label && l.GetValue() == value {
						return m.GetGauge().GetValue()
					}
				}
			}
		}
		return -1
	}
	if gauge("news_db_up", "", "") != 1 || gauge("news_llm_ready", "", "") != 1 || gauge("news_secret_key_ready", "", "") != 1 {
		t.Error("readiness gauges are wrong")
	}
	if gauge("news_posts", "status", "published") != 1 || gauge("news_posts", "status", "pending") != 0 || gauge("news_posts", "status", "failed") != 0 {
		t.Errorf("post gauges: published=%v pending=%v", gauge("news_posts", "status", "published"), gauge("news_posts", "status", "pending"))
	}
	if gauge("news_articles", "status", "ready") != 3 || gauge("news_articles", "status", "duplicate") != 0 {
		t.Errorf("article gauges: ready=%v", gauge("news_articles", "status", "ready"))
	}
	if gauge("news_flows", "state", "enabled") != 1 || gauge("news_sources", "state", "ok") != 2 || gauge("news_channels", "status", "ok") != 1 {
		t.Error("flow/source/channel gauges are wrong")
	}
	if gauge("news_last_post_published_timestamp_seconds", "flow", fl) < float64(time.Now().Add(-time.Minute).Unix()) {
		t.Error("the last-published timestamp is missing")
	}
}

func TestAutoApproveAndFailures(t *testing.T) {
	dsn := os.Getenv("NEWS_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("NEWS_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	store, err := postgres.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	_ = store.Exec(ctx, `DROP SCHEMA IF EXISTS news_collector CASCADE`)
	if err = store.Migrate(ctx); err != nil {
		t.Fatal(err)
	}

	calls := 0
	tg := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sendMessage") {
			calls++
			w.WriteHeader(403)
			_, _ = w.Write([]byte(`{"ok":false,"error_code":403,"description":"Forbidden: bot was kicked from the channel chat"}`))
			return
		}
		_, _ = w.Write([]byte(`{"ok":true,"result":{"id":1,"username":"b","type":"channel","title":"T","status":"administrator","can_post_messages":true}}`))
	}))
	defer tg.Close()
	key := base64.StdEncoding.EncodeToString([]byte(strings.Repeat("k", 32)))
	sealer, _ := NewSealer(key)
	svc := New(DefaultConfig(), store, &feed.Client{HTTP: safehttp.New(time.Second, true)}, &extract.Client{HTTP: safehttp.New(time.Second, true)}, llm.New("", "", ""), telegram.New(tg.URL), sealer, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ch, err := svc.SaveChannel(ctx, domain.Channel{Title: "News", ChatID: "@my_news", BotToken: goodToken})
	if err != nil || ch.Status != "ok" {
		t.Fatalf("%v %+v", err, ch)
	}
	src, _ := svc.CreateSource(ctx, domain.Source{Name: "s", Kind: domain.SourceGNews, Query: "bitcoin", Enabled: true})
	flow, err := svc.SaveFlow(ctx, domain.Flow{Name: "Auto", Enabled: true, ChannelID: &ch.ID, ApproveMode: domain.ApproveAuto, SourceIDs: []int64{src.ID}})
	if err != nil {
		t.Fatal(err)
	}
	// without an LLM key nothing is generated, and the flow reports why
	if _, err = svc.GenerateNow(ctx, flow.ID); err == nil {
		t.Fatal("generation without OPENROUTER_API_KEY must fail")
	}

	// a manually inserted approved post: the channel rejects the bot -> failed, channel flagged, no retry loop
	now := time.Now()
	id, err := store.CreatePost(ctx, domain.Post{FlowID: flow.ID, Title: "t", Text: "x", Links: []domain.Link{}, Status: domain.PostApproved, ScheduledAt: &now})
	if err != nil {
		t.Fatal(err)
	}
	if err = svc.publishDue(ctx); err != nil {
		t.Fatal(err)
	}
	p, _ := store.Post(ctx, id)
	if p.Status != domain.PostFailed || !strings.Contains(p.Error, "kicked") || calls != 1 {
		t.Fatalf("a permanent Telegram error must fail the post once: %+v calls=%d", p, calls)
	}
	if c, _ := store.Channel(ctx, ch.ID); c.Status != "error" {
		t.Errorf("the channel must be flagged: %+v", c)
	}
	if err = svc.publishDue(ctx); err != nil || calls != 1 {
		t.Fatal("a failed post must not be retried automatically")
	}
}
