package service

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"terex/news-collector/internal/domain"
	"terex/news-collector/internal/metrics"
	"terex/news-collector/internal/repository/llm"
)

// effectiveInterval spreads a day's posts over the active window instead of bursting them.
func effectiveInterval(f domain.Flow) time.Duration {
	interval := time.Duration(f.MinIntervalMin) * time.Minute
	span := time.Duration(f.WindowEnd-f.WindowStart) * time.Hour
	if f.PostsPerDay > 0 && span > 0 {
		if spread := span / time.Duration(f.PostsPerDay) * 3 / 4; spread > interval {
			interval = spread
		}
	}
	return interval
}

// due reports whether the schedule allows a new draft right now.
func (s *Service) due(ctx context.Context, f domain.Flow) (bool, error) {
	loc, err := time.LoadLocation(f.Timezone)
	if err != nil {
		loc = time.UTC
	}
	local := s.now().In(loc)
	if h := local.Hour(); h < f.WindowStart || h >= f.WindowEnd {
		return false, nil
	}
	dayStart := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, loc)
	n, err := s.store.CountPosts(ctx, f.ID, dayStart)
	if err != nil || n >= f.PostsPerDay {
		return false, err
	}
	if f.ApproveMode == domain.ApproveManual {
		pending, err := s.store.CountPending(ctx, f.ID)
		if err != nil || pending >= f.MaxPending {
			return false, err
		}
	}
	last, err := s.store.LastPostCreated(ctx, f.ID)
	if err != nil {
		return false, err
	}
	if last != nil && s.now().Sub(*last) < effectiveInterval(f) {
		return false, nil
	}
	return true, nil
}

var errNothing = errors.New("no suitable story")

// generate drafts one post for the flow. With force the schedule limits are ignored (used by
// "generate now"); readiness checks always apply. It returns nil, nil when nothing is due.
func (s *Service) generate(ctx context.Context, f domain.Flow, force bool) (*domain.Post, error) {
	if !s.llm.Ready() {
		return nil, llm.ErrNotConfigured
	}
	if f.ChannelID == nil {
		return nil, fmt.Errorf("%w: the flow has no Telegram channel", domain.ErrInvalid)
	}
	if !force {
		ok, err := s.due(ctx, f)
		if err != nil || !ok {
			return nil, err
		}
	}
	if f.Topic != "" && len(f.TopicEmbedding) == 0 {
		if err := s.assign(ctx, f); err != nil {
			return nil, err
		}
	}
	candidates, err := s.Candidates(ctx, f)
	if err != nil {
		return nil, err
	}
	used, err := s.store.RecentUsedClusters(ctx, f.ID, s.now().Add(-7*24*time.Hour))
	if err != nil {
		return nil, err
	}
	tried := 0
	for _, c := range candidates {
		if c.Sources < f.MinSources {
			continue
		}
		if coveredBy(c, used, s.cfg.CoveredThreshold) {
			metrics.StoriesSkipped.WithLabelValues(flowLabel(f.ID), "covered").Inc()
			_ = s.store.SetClusterStatus(ctx, c.ID, "skipped")
			continue
		}
		if tried++; tried > 3 {
			break
		}
		post, err := s.draft(ctx, f, c)
		if errors.Is(err, errNothing) {
			metrics.StoriesSkipped.WithLabelValues(flowLabel(f.ID), "model_skip").Inc()
			_ = s.store.SetClusterStatus(ctx, c.ID, "skipped")
			continue
		}
		if err != nil {
			return nil, err
		}
		return post, nil
	}
	if force {
		return nil, fmt.Errorf("%w: no story with enough sources is available yet", domain.ErrConflict)
	}
	return nil, nil
}

func coveredBy(c domain.Cluster, used []domain.Cluster, threshold float64) bool {
	for _, u := range used {
		if cosine(c.Centroid, u.Centroid) >= threshold {
			return true
		}
	}
	return false
}

type draft struct {
	Skip   bool   `json:"skip"`
	Reason string `json:"reason"`
	Title  string `json:"title"`
	Text   string `json:"text"`
}

func (s *Service) draft(ctx context.Context, f domain.Flow, c domain.Cluster) (*domain.Post, error) {
	arts, err := s.store.ClusterArticles(ctx, c.ID, 6)
	if err != nil {
		return nil, err
	}
	if len(arts) == 0 {
		return nil, errNothing
	}
	var out draft
	if err = s.llm.JSON(ctx, f.Model, buildPrompt(f, arts), &out); err != nil {
		return nil, err
	}
	out.Title = strings.TrimSpace(cleanMarkup(out.Title))
	out.Text = cleanMarkup(out.Text)
	if out.Skip || out.Text == "" {
		s.log.Info("model skipped a story", "flow", f.ID, "cluster", c.ID, "reason", out.Reason)
		return nil, errNothing
	}
	out.Title = truncateRunes(out.Title, 120)
	out.Text = trimToLimit(out.Text, f.MaxChars)

	post := domain.Post{FlowID: f.ID, ClusterID: &c.ID, Title: out.Title, Text: out.Text, Links: []domain.Link{}, Status: domain.PostPending, Score: score(c, s.now())}
	if f.IncludeLink {
		seen := map[string]bool{}
		for _, a := range arts {
			name := a.Publisher
			if name == "" {
				name = a.SourceName
			}
			if seen[name] || len(post.Links) >= 3 {
				continue
			}
			seen[name] = true
			post.Links = append(post.Links, domain.Link{Title: name, URL: a.URL})
		}
	}
	if f.ApproveMode == domain.ApproveAuto {
		now := s.now()
		post.Status, post.ScheduledAt = domain.PostApproved, &now
	}
	id, err := s.store.CreatePost(ctx, post)
	if err != nil {
		return nil, err
	}
	if err = s.store.SetClusterStatus(ctx, c.ID, "used"); err != nil {
		return nil, err
	}
	_ = s.store.SetFlowResult(ctx, f.ID, true, "")
	metrics.PostsDrafted.WithLabelValues(flowLabel(f.ID), f.ApproveMode).Inc()
	s.log.Info("post drafted", "flow", f.ID, "post", id, "cluster", c.ID, "status", post.Status)
	created, err := s.store.Post(ctx, id)
	return &created, err
}

func buildPrompt(f domain.Flow, arts []domain.Article) []llm.Message {
	language := "Russian"
	if f.Language == "en" {
		language = "English"
	}
	topic := f.Topic
	if topic == "" {
		topic = "general news"
	}
	var sys strings.Builder
	fmt.Fprintf(&sys, "You are the editor of a Telegram news channel about: %s.\n", topic)
	sys.WriteString("Write ONE post about the story that the source articles below describe.\n\nRules:\n")
	fmt.Fprintf(&sys, "- Write in %s. Translate from the sources when they use another language.\n", language)
	sys.WriteString("- Use ONLY facts stated in the sources. Never invent numbers, quotes, names, dates or causes. If sources disagree, leave the disputed detail out or say they differ.\n")
	sys.WriteString("- Neutral, clear tone. No clickbait, no hashtags, at most one or two emoji.\n")
	fmt.Fprintf(&sys, "- Structure: a short headline (up to 90 characters) and a body of 2-4 short paragraphs, at most %d characters in total.\n", f.MaxChars)
	sys.WriteString("- Plain text only: no markdown, no HTML, no links (source links are added automatically).\n")
	sys.WriteString("- If the sources are not really about the channel's topic or hold too little factual content for a useful post, answer {\"skip\": true, \"reason\": \"...\"}.\n")
	if strings.TrimSpace(f.StylePrompt) != "" {
		fmt.Fprintf(&sys, "\nChannel style notes from the owner (follow them unless they conflict with the rules above):\n%s\n", strings.TrimSpace(f.StylePrompt))
	}
	sys.WriteString("\nAnswer with JSON only: {\"skip\": false, \"title\": \"...\", \"text\": \"...\"}")

	var user strings.Builder
	budget := 9000
	for i, a := range arts {
		body := a.Text
		if body == "" {
			body = a.Summary
		}
		body = truncateRunes(body, min(2500, max(400, budget)))
		budget -= len([]rune(body))
		name := a.Publisher
		if name == "" {
			name = a.SourceName
		}
		fmt.Fprintf(&user, "[%d] %s | %s | %s\n%s\n\n", i+1, name, a.PublishedAt.UTC().Format("2006-01-02 15:04"), a.Title, body)
		if budget <= 0 {
			break
		}
	}
	return []llm.Message{{Role: "system", Content: sys.String()}, {Role: "user", Content: user.String()}}
}

// cleanMarkup removes the markdown a model sometimes adds despite instructions.
func cleanMarkup(s string) string {
	s = strings.NewReplacer("**", "", "__", "", "`", "").Replace(s)
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(strings.TrimLeft(l, "# "), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func flowLabel(id int64) string { return strconv.FormatInt(id, 10) }
